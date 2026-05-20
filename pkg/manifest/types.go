package manifest

import (
	"fmt"
	"strconv"
	"strings"
	"time"

	"gopkg.in/yaml.v3"
)

// APIVersion is the only supported apiVersion value.
const APIVersion = "nis/v1"

// Valid kinds.
const (
	KindOperator         = "Operator"
	KindCluster          = "Cluster"
	KindAccount          = "Account"
	KindScopedSigningKey = "ScopedSigningKey"
	KindUser             = "User"
	KindTemplate         = "Template"
)

// TypeMeta holds the discriminator fields present on every document.
type TypeMeta struct {
	APIVersion string `yaml:"apiVersion"`
	Kind       string `yaml:"kind"`
}

// ObjectMeta holds common identity fields. Which fields are required depends
// on Kind — see validateMetadata.
type ObjectMeta struct {
	Name     string `yaml:"name"`
	Operator string `yaml:"operator,omitempty"`
	Account  string `yaml:"account,omitempty"`
}

// Object is the parsed, type-switched representation of one YAML document.
// Exactly one spec pointer is non-nil; the others are nil.
type Object struct {
	TypeMeta `yaml:",inline"`
	Metadata ObjectMeta `yaml:"metadata"`

	Operator         *OperatorSpec         `yaml:"-"`
	Cluster          *ClusterSpec          `yaml:"-"`
	Account          *AccountSpec          `yaml:"-"`
	ScopedSigningKey *ScopedSigningKeySpec `yaml:"-"`
	User             *UserSpec             `yaml:"-"`
	Template         *TemplateSpec         `yaml:"-"`

	SourceFile string `yaml:"-"`
	DocIndex   int    `yaml:"-"`
}

// OperatorSpec is the spec block for kind: Operator.
type OperatorSpec struct {
	Description string         `yaml:"description"`
	JWTPolicy   *JWTPolicySpec `yaml:"jwtPolicy,omitempty"`
}

// JWTPolicySpec is the optional JWT lifecycle policy on an Operator.
// All duration fields are stored as time.Duration after parsing.
type JWTPolicySpec struct {
	UserJWTTTL    time.Duration
	AccountJWTTTL time.Duration
	WarnWindow    time.Duration
	AutoRenew     bool
}

type rawJWTPolicySpec struct {
	UserJWTTTL    string `yaml:"userJWTTTL"`
	AccountJWTTTL string `yaml:"accountJWTTTL"`
	WarnWindow    string `yaml:"warnWindow"`
	AutoRenew     bool   `yaml:"autoRenew"`
}

func (j *JWTPolicySpec) UnmarshalYAML(value *yaml.Node) error {
	var raw rawJWTPolicySpec
	if err := value.Decode(&raw); err != nil {
		return err
	}
	j.AutoRenew = raw.AutoRenew

	var err error
	if raw.UserJWTTTL != "" {
		if j.UserJWTTTL, err = parseDuration(raw.UserJWTTTL); err != nil {
			return fmt.Errorf("jwtPolicy.userJWTTTL: %w", err)
		}
	}
	if raw.AccountJWTTTL != "" {
		if j.AccountJWTTTL, err = parseDuration(raw.AccountJWTTTL); err != nil {
			return fmt.Errorf("jwtPolicy.accountJWTTTL: %w", err)
		}
	}
	if raw.WarnWindow != "" {
		if j.WarnWindow, err = parseDuration(raw.WarnWindow); err != nil {
			return fmt.Errorf("jwtPolicy.warnWindow: %w", err)
		}
	}
	return nil
}

func (j JWTPolicySpec) MarshalYAML() (any, error) {
	type out struct {
		UserJWTTTL    string `yaml:"userJWTTTL,omitempty"`
		AccountJWTTTL string `yaml:"accountJWTTTL,omitempty"`
		WarnWindow    string `yaml:"warnWindow,omitempty"`
		AutoRenew     bool   `yaml:"autoRenew"`
	}
	o := out{AutoRenew: j.AutoRenew}
	if j.UserJWTTTL != 0 {
		o.UserJWTTTL = j.UserJWTTTL.String()
	}
	if j.AccountJWTTTL != 0 {
		o.AccountJWTTTL = j.AccountJWTTTL.String()
	}
	if j.WarnWindow != 0 {
		o.WarnWindow = j.WarnWindow.String()
	}
	return o, nil
}

