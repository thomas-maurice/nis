package events

import (
	"reflect"

	"github.com/thomas-maurice/nis/internal/application/redact"
)

// Diff is the on-the-wire form of a field-level before/after change set,
// produced at a service-layer mutation site and persisted on the
// corresponding event row. Keys are caller-chosen field names; values are
// [before, after] pairs.
//
// The keys are NOT derived by reflection over the entity. Reflecting over
// entities.Account (etc.) would pick up noisy fields the caller didn't
// intend to track (UpdatedAt, regenerated JWT) and would silently break
// when an entity gains a field. Callers name every tracked field
// explicitly via DiffBuilder.Set / SetRedacted.
//
// Empty Diff (nil or zero-length) is the "no audit-visible change" signal:
// EmitTx writes NULL into events.diff in that case. Compare with !=nil
// rather than len()>0 if the distinction matters.
type Diff map[string][2]any

// DiffBuilder accumulates field changes at a service-layer mutation. Zero
// value is ready to use:
//
//	var diff events.DiffBuilder
//	diff.Set("name", before.Name, after.Name)
//	diff.SetRedacted("encrypted_seed", before.EncryptedSeed != after.EncryptedSeed)
//	events.EmitTx(ctx, tx, events.Event{ ..., Diff: diff.Finalize() })
//
// SAFETY: the caller must snapshot the field values BEFORE mutating the
// entity. Passing the same pointer pre- and post-mutation produces an empty
// diff. The canonical pattern is to assign the relevant fields into local
// variables before the mutation, then read the post-mutation values directly
// from the entity. See account_service.go UpdateAccount.
type DiffBuilder struct {
	d Diff
}

// Set records a change at key when before != after (reflect.DeepEqual). No-op
// otherwise. Values are stored raw and JSON-marshalled when EmitTx persists
// the event — pass scalars, slices, or pointer-optional values as-is. Use
// SetRedacted for secret material (keys hit by redact.HasSensitiveSuffix
// are ALSO redacted automatically — this just guarantees it explicitly).
func (b *DiffBuilder) Set(key string, before, after any) {
	if reflect.DeepEqual(before, after) {
		return
	}
	if redact.HasSensitiveSuffix(key) {
		b.set(key, [2]any{redact.Placeholder, redact.Placeholder})
		return
	}
	b.set(key, [2]any{before, after})
}

// SetRedacted records a redacted change at key when changed is true. The
// before/after values are NOT stored — only the redaction marker. Use for
// secret material whose field name does NOT carry a sensitive suffix:
// encrypted_seed, jwt, password_hash, etc. — anything the caller knows is
// sensitive but the suffix rule wouldn't catch.
func (b *DiffBuilder) SetRedacted(key string, changed bool) {
	if !changed {
		return
	}
	b.set(key, [2]any{redact.Placeholder, redact.Placeholder})
}

// Finalize returns the accumulated diff, or nil if no fields changed. The
// returned map is safe to pass to EmitTx via Event.Diff — nil round-trips
// to NULL in events.diff.
func (b *DiffBuilder) Finalize() Diff {
	if len(b.d) == 0 {
		return nil
	}
	return b.d
}

func (b *DiffBuilder) set(key string, v [2]any) {
	if b.d == nil {
		b.d = Diff{}
	}
	b.d[key] = v
}
