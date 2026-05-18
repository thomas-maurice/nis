package manifest

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"strings"

	"gopkg.in/yaml.v3"
)

// Parse decodes a multi-document YAML blob. source is used only for error
// messages (typically a file path or "<stdin>").
func Parse(data []byte, source string) ([]Object, error) {
	dec := yaml.NewDecoder(bytes.NewReader(data))
	dec.KnownFields(false) // unknown fields are silently ignored at parse time; validate catches them

	var objects []Object
	docIdx := 0
	for {
		var raw rawObject
		err := dec.Decode(&raw)
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			return nil, fmt.Errorf("manifest: parse doc %d in %s: %w", docIdx, source, err)
		}
		obj, err := materialize(raw, source, docIdx)
		if err != nil {
			return nil, fmt.Errorf("manifest: parse doc %d in %s: %w", docIdx, source, err)
		}
		objects = append(objects, obj)
		docIdx++
	}
	return objects, nil
}

// Load reads manifests from one or more paths. Paths that are directories are
// walked recursively for *.yaml and *.yml files. A path of "-" reads from
// os.Stdin.
func Load(paths []string) ([]Object, error) {
	var all []Object
	for _, p := range paths {
		objs, err := loadOne(p)
		if err != nil {
			return nil, err
		}
		all = append(all, objs...)
	}
	return all, nil
}

func loadOne(p string) ([]Object, error) {
	if p == "-" {
		data, err := io.ReadAll(os.Stdin)
		if err != nil {
			return nil, fmt.Errorf("manifest: read stdin: %w", err)
		}
		return Parse(data, "<stdin>")
	}

	info, err := os.Stat(p)
	if err != nil {
		return nil, fmt.Errorf("manifest: stat %s: %w", p, err)
	}

	if !info.IsDir() {
		return loadFile(p)
	}

	var all []Object
	err = filepath.WalkDir(p, func(path string, d fs.DirEntry, werr error) error {
		if werr != nil {
			return werr
		}
		if d.IsDir() {
			return nil
		}
		name := strings.ToLower(d.Name())
		if !strings.HasSuffix(name, ".yaml") && !strings.HasSuffix(name, ".yml") {
			return nil
		}
		objs, err := loadFile(path)
		if err != nil {
			return err
		}
		all = append(all, objs...)
		return nil
	})
	if err != nil {
		return nil, err
	}
	return all, nil
}

func loadFile(p string) ([]Object, error) {
	data, err := os.ReadFile(p)
	if err != nil {
		return nil, fmt.Errorf("manifest: read %s: %w", p, err)
	}
	return Parse(data, p)
}

// rawObject is the envelope parsed before the spec is type-switched.
type rawObject struct {
	APIVersion string     `yaml:"apiVersion"`
	Kind       string     `yaml:"kind"`
	Metadata   ObjectMeta `yaml:"metadata"`
	Spec       yaml.Node  `yaml:"spec"`
}

// materialize converts a rawObject into a fully-typed Object by unmarshalling
// the spec node into the appropriate typed struct.
func materialize(raw rawObject, source string, docIdx int) (Object, error) {
	obj := Object{
		TypeMeta: TypeMeta{
			APIVersion: raw.APIVersion,
			Kind:       raw.Kind,
		},
		Metadata:   raw.Metadata,
		SourceFile: source,
		DocIndex:   docIdx,
	}

	specNode := &raw.Spec
	// An empty YAML document has a null spec node.
	empty := specNode.Kind == 0

	switch raw.Kind {
	case KindOperator:
		var spec OperatorSpec
		if !empty {
			if err := specNode.Decode(&spec); err != nil {
				return Object{}, fmt.Errorf("spec: %w", err)
			}
		}
		obj.Operator = &spec

	case KindCluster:
		var spec ClusterSpec
		if !empty {
			if err := specNode.Decode(&spec); err != nil {
				return Object{}, fmt.Errorf("spec: %w", err)
			}
		}
		obj.Cluster = &spec

	case KindAccount:
		var spec AccountSpec
		if !empty {
			if err := specNode.Decode(&spec); err != nil {
				return Object{}, fmt.Errorf("spec: %w", err)
			}
		}
		obj.Account = &spec

	case KindScopedSigningKey:
		var spec ScopedSigningKeySpec
		if !empty {
			if err := specNode.Decode(&spec); err != nil {
				return Object{}, fmt.Errorf("spec: %w", err)
			}
		}
		obj.ScopedSigningKey = &spec

	case KindUser:
		var spec UserSpec
		if !empty {
			if err := specNode.Decode(&spec); err != nil {
				return Object{}, fmt.Errorf("spec: %w", err)
			}
		}
		obj.User = &spec

	default:
		// Unknown kinds are passed through with no spec set; Validate will reject them.
	}

	return obj, nil
}