// ClusterSpec is the spec block for kind: Cluster.
type ClusterSpec struct {
	Description string   `yaml:"description"`
	ServerURLs  []string `yaml:"serverURLs"`
}

// AccountSpec is the spec block for kind: Account.
type AccountSpec struct {
	Description string         `yaml:"description"`
	JetStream   *JetStreamSpec `yaml:"jetStream,omitempty"`
}

// JetStreamSpec holds JetStream configuration. MaxMemory and MaxStorage are
// stored as int64 bytes after parsing the human-readable size strings.
type JetStreamSpec struct {
	Enabled      bool
	MaxMemory    int64
	MaxStorage   int64
	MaxStreams   int64
	MaxConsumers int64
}

type rawJetStreamSpec struct {
	Enabled      bool   `yaml:"enabled"`
	MaxMemory    string `yaml:"maxMemory"`
	MaxStorage   string `yaml:"maxStorage"`
	MaxStreams   int64  `yaml:"maxStreams"`
	MaxConsumers int64  `yaml:"maxConsumers"`
}

func (j *JetStreamSpec) UnmarshalYAML(value *yaml.Node) error {
	var raw rawJetStreamSpec
	if err := value.Decode(&raw); err != nil {
		return err
	}
	j.Enabled = raw.Enabled
	j.MaxStreams = raw.MaxStreams
	j.MaxConsumers = raw.MaxConsumers

	var err error
	if raw.MaxMemory != "" {
		if j.MaxMemory, err = parseSize(raw.MaxMemory); err != nil {
			return fmt.Errorf("jetStream.maxMemory: %w", err)
		}
	}
	if raw.MaxStorage != "" {
		if j.MaxStorage, err = parseSize(raw.MaxStorage); err != nil {
			return fmt.Errorf("jetStream.maxStorage: %w", err)
		}
	}
	return nil
}

func (j JetStreamSpec) MarshalYAML() (any, error) {
	type out struct {
		Enabled      bool   `yaml:"enabled"`
		MaxMemory    string `yaml:"maxMemory,omitempty"`
		MaxStorage   string `yaml:"maxStorage,omitempty"`
		MaxStreams   int64  `yaml:"maxStreams"`
		MaxConsumers int64  `yaml:"maxConsumers"`
	}
	o := out{
		Enabled:      j.Enabled,
		MaxStreams:   j.MaxStreams,
		MaxConsumers: j.MaxConsumers,
	}
	if j.MaxMemory != 0 {
		o.MaxMemory = formatSize(j.MaxMemory)
	}
	if j.MaxStorage != 0 {
		o.MaxStorage = formatSize(j.MaxStorage)
	}
	return o, nil
}

// parseSize parses a binary-unit size string into bytes.
// Accepts bare integers and XKi / XMi / XGi / XTi (base-1024).
// Decimal K/M/G/T are intentionally rejected.
func parseSize(s string) (int64, error) {
	suffixes := []struct {
		suffix string
		mult   int64
	}{
		{"Ti", 1 << 40},
		{"Gi", 1 << 30},
		{"Mi", 1 << 20},
		{"Ki", 1 << 10},
	}
	for _, sf := range suffixes {
		if strings.HasSuffix(s, sf.suffix) {
			n, err := strconv.ParseInt(s[:len(s)-len(sf.suffix)], 10, 64)
			if err != nil || n < 0 {
				return 0, fmt.Errorf("manifest: invalid size %q", s)
			}
			return n * sf.mult, nil
		}
	}
	n, err := strconv.ParseInt(s, 10, 64)
	if err != nil || n < 0 {
		return 0, fmt.Errorf("manifest: invalid size %q", s)
	}
	return n, nil
}

// formatSize renders bytes back to the most compact binary-unit string.
func formatSize(b int64) string {
	switch {
	case b >= 1<<40 && b%(1<<40) == 0:
		return fmt.Sprintf("%dTi", b/(1<<40))
	case b >= 1<<30 && b%(1<<30) == 0:
		return fmt.Sprintf("%dGi", b/(1<<30))
	case b >= 1<<20 && b%(1<<20) == 0:
		return fmt.Sprintf("%dMi", b/(1<<20))
	case b >= 1<<10 && b%(1<<10) == 0:
		return fmt.Sprintf("%dKi", b/(1<<10))
	default:
		return fmt.Sprintf("%d", b)
	}
}

