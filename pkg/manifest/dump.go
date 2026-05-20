package manifest

import (
	"bytes"
	"fmt"
	"time"

	nisv1 "github.com/thomas-maurice/nis/gen/nis/v1"
	"gopkg.in/yaml.v3"
)

// DumpObjects converts proto server state into a slice of manifest Objects in
// topological order (Operator → Template → Cluster → Account →
// ScopedSigningKey → User).
//
// Filtering rules:
//   - Account "$SYS" is excluded.
//   - User "system" is excluded.
//   - ScopedSigningKey "default" is INCLUDED (needed for round-trip).
//   - Templates dump their CURRENT version's permissions only — older
//     versions are not round-trippable through the manifest schema, by
//     design. Operators wanting to inspect history use `nisctl template
//     versions NAME`.
//
// The kinds parameter restricts which kinds are emitted. Pass all six
// constants to get the full tree.
func DumpObjects(
	op *nisv1.Operator,
	clusters []*nisv1.Cluster,
	accounts []*nisv1.Account,
	skks []*nisv1.ScopedSigningKey,
	users []*nisv1.User,
	templates []*TemplateWithCurrentVersion,
	kinds map[string]bool,
) []Object {
	// Build a lookup from SKK ID → name for user ScopedKey resolution.
	skkIDToName := make(map[string]string, len(skks))
	for _, sk := range skks {
		skkIDToName[sk.GetId()] = sk.GetName()
	}

	// Build a lookup from template ID → name for SKK template-ref resolution.
	tplIDToName := make(map[string]string, len(templates))
	for _, t := range templates {
		tplIDToName[t.Template.GetId()] = t.Template.GetName()
	}

	var out []Object

	// Operator
	if kinds[KindOperator] {
		out = append(out, operatorToObject(op))
	}

	// Templates (operator-scoped). Emit before Clusters/Accounts so an SKK
	// further down can reference one declared above.
	if kinds[KindTemplate] {
		for _, t := range templates {
			out = append(out, templateToObject(t, op.GetName()))
		}
	}

	// Clusters
	if kinds[KindCluster] {
		for _, cl := range clusters {
			out = append(out, clusterToObject(cl, op.GetName()))
		}
	}

	// Accounts (skip $SYS)
	if kinds[KindAccount] || kinds[KindScopedSigningKey] || kinds[KindUser] {
		for _, acc := range accounts {
			if acc.GetName() == "$SYS" {
				continue
			}
			if kinds[KindAccount] {
				out = append(out, accountToObject(acc, op.GetName()))
			}
			if kinds[KindScopedSigningKey] {
				for _, sk := range skks {
					if sk.GetAccountId() == acc.GetId() {
						out = append(out, skkToObject(sk, op.GetName(), acc.GetName(), tplIDToName))
					}
				}
			}
			if kinds[KindUser] {
				for _, u := range users {
					if u.GetAccountId() != acc.GetId() {
						continue
					}
					if u.GetName() == "system" {
						continue
					}
					out = append(out, userToObject(u, op.GetName(), acc.GetName(), skkIDToName))
				}
			}
		}
	}

	return out
}

func operatorToObject(op *nisv1.Operator) Object {
	spec := &OperatorSpec{
		Description: op.GetDescription(),
	}
	if op.GetUserJwtTtlSeconds() != 0 ||
		op.GetAccountJwtTtlSeconds() != 0 ||
		op.GetJwtWarnWindowSeconds() != 0 ||
		op.GetJwtAutoRenew() {
		spec.JWTPolicy = &JWTPolicySpec{
			UserJWTTTL:    time.Duration(op.GetUserJwtTtlSeconds()) * time.Second,
			AccountJWTTTL: time.Duration(op.GetAccountJwtTtlSeconds()) * time.Second,
			WarnWindow:    time.Duration(op.GetJwtWarnWindowSeconds()) * time.Second,
			AutoRenew:     op.GetJwtAutoRenew(),
		}
	}
	return Object{
		TypeMeta: TypeMeta{APIVersion: APIVersion, Kind: KindOperator},
		Metadata: ObjectMeta{Name: op.GetName()},
		Operator: spec,
	}
}

func clusterToObject(cl *nisv1.Cluster, opName string) Object {
	return Object{
		TypeMeta: TypeMeta{APIVersion: APIVersion, Kind: KindCluster},
		Metadata: ObjectMeta{Name: cl.GetName(), Operator: opName},
		Cluster: &ClusterSpec{
			Description: cl.GetDescription(),
			ServerURLs:  cl.GetServerUrls(),
		},
	}
}

func accountToObject(acc *nisv1.Account, opName string) Object {
	spec := &AccountSpec{
		Description: acc.GetDescription(),
	}
	lim := acc.GetJetstreamLimits()
	if lim != nil && (lim.GetEnabled() ||
		lim.GetMaxMemory() != 0 ||
		lim.GetMaxStorage() != 0 ||
		lim.GetMaxStreams() != 0 ||
		lim.GetMaxConsumers() != 0) {
		spec.JetStream = &JetStreamSpec{
			Enabled:      lim.GetEnabled(),
			MaxMemory:    lim.GetMaxMemory(),
			MaxStorage:   lim.GetMaxStorage(),
			MaxStreams:   int64(lim.GetMaxStreams()),
			MaxConsumers: int64(lim.GetMaxConsumers()),
		}
	}
	return Object{
		TypeMeta: TypeMeta{APIVersion: APIVersion, Kind: KindAccount},
		Metadata: ObjectMeta{Name: acc.GetName(), Operator: opName},
		Account:  spec,
	}
}

