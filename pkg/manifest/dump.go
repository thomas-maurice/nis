package manifest

import (
	"bytes"
	"fmt"
	"time"

	nisv1 "github.com/thomas-maurice/nis/gen/nis/v1"
	"gopkg.in/yaml.v3"
)

// DumpObjects converts proto server state into a slice of manifest Objects in
// topological order (Operator → Cluster → Account → ScopedSigningKey → User).
//
// Filtering rules:
//   - Account "$SYS" is excluded.
//   - User "system" is excluded.
//   - ScopedSigningKey "default" is INCLUDED (needed for round-trip).
//
// The kinds parameter restricts which kinds are emitted. Pass all five
// constants to get the full tree.
func DumpObjects(
	op *nisv1.Operator,
	clusters []*nisv1.Cluster,
	accounts []*nisv1.Account,
	skks []*nisv1.ScopedSigningKey,
	users []*nisv1.User,
	kinds map[string]bool,
) []Object {
	// Build a lookup from SKK ID → name for user ScopedKey resolution.
	skkIDToName := make(map[string]string, len(skks))
	for _, sk := range skks {
		skkIDToName[sk.GetId()] = sk.GetName()
	}

	var out []Object

	// Operator
	if kinds[KindOperator] {
		out = append(out, operatorToObject(op))
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
						out = append(out, skkToObject(sk, op.GetName(), acc.GetName()))
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

func skkToObject(sk *nisv1.ScopedSigningKey, opName, accName string) Object {
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
	return Object{
		TypeMeta:         TypeMeta{APIVersion: APIVersion, Kind: KindScopedSigningKey},
		Metadata:         ObjectMeta{Name: sk.GetName(), Operator: opName, Account: accName},
		ScopedSigningKey: spec,
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