// ScopedSigningKeySpec is the spec block for kind: ScopedSigningKey.
//
// When Template is non-empty, the SKK is created from that named
// template's snapshot and the permission fields above are IGNORED on
// create. Manifest-side updates to template binding are intentionally
// not supported in v1 — attaching, detaching, or bumping is an explicit
// nisctl/UI action so operators don't roll out new permissions by
// accident. The planner surfaces any pin/manifest mismatch as a note,
// not an action, so the discrepancy is visible without being applied.
type ScopedSigningKeySpec struct {
	Description     string
	PubAllow        []string
	PubDeny         []string
	SubAllow        []string
	SubDeny         []string
	ResponseMaxMsgs int
	ResponseTTL     time.Duration
	// Template references a Template by name within the same operator.
	// Optional; when set, the SKK is templated.
	Template string
	// TemplateVersion pins to a specific version. Zero means "current
	// latest at apply time"; non-zero requires that exact version exist.
	TemplateVersion int
	// TrackLatest opts the SKK into TemplateService.UpdateTemplate's
	// auto-propagation. Honoured only when Template != ""; otherwise
	// the apply call rejects. Coexists with TemplateVersion in the
	// schema but at apply time the SKK is created at the template's
	// current latest regardless of the pin (track + pin contradict).
	TrackLatest bool
}

func (s *ScopedSigningKeySpec) UnmarshalYAML(value *yaml.Node) error {
	type plain struct {
		Description     string   `yaml:"description"`
		PubAllow        []string `yaml:"pubAllow"`
		PubDeny         []string `yaml:"pubDeny"`
		SubAllow        []string `yaml:"subAllow"`
		SubDeny         []string `yaml:"subDeny"`
		ResponseMaxMsgs int      `yaml:"responseMaxMsgs"`
		ResponseTTL     string   `yaml:"responseTTL"`
		Template        string   `yaml:"template"`
		TemplateVersion int      `yaml:"templateVersion"`
		TrackLatest     bool     `yaml:"trackLatest"`
	}
	var p plain
	if err := value.Decode(&p); err != nil {
		return err
	}
	s.Description = p.Description
	s.PubAllow = p.PubAllow
	s.PubDeny = p.PubDeny
	s.SubAllow = p.SubAllow
	s.SubDeny = p.SubDeny
	s.ResponseMaxMsgs = p.ResponseMaxMsgs
	s.Template = p.Template
	s.TemplateVersion = p.TemplateVersion
	s.TrackLatest = p.TrackLatest

	if p.ResponseTTL != "" && p.ResponseTTL != "0s" && p.ResponseTTL != "0" {
		d, err := parseDuration(p.ResponseTTL)
		if err != nil {
			return fmt.Errorf("responseTTL: %w", err)
		}
		s.ResponseTTL = d
	}
	return nil
}

func (s ScopedSigningKeySpec) MarshalYAML() (any, error) {
	type out struct {
		Description     string   `yaml:"description,omitempty"`
		PubAllow        []string `yaml:"pubAllow"`
		PubDeny         []string `yaml:"pubDeny"`
		SubAllow        []string `yaml:"subAllow"`
		SubDeny         []string `yaml:"subDeny"`
		ResponseMaxMsgs int      `yaml:"responseMaxMsgs"`
		ResponseTTL     string   `yaml:"responseTTL"`
		Template        string   `yaml:"template,omitempty"`
		TemplateVersion int      `yaml:"templateVersion,omitempty"`
		TrackLatest     bool     `yaml:"trackLatest,omitempty"`
	}
	ttl := "0s"
	if s.ResponseTTL != 0 {
		ttl = s.ResponseTTL.String()
	}
	return out{
		Description:     s.Description,
		PubAllow:        s.PubAllow,
		PubDeny:         s.PubDeny,
		SubAllow:        s.SubAllow,
		SubDeny:         s.SubDeny,
		ResponseMaxMsgs: s.ResponseMaxMsgs,
		ResponseTTL:     ttl,
		Template:        s.Template,
		TemplateVersion: s.TemplateVersion,
		TrackLatest:     s.TrackLatest,
	}, nil
}