func skkToObject(sk *nisv1.ScopedSigningKey, opName, accName string, tplIDToName map[string]string) Object {
	spec := &ScopedSigningKeySpec{
		Description: sk.GetDescription(),
	}
	if p := sk.GetPermissions(); p != nil {
		spec.PubAllow = p.GetPubAllow()
		spec.PubDeny = p.GetPubDeny()
		spec.SubAllow = p.GetSubAllow()
		spec.SubDeny = p.GetSubDeny()
	}
	if r := sk.GetResponsePermission(); r != nil {
		spec.ResponseMaxMsgs = int(r.GetMaxMsgs())
		spec.ResponseTTL = time.Duration(r.GetExpires())
	}
	// Surface template binding when present. The version is always
	// emitted explicitly (even if it's the current latest) so a
	// round-tripped manifest preserves the pin and won't silently re-
	// snap on the next apply if a new template version ships before
	// the manifest is re-applied. TrackLatest is emitted alongside —
	// otherwise a dump→apply round-trip would silently strip the flag,
	// which is the exact silent-overwrite footgun we exist to prevent.
	if tid := sk.GetTemplateId(); tid != "" {
		if name, ok := tplIDToName[tid]; ok {
			spec.Template = name
			spec.TemplateVersion = int(sk.GetTemplateVersion())
		}
	}
	spec.TrackLatest = sk.GetTrackLatest()
	return Object{
		TypeMeta:         TypeMeta{APIVersion: APIVersion, Kind: KindScopedSigningKey},
		Metadata:         ObjectMeta{Name: sk.GetName(), Operator: opName, Account: accName},
		ScopedSigningKey: spec,
	}
}

// TemplateWithCurrentVersion pairs a template with its current latest
// version's permission snapshot for dump purposes. Callers fetch both
// via TemplateService.GetTemplate (without specifying a version_number).
type TemplateWithCurrentVersion struct {
	Template *nisv1.Template
	Version  *nisv1.TemplateVersion
}

func templateToObject(t *TemplateWithCurrentVersion, opName string) Object {
	spec := &TemplateSpec{
		Description: t.Template.GetDescription(),
	}
	if v := t.Version; v != nil {
		if p := v.GetPermissions(); p != nil {
			spec.PubAllow = p.GetPubAllow()
			spec.PubDeny = p.GetPubDeny()
			spec.SubAllow = p.GetSubAllow()
			spec.SubDeny = p.GetSubDeny()
		}
		if r := v.GetResponsePermission(); r != nil {
			spec.ResponseMaxMsgs = int(r.GetMaxMsgs())
			spec.ResponseTTL = time.Duration(r.GetExpires())
		}
		// ChangeNote intentionally NOT round-tripped: it describes a
		// historical bump event and would lie on the next apply.
	}
	return Object{
		TypeMeta: TypeMeta{APIVersion: APIVersion, Kind: KindTemplate},
		Metadata: ObjectMeta{Name: t.Template.GetName(), Operator: opName},
		Template: spec,
	}
}

func userToObject(u *nisv1.User, opName, accName string, skkIDToName map[string]string) Object {
	spec := &UserSpec{
		Description: u.GetDescription(),
	}
	if id := u.GetScopedSigningKeyId(); id != "" {
		spec.ScopedKey = skkIDToName[id]
	}
	if u.GetJwtTtlSeconds() != 0 {
		d := time.Duration(u.GetJwtTtlSeconds()) * time.Second
		spec.JWTTTL = &d
	}
	return Object{
		TypeMeta: TypeMeta{APIVersion: APIVersion, Kind: KindUser},
		Metadata: ObjectMeta{Name: u.GetName(), Operator: opName, Account: accName},
		User:     spec,
	}
}

// EncodeYAML encodes a slice of Objects as a multi-document YAML byte slice
// with "---\n" separators between documents.
func EncodeYAML(objs []Object) ([]byte, error) {
	// Each Object encodes as a struct with apiVersion, kind, metadata, spec.
	// We use a local wrapper because Object.Spec fields are tagged yaml:"-".
	type wireObject struct {
		APIVersion string     `yaml:"apiVersion"`
		Kind       string     `yaml:"kind"`
		Metadata   ObjectMeta `yaml:"metadata"`
		Spec       any        `yaml:"spec"`
	}

	var buf bytes.Buffer
	enc := yaml.NewEncoder(&buf)
	enc.SetIndent(2)

	for _, obj := range objs {
		var spec any
		switch obj.Kind {
		case KindOperator:
			spec = obj.Operator
		case KindCluster:
			spec = obj.Cluster
		case KindAccount:
			spec = obj.Account
		case KindScopedSigningKey:
			spec = obj.ScopedSigningKey
		case KindUser:
			spec = obj.User
		case KindTemplate:
			spec = obj.Template
		default:
			return nil, fmt.Errorf("manifest dump: unknown kind %q", obj.Kind)
		}
		wire := wireObject{
			APIVersion: obj.APIVersion,
			Kind:       obj.Kind,
			Metadata:   obj.Metadata,
			Spec:       spec,
		}
		if err := enc.Encode(wire); err != nil {
			return nil, fmt.Errorf("manifest dump: encode %s/%s: %w", obj.Kind, obj.Metadata.Name, err)
		}
	}

	if err := enc.Close(); err != nil {
		return nil, fmt.Errorf("manifest dump: close encoder: %w", err)
	}

	return buf.Bytes(), nil
}
