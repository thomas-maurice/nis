package manifest

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

// examplesDir returns the absolute path to example/manifests/ by walking up
// from this test file's location to the repo root, then descending.
func examplesDir(t *testing.T) string {
	t.Helper()
	// runtime.Caller(0) gives the path of this source file at compile time,
	// which is inside pkg/manifest/. The repo root is two levels up.
	_, thisFile, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("runtime.Caller returned !ok")
	}
	repoRoot := filepath.Join(filepath.Dir(thisFile), "..", "..")
	dir := filepath.Join(repoRoot, "example", "manifests")
	if _, err := os.Stat(dir); err != nil {
		t.Fatalf("example/manifests dir not found at %s: %v", dir, err)
	}
	return dir
}

// TestExampleManifests parses and validates every *.yaml file in example/manifests/.
// It is the CI gate that catches drift between pkg/manifest schema and the examples.
func TestExampleManifests(t *testing.T) {
	dir := examplesDir(t)

	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatalf("read dir %s: %v", dir, err)
	}

	var yamlFiles []string
	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		name := strings.ToLower(e.Name())
		if strings.HasSuffix(name, ".yaml") || strings.HasSuffix(name, ".yml") {
			yamlFiles = append(yamlFiles, filepath.Join(dir, e.Name()))
		}
	}

	if len(yamlFiles) == 0 {
		t.Fatal("no *.yaml files found in example/manifests — directory may be empty")
	}

	for _, path := range yamlFiles {
		path := path // capture
		t.Run(filepath.Base(path), func(t *testing.T) {
			data, err := os.ReadFile(path)
			if err != nil {
				t.Fatalf("read %s: %v", path, err)
			}

			objs, err := Parse(data, path)
			if err != nil {
				t.Fatalf("Parse: %v", err)
			}
			if len(objs) == 0 {
				t.Fatalf("Parse returned 0 objects from %s", path)
			}

			_, err = Validate(objs, ValidateOptions{StrictRefs: false})
			if err != nil {
				t.Fatalf("Validate(StrictRefs=false): %v", err)
			}
		})
	}
}

// TestFullStackManifest verifies full-stack.yaml specifically with StrictRefs=true —
// every User.spec.scopedKey reference must resolve to a ScopedSigningKey declared
// within the same file.
func TestFullStackManifest(t *testing.T) {
	dir := examplesDir(t)
	path := filepath.Join(dir, "full-stack.yaml")

	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}

	objs, err := Parse(data, path)
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}

	_, err = Validate(objs, ValidateOptions{StrictRefs: true})
	if err != nil {
		t.Fatalf("Validate(StrictRefs=true): %v", err)
	}
}