// TemplateSpec is the spec block for kind: Template. Operator-scoped
// versioned permission bundle. Permission fields define the v1 snapshot
// on create; UpdateTemplate compares incoming permissions against
// current latest and bumps the version when they differ.
type TemplateSpec struct {
	Description     string
	PubAllow        []string
	PubDeny         []string
	SubAllow        []string
	SubDeny         []string
	ResponseMaxMsgs int
	ResponseTTL     time.Duration
	// ChangeNote is attached to the new version row when a permission
	// edit bumps the version. Ignored when no bump happens.
	ChangeNote string
}

func (t *TemplateSpec) UnmarshalYAML(value *yaml.Node) error {
	type plain struct {
		Description     string   `yaml:"description"`
		PubAllow        []string `yaml:"pubAllow"`
		PubDeny         []string `yaml:"pubDeny"`
		SubAllow        []string `yaml:"subAllow"`
		SubDeny         []string `yaml:"subDeny"`
		ResponseMaxMsgs int      `yaml:"responseMaxMsgs"`
		ResponseTTL     string   `yaml:"responseTTL"`
		ChangeNote      string   `yaml:"changeNote"`
	}
	var p plain
	if err := value.Decode(&p); err != nil {
		return err
	}
	t.Description = p.Description
	t.PubAllow = p.PubAllow
	t.PubDeny = p.PubDeny
	t.SubAllow = p.SubAllow
	t.SubDeny = p.SubDeny
	t.ResponseMaxMsgs = p.ResponseMaxMsgs
	t.ChangeNote = p.ChangeNote

	if p.ResponseTTL != "" && p.ResponseTTL != "0s" && p.ResponseTTL != "0" {
		d, err := parseDuration(p.ResponseTTL)
		if err != nil {
			return fmt.Errorf("responseTTL: %w", err)
		}
		t.ResponseTTL = d
	}
	return nil
}

func (t TemplateSpec) MarshalYAML() (any, error) {
	type out struct {
		Description     string   `yaml:"description,omitempty"`
		PubAllow        []string `yaml:"pubAllow"`
		PubDeny         []string `yaml:"pubDeny"`
		SubAllow        []string `yaml:"subAllow"`
		SubDeny         []string `yaml:"subDeny"`
		ResponseMaxMsgs int      `yaml:"responseMaxMsgs"`
		ResponseTTL     string   `yaml:"responseTTL"`
		ChangeNote      string   `yaml:"changeNote,omitempty"`
	}
	ttl := "0s"
	if t.ResponseTTL != 0 {
		ttl = t.ResponseTTL.String()
	}
	return out{
		Description:     t.Description,
		PubAllow:        t.PubAllow,
		PubDeny:         t.PubDeny,
		SubAllow:        t.SubAllow,
		SubDeny:         t.SubDeny,
		ResponseMaxMsgs: t.ResponseMaxMsgs,
		ResponseTTL:     ttl,
		ChangeNote:      t.ChangeNote,
	}, nil
}

// UserSpec is the spec block for kind: User.
type UserSpec struct {
	Description string
	ScopedKey   string
	JWTTTL      *time.Duration
}

func (u *UserSpec) UnmarshalYAML(value *yaml.Node) error {
	type plain struct {
		Description string `yaml:"description"`
		ScopedKey   string `yaml:"scopedKey"`
		JWTTTL      string `yaml:"jwtTTL"`
	}
	var p plain
	if err := value.Decode(&p); err != nil {
		return err
	}
	u.Description = p.Description
	u.ScopedKey = p.ScopedKey

	if p.JWTTTL != "" {
		d, err := parseDuration(p.JWTTTL)
		if err != nil {
			return fmt.Errorf("jwtTTL: %w", err)
		}
		u.JWTTTL = &d
	}
	return nil
}

func (u UserSpec) MarshalYAML() (any, error) {
	type out struct {
		Description string `yaml:"description,omitempty"`
		ScopedKey   string `yaml:"scopedKey,omitempty"`
		JWTTTL      string `yaml:"jwtTTL,omitempty"`
	}
	o := out{Description: u.Description, ScopedKey: u.ScopedKey}
	if u.JWTTTL != nil {
		o.JWTTTL = u.JWTTTL.String()
	}
	return o, nil
}
