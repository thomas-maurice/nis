package manifest

import (
	"errors"
	"fmt"
)

// ValidateOptions controls strictness of the Validate pass.
type ValidateOptions struct {
	// StrictRefs makes unresolved intra-batch references (e.g. a User
	// referencing a ScopedSigningKey not present in the batch) an error
	// instead of just surfacing them in ValidateResult.UnresolvedRefs.
	StrictRefs bool
}

// UnresolvedRef records a cross-object reference that cannot be confirmed
// within the parsed batch.
type UnresolvedRef struct {
	// Object is the identity string of the referencing object.
	Object string
	// Field is the field that holds the reference (e.g. "spec.scopedKey").
	Field string
	// Ref is the value of the reference (e.g. "writer").
	Ref string
}

func (r UnresolvedRef) String() string {
	return fmt.Sprintf("%s.%s=%q not found in batch", r.Object, r.Field, r.Ref)
}

// ValidateResult carries non-fatal findings from Validate.
type ValidateResult struct {
	UnresolvedRefs []UnresolvedRef
}

// Validate runs static validation on a batch of Objects. It returns a
// non-nil ValidateResult on success (possibly with UnresolvedRefs populated),
// and a non-nil error when a hard constraint is violated.
func Validate(objects []Object, opts ValidateOptions) (*ValidateResult, error) {
	var errs []error
	result := &ValidateResult{}

	// Track seen identities for duplicate detection.
	// Key: (kind, name, operator, account) — only the fields relevant per kind.
	type identity struct{ kind, name, operator, account string }
	seen := make(map[identity]string) // value = "source:docIdx" for diagnostics

	// Track ScopedSigningKeys by (operator, account, name) for ref resolution.
	type skkKey struct{ operator, account, name string }
	knownScopedKeys := make(map[skkKey]struct{})

	for _, obj := range objects {
		loc := objectLocation(obj)

		// Rule 1: apiVersion must be nis/v1.
		if obj.APIVersion != APIVersion {
			errs = append(errs, fmt.Errorf("manifest: %s: apiVersion must be %q, got %q", loc, APIVersion, obj.APIVersion))
		}

		// Rule 2: kind must be one of the five.
		switch obj.Kind {
		case KindOperator, KindCluster, KindAccount, KindScopedSigningKey, KindUser:
			// valid
		default:
			errs = append(errs, fmt.Errorf("manifest: %s: unknown kind %q", loc, obj.Kind))
			continue // no point validating further without a valid kind
		}

		// Metadata field presence rules.
		if err := validateMetadata(obj); err != nil {
			errs = append(errs, fmt.Errorf("manifest: %s: %w", loc, err))
		}

		// Reserved-name checks.
		if err := validateReservedNames(obj); err != nil {
			errs = append(errs, fmt.Errorf("manifest: %s: %w", loc, err))
		}

		// Rule 3: duplicate identity within batch.
		id := identity{
			kind:     obj.Kind,
			name:     obj.Metadata.Name,
			operator: obj.Metadata.Operator,
			account:  obj.Metadata.Account,
		}
		if prev, exists := seen[id]; exists {
			errs = append(errs, fmt.Errorf("manifest: %s: duplicate identity (first seen at %s)", loc, prev))
		} else {
			seen[id] = loc
		}

		// Collect known ScopedSigningKeys for Rule 4 ref resolution.
		if obj.Kind == KindScopedSigningKey {
			knownScopedKeys[skkKey{
				operator: obj.Metadata.Operator,
				account:  obj.Metadata.Account,
				name:     obj.Metadata.Name,
			}] = struct{}{}
		}

		if obj.Kind == KindCluster {
			if err := validateClusterSpec(obj); err != nil {
				errs = append(errs, fmt.Errorf("manifest: %s: %w", loc, err))
			}
		}
	}

	// Rule 4: resolve User.spec.scopedKey references within the batch.
	for _, obj := range objects {
		if obj.Kind != KindUser || obj.User == nil || obj.User.ScopedKey == "" {
			continue
		}
		loc := objectLocation(obj)
		key := skkKey{
			operator: obj.Metadata.Operator,
			account:  obj.Metadata.Account,
			name:     obj.User.ScopedKey,
		}
		if _, ok := knownScopedKeys[key]; !ok {
			ref := UnresolvedRef{
				Object: loc,
				Field:  "spec.scopedKey",
				Ref:    obj.User.ScopedKey,
			}
			if opts.StrictRefs {
				errs = append(errs, fmt.Errorf("manifest: %s: %w", loc, errors.New(ref.String())))
			} else {
				result.UnresolvedRefs = append(result.UnresolvedRefs, ref)
			}
		}
	}

	if len(errs) > 0 {
		return nil, errors.Join(errs...)
	}
	return result, nil
}

func objectLocation(obj Object) string {
	if obj.SourceFile != "" {
		return fmt.Sprintf("%s[%d](%s/%s)", obj.SourceFile, obj.DocIndex, obj.Kind, obj.Metadata.Name)
	}
	return fmt.Sprintf("doc[%d](%s/%s)", obj.DocIndex, obj.Kind, obj.Metadata.Name)
}

func validateMetadata(obj Object) error {
	if obj.Metadata.Name == "" {
		return fmt.Errorf("metadata.name is required")
	}
	switch obj.Kind {
	case KindOperator:
		if obj.Metadata.Operator != "" {
			return fmt.Errorf("metadata.operator must not be set on kind Operator")
		}
		if obj.Metadata.Account != "" {
			return fmt.Errorf("metadata.account must not be set on kind Operator")
		}
	case KindCluster:
		if obj.Metadata.Operator == "" {
			return fmt.Errorf("metadata.operator is required for kind Cluster")
		}
		if obj.Metadata.Account != "" {
			return fmt.Errorf("metadata.account must not be set on kind Cluster")
		}
	case KindAccount:
		if obj.Metadata.Operator == "" {
			return fmt.Errorf("metadata.operator is required for kind Account")
		}
		if obj.Metadata.Account != "" {
			return fmt.Errorf("metadata.account must not be set on kind Account")
		}
	case KindScopedSigningKey:
		if obj.Metadata.Operator == "" {
			return fmt.Errorf("metadata.operator is required for kind ScopedSigningKey")
		}
		if obj.Metadata.Account == "" {
			return fmt.Errorf("metadata.account is required for kind ScopedSigningKey")
		}
	case KindUser:
		if obj.Metadata.Operator == "" {
			return fmt.Errorf("metadata.operator is required for kind User")
		}
		if obj.Metadata.Account == "" {
			return fmt.Errorf("metadata.account is required for kind User")
		}
	}
	return nil
}

func validateReservedNames(obj Object) error {
	switch obj.Kind {
	case KindAccount:
		if obj.Metadata.Name == "$SYS" {
			return fmt.Errorf("metadata.name %q is reserved for kind Account", "$SYS")
		}
	case KindUser:
		if obj.Metadata.Name == "system" {
			return fmt.Errorf("metadata.name %q is reserved for kind User", "system")
		}
		// ScopedSigningKey "default" is explicitly allowed — callers may declare
		// it to customize permissions on the auto-created key.
	}
	return nil
}

func validateClusterSpec(obj Object) error {
	if obj.Cluster == nil {
		return nil
	}
	if len(obj.Cluster.ServerURLs) == 0 {
		return fmt.Errorf("spec.serverURLs must not be empty for kind Cluster")
	}
	return nil
}
