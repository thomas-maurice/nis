package sql

import (
	"encoding/json"
	"time"

	"github.com/google/uuid"
	"github.com/thomas-maurice/nis/internal/domain/entities"
)

// Tag philosophy: these models are the source of truth for the schema.
// Atlas (atlas-provider-gorm) walks them to derive the desired DB shape and
// generates migrations against the dev DB. That means every constraint the
// schema must enforce — NOT NULL, defaults, foreign keys, CASCADE/RESTRICT,
// compound uniques, named indexes — has to live in a GORM tag here.
//
// Relation fields (e.g. `Operator *OperatorModel`) carry the FK constraint
// declaration and are otherwise unused: nil at all times, never preloaded,
// not serialized (we go through ToEntity/FromEntity). They only exist so
// GORM can derive the foreign-key + ON DELETE clause for the migration.

// OrganizationModel — organizations table. Top-level tenant; every operator
// belongs to exactly one organization. Slug is [a-z0-9-], unique, used for
// OIDC SSO routing (no discovery endpoint — callers must type the slug).
type OrganizationModel struct {
	ID          string    `gorm:"primaryKey;type:text;not null"`
	Name        string    `gorm:"type:text;not null;uniqueIndex:idx_organizations_name"`
	Slug        string    `gorm:"type:text;not null;uniqueIndex:idx_organizations_slug"`
	Description string    `gorm:"type:text"`
	CreatedAt   time.Time `gorm:"type:timestamp;not null;default:CURRENT_TIMESTAMP"`
	UpdatedAt   time.Time `gorm:"type:timestamp;not null;default:CURRENT_TIMESTAMP"`
}

func (OrganizationModel) TableName() string { return "organizations" }

func (m *OrganizationModel) ToEntity() *entities.Organization {
	return &entities.Organization{
		ID:          uuid.MustParse(m.ID),
		Name:        m.Name,
		Slug:        m.Slug,
		Description: m.Description,
		CreatedAt:   m.CreatedAt,
		UpdatedAt:   m.UpdatedAt,
	}
}

func OrganizationModelFromEntity(e *entities.Organization) *OrganizationModel {
	return &OrganizationModel{
		ID:          e.ID.String(),
		Name:        e.Name,
		Slug:        e.Slug,
		Description: e.Description,
		CreatedAt:   e.CreatedAt,
		UpdatedAt:   e.UpdatedAt,
	}
}

// OrganizationSSOConfigModel — organization_sso_configs table.
// 1:1 with organizations (unique on organization_id). The encrypted_client_secret
// uses the same storage-ref format as operator seeds.
//
// Scopes and group_claim are NOT NULL without a GORM default — the app layer
// always supplies them. Putting default:'openid...' here would trigger the GORM
// footgun: GORM substitutes the DB default for the Go zero value on INSERT,
// silently clobbering an explicit empty-string write. The entity layer sets the
// defaults ("openid profile email groups" and "groups" respectively).
type OrganizationSSOConfigModel struct {
	ID                    string              `gorm:"primaryKey;type:text;not null"`
	OrganizationID        string              `gorm:"type:text;not null;uniqueIndex:idx_org_sso_configs_org_id;index:idx_org_sso_configs_org_id_fk"`
	Organization          *OrganizationModel  `gorm:"foreignKey:OrganizationID;references:ID;constraint:OnDelete:CASCADE,OnUpdate:NO ACTION"`
	Enabled               bool               `gorm:"type:boolean;not null;default:false"`
	IssuerURL             string             `gorm:"type:text;not null;default:''"`
	ClientID              string             `gorm:"type:text;not null;default:''"`
	EncryptedClientSecret string             `gorm:"type:text;not null;default:''"`
	Scopes                string             `gorm:"type:text;not null"`
	GroupClaim            string             `gorm:"type:text;not null"`
	// DefaultRole is nullable: NULL means deny login when no group mapping matches.
	DefaultRole           *string            `gorm:"type:text"`
	CreatedAt             time.Time          `gorm:"type:timestamp;not null;default:CURRENT_TIMESTAMP"`
	UpdatedAt             time.Time          `gorm:"type:timestamp;not null;default:CURRENT_TIMESTAMP"`
}

func (OrganizationSSOConfigModel) TableName() string { return "organization_sso_configs" }

func (m *OrganizationSSOConfigModel) ToEntity() *entities.OrganizationSSOConfig {
	e := &entities.OrganizationSSOConfig{
		ID:                    uuid.MustParse(m.ID),
		OrganizationID:        uuid.MustParse(m.OrganizationID),
		Enabled:               m.Enabled,
		IssuerURL:             m.IssuerURL,
		ClientID:              m.ClientID,
		EncryptedClientSecret: m.EncryptedClientSecret,
		Scopes:                m.Scopes,
		GroupClaim:            m.GroupClaim,
		CreatedAt:             m.CreatedAt,
		UpdatedAt:             m.UpdatedAt,
	}
	if m.DefaultRole != nil {
		r := entities.APIUserRole(*m.DefaultRole)
		e.DefaultRole = &r
	}
	return e
}

func OrganizationSSOConfigModelFromEntity(e *entities.OrganizationSSOConfig) *OrganizationSSOConfigModel {
	m := &OrganizationSSOConfigModel{
		ID:                    e.ID.String(),
		OrganizationID:        e.OrganizationID.String(),
		Enabled:               e.Enabled,
		IssuerURL:             e.IssuerURL,
		ClientID:              e.ClientID,
		EncryptedClientSecret: e.EncryptedClientSecret,
		Scopes:                e.Scopes,
		GroupClaim:            e.GroupClaim,
		CreatedAt:             e.CreatedAt,
		UpdatedAt:             e.UpdatedAt,
	}
	if e.DefaultRole != nil {
		s := string(*e.DefaultRole)
		m.DefaultRole = &s
	}
	return m
}

// OrganizationSSORoleMappingModel — organization_sso_role_mappings table.
// Maps an IdP group value → NIS role+scope for one organization. Priority
// ascending (lower = checked first); first match wins during login.
// Unique on (organization_id, group_value).
type OrganizationSSORoleMappingModel struct {
	ID              string             `gorm:"primaryKey;type:text;not null"`
	OrganizationID  string             `gorm:"type:text;not null;index:idx_sso_role_mappings_org_id;uniqueIndex:idx_sso_role_mappings_org_group,priority:1"`
	Organization    *OrganizationModel `gorm:"foreignKey:OrganizationID;references:ID;constraint:OnDelete:CASCADE,OnUpdate:NO ACTION"`
	GroupValue      string             `gorm:"type:text;not null;uniqueIndex:idx_sso_role_mappings_org_group,priority:2"`
	Role            string             `gorm:"type:text;not null"`
	// ScopeOperatorID is required when Role == operator-admin. SET NULL on
	// operator delete so a deleted operator doesn't invalidate the whole mapping
	// row; the service layer validates the FK at mapping-create time.
	ScopeOperatorID *string            `gorm:"type:text;index:idx_sso_role_mappings_scope_op_id"`
	ScopeOperator   *OperatorModel     `gorm:"foreignKey:ScopeOperatorID;references:ID;constraint:OnDelete:SET NULL,OnUpdate:NO ACTION"`
	// ScopeAccountID is required when Role == account-admin. Same SET NULL logic.
	ScopeAccountID  *string            `gorm:"type:text;index:idx_sso_role_mappings_scope_acc_id"`
	ScopeAccount    *AccountModel      `gorm:"foreignKey:ScopeAccountID;references:ID;constraint:OnDelete:SET NULL,OnUpdate:NO ACTION"`
	Priority        int                `gorm:"type:integer;not null;default:0"`
	CreatedAt       time.Time          `gorm:"type:timestamp;not null;default:CURRENT_TIMESTAMP"`
}

func (OrganizationSSORoleMappingModel) TableName() string { return "organization_sso_role_mappings" }

func (m *OrganizationSSORoleMappingModel) ToEntity() *entities.SSORoleMapping {
	e := &entities.SSORoleMapping{
		ID:             uuid.MustParse(m.ID),
		OrganizationID: uuid.MustParse(m.OrganizationID),
		GroupValue:     m.GroupValue,
		Role:           entities.APIUserRole(m.Role),
		Priority:       m.Priority,
		CreatedAt:      m.CreatedAt,
	}
	if m.ScopeOperatorID != nil {
		id := uuid.MustParse(*m.ScopeOperatorID)
		e.ScopeOperatorID = &id
	}
	if m.ScopeAccountID != nil {
		id := uuid.MustParse(*m.ScopeAccountID)
		e.ScopeAccountID = &id
	}
	return e
}

func OrganizationSSORoleMappingModelFromEntity(e *entities.SSORoleMapping) *OrganizationSSORoleMappingModel {
	m := &OrganizationSSORoleMappingModel{
		ID:             e.ID.String(),
		OrganizationID: e.OrganizationID.String(),
		GroupValue:     e.GroupValue,
		Role:           string(e.Role),
		Priority:       e.Priority,
		CreatedAt:      e.CreatedAt,
	}
	if e.ScopeOperatorID != nil {
		s := e.ScopeOperatorID.String()
		m.ScopeOperatorID = &s
	}
	if e.ScopeAccountID != nil {
		s := e.ScopeAccountID.String()
		m.ScopeAccountID = &s
	}
	return m
}

// OIDCLoginStateModel — oidc_login_states table.
// Durable PKCE+state records for in-flight OIDC login flows. Swept by a
// recurring job. state is the PK (high-entropy random string).
type OIDCLoginStateModel struct {
	State          string             `gorm:"primaryKey;type:text;not null"`
	OrganizationID string             `gorm:"type:text;not null;index:idx_oidc_login_states_org_id"`
	Organization   *OrganizationModel `gorm:"foreignKey:OrganizationID;references:ID;constraint:OnDelete:CASCADE,OnUpdate:NO ACTION"`
	Nonce          string             `gorm:"type:text;not null"`
	PKCEVerifier   string             `gorm:"column:pkce_verifier;type:text;not null"`
	RedirectAfter  *string            `gorm:"type:text"`
	CreatedAt      time.Time          `gorm:"type:timestamp;not null;default:CURRENT_TIMESTAMP"`
	ExpiresAt      time.Time          `gorm:"type:timestamp;not null;index:idx_oidc_login_states_expires_at"`
}

func (OIDCLoginStateModel) TableName() string { return "oidc_login_states" }

func (m *OIDCLoginStateModel) ToEntity() *entities.OIDCLoginState {
	e := &entities.OIDCLoginState{
		State:          m.State,
		OrganizationID: uuid.MustParse(m.OrganizationID),
		Nonce:          m.Nonce,
		PKCEVerifier:   m.PKCEVerifier,
		RedirectAfter:  m.RedirectAfter,
		CreatedAt:      m.CreatedAt.UTC(),
		ExpiresAt:      m.ExpiresAt.UTC(),
	}
	return e
}

func OIDCLoginStateModelFromEntity(e *entities.OIDCLoginState) *OIDCLoginStateModel {
	return &OIDCLoginStateModel{
		State:          e.State,
		OrganizationID: e.OrganizationID.String(),
		Nonce:          e.Nonce,
		PKCEVerifier:   e.PKCEVerifier,
		RedirectAfter:  e.RedirectAfter,
		CreatedAt:      e.CreatedAt.UTC(),
		ExpiresAt:      e.ExpiresAt.UTC(),
	}
}

// OperatorModel — operators table.
type OperatorModel struct {
	ID                   string    `gorm:"primaryKey;type:text;not null"`
	Name                 string    `gorm:"type:text;not null;uniqueIndex:idx_operators_org_name,priority:2"`
	Description          string    `gorm:"type:text"`
	EncryptedSeed        string    `gorm:"type:text;not null"`
	PublicKey            string    `gorm:"type:text;not null;uniqueIndex:idx_operators_public_key"`
	JWT                  string    `gorm:"type:text;not null"`
	SystemAccountPubKey  string    `gorm:"type:text"`
	UserJWTTTLSeconds    int64     `gorm:"column:user_jwt_ttl_seconds;type:bigint;not null;default:0"`
	AccountJWTTTLSeconds int64     `gorm:"column:account_jwt_ttl_seconds;type:bigint;not null;default:0"`
	// No `default:` here: GORM would substitute it for the Go zero value (0),
	// silently changing the value of operators created with an unset warn
	// window. The entity layer is responsible for providing the value.
	JWTWarnWindowSeconds int64     `gorm:"column:jwt_warn_window_seconds;type:bigint;not null"`
	JWTAutoRenew         bool      `gorm:"column:jwt_auto_renew;type:boolean;not null;default:false"`
	// Backups (P12). Default disabled. When BackupEnabled is true,
	// BackupIntervalSeconds gates how often the sweep enqueues a backup.
	// CHECK constraint enforces the minimum 1h interval at the DB level.
	// BackupRetentionCount NULL means "keep forever"; explicit N enforces
	// keep-last-N. LastBackupAt is updated by BackupService.RunBackup.
	BackupEnabled         bool       `gorm:"column:backup_enabled;type:boolean;not null;default:false"`
	BackupIntervalSeconds *int64     `gorm:"column:backup_interval_seconds;type:bigint;check:backup_interval_seconds IS NULL OR backup_interval_seconds >= 3600"`
	BackupRetentionCount  *int       `gorm:"column:backup_retention_count;type:int"`
	LastBackupAt          *time.Time `gorm:"column:last_backup_at;type:timestamp"`
	// OrganizationID links the operator to its owning organization. Added in
	// migration 00009_add_organizations. Stored as a nullable column at the DB
	// level because the backfill runs in the migration before the NOT NULL
	// constraint is applied (see §5.1 of DESIGN.md and migration comments).
	// App-layer invariant: CreateOperator always sets this; rows in the DB
	// after migration are guaranteed non-NULL.
	OrganizationID   string             `gorm:"column:organization_id;type:text;not null;index:idx_operators_org_id;uniqueIndex:idx_operators_org_name,priority:1"`
	Organization     *OrganizationModel `gorm:"foreignKey:OrganizationID;references:ID;constraint:OnDelete:CASCADE,OnUpdate:NO ACTION"`
	CreatedAt        time.Time          `gorm:"type:timestamp;not null;default:CURRENT_TIMESTAMP"`
	UpdatedAt        time.Time          `gorm:"type:timestamp;not null;default:CURRENT_TIMESTAMP"`
}

func (OperatorModel) TableName() string { return "operators" }

func (m *OperatorModel) ToEntity() *entities.Operator {
	op := &entities.Operator{
		ID:                  uuid.MustParse(m.ID),
		Name:                m.Name,
		Description:         m.Description,
		EncryptedSeed:       m.EncryptedSeed,
		PublicKey:           m.PublicKey,
		JWT:                 m.JWT,
		SystemAccountPubKey: m.SystemAccountPubKey,
		UserJWTTTL:          time.Duration(m.UserJWTTTLSeconds) * time.Second,
		AccountJWTTTL:       time.Duration(m.AccountJWTTTLSeconds) * time.Second,
		JWTWarnWindow:       time.Duration(m.JWTWarnWindowSeconds) * time.Second,
		JWTAutoRenew:        m.JWTAutoRenew,
		BackupEnabled:       m.BackupEnabled,
		BackupRetention:     m.BackupRetentionCount,
		LastBackupAt:        m.LastBackupAt,
		CreatedAt:           m.CreatedAt,
		UpdatedAt:           m.UpdatedAt,
	}
	if m.BackupIntervalSeconds != nil {
		d := time.Duration(*m.BackupIntervalSeconds) * time.Second
		op.BackupInterval = &d
	}
	if m.OrganizationID != "" {
		op.OrganizationID = uuid.MustParse(m.OrganizationID)
	}
	return op
}

func OperatorModelFromEntity(e *entities.Operator) *OperatorModel {
	m := &OperatorModel{
		ID:                   e.ID.String(),
		Name:                 e.Name,
		Description:          e.Description,
		EncryptedSeed:        e.EncryptedSeed,
		PublicKey:            e.PublicKey,
		JWT:                  e.JWT,
		SystemAccountPubKey:  e.SystemAccountPubKey,
		UserJWTTTLSeconds:    int64(e.UserJWTTTL.Seconds()),
		AccountJWTTTLSeconds: int64(e.AccountJWTTTL.Seconds()),
		JWTWarnWindowSeconds: int64(e.JWTWarnWindow.Seconds()),
		JWTAutoRenew:         e.JWTAutoRenew,
		BackupEnabled:        e.BackupEnabled,
		BackupRetentionCount: e.BackupRetention,
		LastBackupAt:         e.LastBackupAt,
		OrganizationID:       e.OrganizationID.String(),
		CreatedAt:            e.CreatedAt,
		UpdatedAt:            e.UpdatedAt,
	}
	if e.BackupInterval != nil {
		s := int64(e.BackupInterval.Seconds())
		m.BackupIntervalSeconds = &s
	}
	return m
}

// AccountModel — accounts table.
type AccountModel struct {
	ID                    string `gorm:"primaryKey;type:text;not null"`
	OperatorID            string `gorm:"type:text;not null;index:idx_accounts_operator_id;uniqueIndex:idx_accounts_operator_name,priority:1"`
	Operator              *OperatorModel `gorm:"foreignKey:OperatorID;references:ID;constraint:OnDelete:CASCADE,OnUpdate:NO ACTION"`
	Name                  string `gorm:"type:text;not null;uniqueIndex:idx_accounts_operator_name,priority:2"`
	Description           string `gorm:"type:text"`
	EncryptedSeed         string `gorm:"type:text;not null"`
	PublicKey             string `gorm:"type:text;not null;uniqueIndex:idx_accounts_public_key"`
	JWT                   string `gorm:"type:text;not null"`
	JetStreamEnabled      bool   `gorm:"column:jetstream_enabled;type:boolean;not null;default:false"`
	// JetStream limits: NO `default:-1` here even though the schema intent is
	// "unlimited by default". GORM substitutes DEFAULT for the Go zero value
	// (0 = JetStream disabled), which would silently flip an explicit
	// "disabled" into "unlimited" on insert. Entity layer must always pass a
	// value.
	JetStreamMaxMemory    int64  `gorm:"column:jetstream_max_memory;type:bigint;not null"`
	JetStreamMaxStorage   int64  `gorm:"column:jetstream_max_storage;type:bigint;not null"`
	JetStreamMaxStreams   int64  `gorm:"column:jetstream_max_streams;type:bigint;not null"`
	JetStreamMaxConsumers int64  `gorm:"column:jetstream_max_consumers;type:bigint;not null"`
	CreatedAt             time.Time `gorm:"type:timestamp;not null;default:CURRENT_TIMESTAMP"`
	UpdatedAt             time.Time `gorm:"type:timestamp;not null;default:CURRENT_TIMESTAMP"`
}

func (AccountModel) TableName() string { return "accounts" }

func (m *AccountModel) ToEntity() *entities.Account {
	return &entities.Account{
		ID:                    uuid.MustParse(m.ID),
		OperatorID:            uuid.MustParse(m.OperatorID),
		Name:                  m.Name,
		Description:           m.Description,
		EncryptedSeed:         m.EncryptedSeed,
		PublicKey:             m.PublicKey,
		JWT:                   m.JWT,
		JetStreamEnabled:      m.JetStreamEnabled,
		JetStreamMaxMemory:    m.JetStreamMaxMemory,
		JetStreamMaxStorage:   m.JetStreamMaxStorage,
		JetStreamMaxStreams:   m.JetStreamMaxStreams,
		JetStreamMaxConsumers: m.JetStreamMaxConsumers,
		CreatedAt:             m.CreatedAt,
		UpdatedAt:             m.UpdatedAt,
	}
}

func AccountModelFromEntity(e *entities.Account) *AccountModel {
	return &AccountModel{
		ID:                    e.ID.String(),
		OperatorID:            e.OperatorID.String(),
		Name:                  e.Name,
		Description:           e.Description,
		EncryptedSeed:         e.EncryptedSeed,
		PublicKey:             e.PublicKey,
		JWT:                   e.JWT,
		JetStreamEnabled:      e.JetStreamEnabled,
		JetStreamMaxMemory:    e.JetStreamMaxMemory,
		JetStreamMaxStorage:   e.JetStreamMaxStorage,
		JetStreamMaxStreams:   e.JetStreamMaxStreams,
		JetStreamMaxConsumers: e.JetStreamMaxConsumers,
		CreatedAt:             e.CreatedAt,
		UpdatedAt:             e.UpdatedAt,
	}
}

// ScopedSigningKeyModel — scoped_signing_keys table.
type ScopedSigningKeyModel struct {
	ID              string         `gorm:"primaryKey;type:text;not null"`
	AccountID       string         `gorm:"type:text;not null;index:idx_scoped_signing_keys_account_id;uniqueIndex:idx_scoped_signing_keys_account_name,priority:1"`
	Account         *AccountModel  `gorm:"foreignKey:AccountID;references:ID;constraint:OnDelete:CASCADE,OnUpdate:NO ACTION"`
	Name            string         `gorm:"type:text;not null;uniqueIndex:idx_scoped_signing_keys_account_name,priority:2"`
	Description     string         `gorm:"type:text"`
	EncryptedSeed   string         `gorm:"type:text;not null"`
	PublicKey       string         `gorm:"type:text;not null;uniqueIndex:idx_scoped_signing_keys_public_key"`
	PubAllow        []string       `gorm:"type:text;serializer:json"`
	PubDeny         []string       `gorm:"type:text;serializer:json"`
	SubAllow        []string       `gorm:"type:text;serializer:json"`
	SubDeny         []string       `gorm:"type:text;serializer:json"`
	ResponseMaxMsgs int            `gorm:"type:integer;not null;default:0"`
	ResponseTTLSecs int64          `gorm:"column:response_ttl_seconds;type:bigint;not null;default:0"`
	// Template ref: SET NULL on template delete (not RESTRICT) so an operator
	// cascade isn't deadlocked by its own children. The "can't delete a
	// template with dependents" guard lives in TemplateService at the app
	// layer; this FK is just the safety net. The CHECK keeps template_id and
	// template_version in lockstep — both NULL or both set — so the
	// bump/detach logic never has to handle a "pinned to template X but no
	// version" half-state.
	TemplateID      *string         `gorm:"type:text;index:idx_scoped_signing_keys_template_id;check:chk_scoped_signing_keys_template_pair,(template_id IS NULL) = (template_version IS NULL)"`
	Template        *TemplateModel  `gorm:"foreignKey:TemplateID;references:ID;constraint:OnDelete:SET NULL,OnUpdate:NO ACTION"`
	TemplateVersion *int            `gorm:"type:integer"`
	TemplateDrifted bool            `gorm:"type:boolean;not null;default:false"`
	// TrackLatest opts this SSK into TemplateService.UpdateTemplate's
	// auto-propagation: when a new template_versions row is created, every
	// SSK with track_latest=true gets its perm columns snapshotted from
	// the new version and its parent account JWT re-signed + pushed.
	// Service-layer invariants (not enforced by FK/CHECK): only allowed
	// when template_id IS NOT NULL AND template_drifted=false.
	TrackLatest     bool            `gorm:"type:boolean;not null;default:false"`
	// IsPlainSigner marks SSKs that were imported from an NSC store
	// where the parent account JWT lists this key as a plain string in
	// signing_keys (not as a UserScope). For these keys NIS must:
	//   1) emit them as plain strings on account-JWT regen (so existing
	//      user JWTs whose own perms were authoritative remain valid),
	//   2) NOT call SetScoped(true) on user JWTs we mint signed by them
	//      (NATS would then apply the user JWT's perms directly, and a
	//      SetScoped-zeroed JWT has subs:0/payload:0 = locked-out user).
	// Default false: every NIS-native SSK is a real UserScope.
	IsPlainSigner   bool            `gorm:"type:boolean;not null;default:false"`
	CreatedAt       time.Time       `gorm:"type:timestamp;not null;default:CURRENT_TIMESTAMP"`
	UpdatedAt       time.Time       `gorm:"type:timestamp;not null;default:CURRENT_TIMESTAMP"`
}

func (ScopedSigningKeyModel) TableName() string { return "scoped_signing_keys" }

func (m *ScopedSigningKeyModel) ToEntity() *entities.ScopedSigningKey {
	out := &entities.ScopedSigningKey{
		ID:              uuid.MustParse(m.ID),
		AccountID:       uuid.MustParse(m.AccountID),
		Name:            m.Name,
		Description:     m.Description,
		EncryptedSeed:   m.EncryptedSeed,
		PublicKey:       m.PublicKey,
		PubAllow:        m.PubAllow,
		PubDeny:         m.PubDeny,
		SubAllow:        m.SubAllow,
		SubDeny:         m.SubDeny,
		ResponseMaxMsgs: m.ResponseMaxMsgs,
		ResponseTTL:     time.Duration(m.ResponseTTLSecs) * time.Second,
		TemplateVersion: m.TemplateVersion,
		TemplateDrifted: m.TemplateDrifted,
		TrackLatest:     m.TrackLatest,
		IsPlainSigner:   m.IsPlainSigner,
		CreatedAt:       m.CreatedAt,
		UpdatedAt:       m.UpdatedAt,
	}
	if m.TemplateID != nil && *m.TemplateID != "" {
		id := uuid.MustParse(*m.TemplateID)
		out.TemplateID = &id
	}
	return out
}

func ScopedSigningKeyModelFromEntity(e *entities.ScopedSigningKey) *ScopedSigningKeyModel {
	m := &ScopedSigningKeyModel{
		ID:              e.ID.String(),
		AccountID:       e.AccountID.String(),
		Name:            e.Name,
		Description:     e.Description,
		EncryptedSeed:   e.EncryptedSeed,
		PublicKey:       e.PublicKey,
		PubAllow:        e.PubAllow,
		PubDeny:         e.PubDeny,
		SubAllow:        e.SubAllow,
		SubDeny:         e.SubDeny,
		ResponseMaxMsgs: e.ResponseMaxMsgs,
		ResponseTTLSecs: int64(e.ResponseTTL.Seconds()),
		TemplateVersion: e.TemplateVersion,
		TemplateDrifted: e.TemplateDrifted,
		TrackLatest:     e.TrackLatest,
		IsPlainSigner:   e.IsPlainSigner,
		CreatedAt:       e.CreatedAt,
		UpdatedAt:       e.UpdatedAt,
	}
	if e.TemplateID != nil {
		s := e.TemplateID.String()
		m.TemplateID = &s
	}
	return m
}

// TemplateModel — templates table. Operator-scoped permission template; the
// permission bytes live in template_versions, this row is just identity +
// "what's the latest version number to pin against".
type TemplateModel struct {
	ID            string         `gorm:"primaryKey;type:text;not null"`
	OperatorID    string         `gorm:"type:text;not null;index:idx_templates_operator_id;uniqueIndex:idx_templates_operator_name,priority:1"`
	Operator      *OperatorModel `gorm:"foreignKey:OperatorID;references:ID;constraint:OnDelete:CASCADE,OnUpdate:NO ACTION"`
	Name          string         `gorm:"type:text;not null;uniqueIndex:idx_templates_operator_name,priority:2"`
	Description   string         `gorm:"type:text"`
	LatestVersion int            `gorm:"type:integer;not null;default:0"`
	CreatedAt     time.Time      `gorm:"type:timestamp;not null;default:CURRENT_TIMESTAMP"`
	UpdatedAt     time.Time      `gorm:"type:timestamp;not null;default:CURRENT_TIMESTAMP"`
}

func (TemplateModel) TableName() string { return "templates" }

func (m *TemplateModel) ToEntity() *entities.Template {
	return &entities.Template{
		ID:            uuid.MustParse(m.ID),
		OperatorID:    uuid.MustParse(m.OperatorID),
		Name:          m.Name,
		Description:   m.Description,
		LatestVersion: m.LatestVersion,
		CreatedAt:     m.CreatedAt,
		UpdatedAt:     m.UpdatedAt,
	}
}

func TemplateModelFromEntity(e *entities.Template) *TemplateModel {
	return &TemplateModel{
		ID:            e.ID.String(),
		OperatorID:    e.OperatorID.String(),
		Name:          e.Name,
		Description:   e.Description,
		LatestVersion: e.LatestVersion,
		CreatedAt:     e.CreatedAt,
		UpdatedAt:     e.UpdatedAt,
	}
}

// TemplateVersionModel — template_versions table. Immutable snapshots; one
// row per (template, version_number). created_by_user_id is SET NULL on
// the parent api_user delete so offboarding a human doesn't erase audit
// history.
type TemplateVersionModel struct {
	ID              string         `gorm:"primaryKey;type:text;not null"`
	TemplateID      string         `gorm:"type:text;not null;index:idx_template_versions_template_id;uniqueIndex:idx_template_versions_template_number,priority:1"`
	Template        *TemplateModel `gorm:"foreignKey:TemplateID;references:ID;constraint:OnDelete:CASCADE,OnUpdate:NO ACTION"`
	VersionNumber   int            `gorm:"type:integer;not null;uniqueIndex:idx_template_versions_template_number,priority:2"`
	PubAllow        []string       `gorm:"type:text;serializer:json"`
	PubDeny         []string       `gorm:"type:text;serializer:json"`
	SubAllow        []string       `gorm:"type:text;serializer:json"`
	SubDeny         []string       `gorm:"type:text;serializer:json"`
	ResponseMaxMsgs int            `gorm:"type:integer;not null;default:0"`
	ResponseTTLSecs int64          `gorm:"column:response_ttl_seconds;type:bigint;not null;default:0"`
	ChangeNote      string         `gorm:"type:text;not null;default:''"`
	CreatedAt       time.Time      `gorm:"type:timestamp;not null;default:CURRENT_TIMESTAMP"`
	CreatedByUserID *string        `gorm:"type:text;index:idx_template_versions_created_by_user_id"`
	CreatedByUser   *APIUserModel  `gorm:"foreignKey:CreatedByUserID;references:ID;constraint:OnDelete:SET NULL,OnUpdate:NO ACTION"`
}

func (TemplateVersionModel) TableName() string { return "template_versions" }

func (m *TemplateVersionModel) ToEntity() *entities.TemplateVersion {
	out := &entities.TemplateVersion{
		ID:              uuid.MustParse(m.ID),
		TemplateID:      uuid.MustParse(m.TemplateID),
		VersionNumber:   m.VersionNumber,
		PubAllow:        m.PubAllow,
		PubDeny:         m.PubDeny,
		SubAllow:        m.SubAllow,
		SubDeny:         m.SubDeny,
		ResponseMaxMsgs: m.ResponseMaxMsgs,
		ResponseTTL:     time.Duration(m.ResponseTTLSecs) * time.Second,
		ChangeNote:      m.ChangeNote,
		CreatedAt:       m.CreatedAt,
	}
	if m.CreatedByUserID != nil && *m.CreatedByUserID != "" {
		id := uuid.MustParse(*m.CreatedByUserID)
		out.CreatedByUserID = &id
	}
	return out
}

func TemplateVersionModelFromEntity(e *entities.TemplateVersion) *TemplateVersionModel {
	m := &TemplateVersionModel{
		ID:              e.ID.String(),
		TemplateID:      e.TemplateID.String(),
		VersionNumber:   e.VersionNumber,
		PubAllow:        e.PubAllow,
		PubDeny:         e.PubDeny,
		SubAllow:        e.SubAllow,
		SubDeny:         e.SubDeny,
		ResponseMaxMsgs: e.ResponseMaxMsgs,
		ResponseTTLSecs: int64(e.ResponseTTL.Seconds()),
		ChangeNote:      e.ChangeNote,
		CreatedAt:       e.CreatedAt,
	}
	if e.CreatedByUserID != nil {
		s := e.CreatedByUserID.String()
		m.CreatedByUserID = &s
	}
	return m
}

// UserModel — users table.
type UserModel struct {
	ID                  string                 `gorm:"primaryKey;type:text;not null"`
	AccountID           string                 `gorm:"type:text;not null;index:idx_users_account_id;uniqueIndex:idx_users_account_name,priority:1"`
	Account             *AccountModel          `gorm:"foreignKey:AccountID;references:ID;constraint:OnDelete:CASCADE,OnUpdate:NO ACTION"`
	Name                string                 `gorm:"type:text;not null;uniqueIndex:idx_users_account_name,priority:2"`
	Description         string                 `gorm:"type:text"`
	EncryptedSeed       string                 `gorm:"type:text;not null"`
	PublicKey           string                 `gorm:"type:text;not null;uniqueIndex:idx_users_public_key"`
	JWT                 string                 `gorm:"type:text;not null"`
	ScopedSigningKeyID  *string                `gorm:"type:text;index:idx_users_scoped_signing_key_id"`
	ScopedSigningKey    *ScopedSigningKeyModel `gorm:"foreignKey:ScopedSigningKeyID;references:ID;constraint:OnDelete:SET NULL,OnUpdate:NO ACTION"`
	JWTTTLSeconds       *int64                 `gorm:"column:jwt_ttl_seconds;type:bigint"`
	JWTIssuedAt         *time.Time             `gorm:"column:jwt_issued_at;type:timestamp"`
	JWTExpiresAt        *time.Time             `gorm:"column:jwt_expires_at;type:timestamp;index:idx_users_jwt_expires_at"`
	RevokedAt           *time.Time             `gorm:"column:revoked_at;type:timestamp;index:idx_users_revoked_at"`
	RevocationReason    string                 `gorm:"column:revocation_reason;type:text;not null;default:''"`
	LastExpiringWarnIAT *time.Time             `gorm:"column:last_expiring_warn_iat;type:timestamp"`
	LastExpiredAlertIAT *time.Time             `gorm:"column:last_expired_alert_iat;type:timestamp"`
	CreatedAt           time.Time              `gorm:"type:timestamp;not null;default:CURRENT_TIMESTAMP"`
	UpdatedAt           time.Time              `gorm:"type:timestamp;not null;default:CURRENT_TIMESTAMP"`
}

func (UserModel) TableName() string { return "users" }

func (m *UserModel) ToEntity() *entities.User {
	var scopedKeyID *uuid.UUID
	if m.ScopedSigningKeyID != nil && *m.ScopedSigningKeyID != "" {
		id := uuid.MustParse(*m.ScopedSigningKeyID)
		scopedKeyID = &id
	}
	var ttl *time.Duration
	if m.JWTTTLSeconds != nil {
		d := time.Duration(*m.JWTTTLSeconds) * time.Second
		ttl = &d
	}

	return &entities.User{
		ID:                  uuid.MustParse(m.ID),
		AccountID:           uuid.MustParse(m.AccountID),
		Name:                m.Name,
		Description:         m.Description,
		EncryptedSeed:       m.EncryptedSeed,
		PublicKey:           m.PublicKey,
		JWT:                 m.JWT,
		ScopedSigningKeyID:  scopedKeyID,
		JWTTTL:              ttl,
		JWTIssuedAt:         m.JWTIssuedAt,
		JWTExpiresAt:        m.JWTExpiresAt,
		RevokedAt:           m.RevokedAt,
		RevocationReason:    m.RevocationReason,
		LastExpiringWarnIAT: m.LastExpiringWarnIAT,
		LastExpiredAlertIAT: m.LastExpiredAlertIAT,
		CreatedAt:           m.CreatedAt,
		UpdatedAt:           m.UpdatedAt,
	}
}

func UserModelFromEntity(e *entities.User) *UserModel {
	var scopedKeyID *string
	if e.ScopedSigningKeyID != nil {
		s := e.ScopedSigningKeyID.String()
		scopedKeyID = &s
	}
	var ttlSecs *int64
	if e.JWTTTL != nil {
		s := int64(e.JWTTTL.Seconds())
		ttlSecs = &s
	}

	return &UserModel{
		ID:                  e.ID.String(),
		AccountID:           e.AccountID.String(),
		Name:                e.Name,
		Description:         e.Description,
		EncryptedSeed:       e.EncryptedSeed,
		PublicKey:           e.PublicKey,
		JWT:                 e.JWT,
		ScopedSigningKeyID:  scopedKeyID,
		JWTTTLSeconds:       ttlSecs,
		JWTIssuedAt:         e.JWTIssuedAt,
		JWTExpiresAt:        e.JWTExpiresAt,
		RevokedAt:           e.RevokedAt,
		RevocationReason:    e.RevocationReason,
		LastExpiringWarnIAT: e.LastExpiringWarnIAT,
		LastExpiredAlertIAT: e.LastExpiredAlertIAT,
		CreatedAt:           e.CreatedAt,
		UpdatedAt:           e.UpdatedAt,
	}
}

// UserJWTRevocationModel — user_jwt_revocations table.
type UserJWTRevocationModel struct {
	ID            string        `gorm:"primaryKey;type:text;not null"`
	AccountID     string        `gorm:"type:text;not null;index:idx_user_jwt_revocations_account_id"`
	Account       *AccountModel `gorm:"foreignKey:AccountID;references:ID;constraint:OnDelete:CASCADE,OnUpdate:NO ACTION"`
	UserID        *string       `gorm:"type:text"`
	User          *UserModel    `gorm:"foreignKey:UserID;references:ID;constraint:OnDelete:SET NULL,OnUpdate:NO ACTION"`
	UserPublicKey string        `gorm:"type:text;not null"`
	RevokedAt     time.Time     `gorm:"type:timestamp;not null"`
	JWTExp        time.Time     `gorm:"column:jwt_exp;type:timestamp;not null;index:idx_user_jwt_revocations_jwt_exp"`
	Reason        string        `gorm:"type:text;not null;default:''"`
	PrunedAt      *time.Time    `gorm:"type:timestamp;index:idx_user_jwt_revocations_pruned_at"`
	CreatedAt     time.Time     `gorm:"type:timestamp;not null;default:CURRENT_TIMESTAMP"`
}

func (UserJWTRevocationModel) TableName() string { return "user_jwt_revocations" }

func (m *UserJWTRevocationModel) ToEntity() *entities.UserJWTRevocation {
	out := &entities.UserJWTRevocation{
		ID:            uuid.MustParse(m.ID),
		AccountID:     uuid.MustParse(m.AccountID),
		UserPublicKey: m.UserPublicKey,
		RevokedAt:     m.RevokedAt,
		JWTExp:        m.JWTExp,
		Reason:        m.Reason,
		PrunedAt:      m.PrunedAt,
		CreatedAt:     m.CreatedAt,
	}
	if m.UserID != nil && *m.UserID != "" {
		id := uuid.MustParse(*m.UserID)
		out.UserID = &id
	}
	return out
}

func UserJWTRevocationModelFromEntity(e *entities.UserJWTRevocation) *UserJWTRevocationModel {
	m := &UserJWTRevocationModel{
		ID:            e.ID.String(),
		AccountID:     e.AccountID.String(),
		UserPublicKey: e.UserPublicKey,
		RevokedAt:     e.RevokedAt,
		JWTExp:        e.JWTExp,
		Reason:        e.Reason,
		PrunedAt:      e.PrunedAt,
		CreatedAt:     e.CreatedAt,
	}
	if e.UserID != nil {
		s := e.UserID.String()
		m.UserID = &s
	}
	return m
}

// ClusterModel — clusters table.
type ClusterModel struct {
	ID                  string         `gorm:"primaryKey;type:text;not null"`
	Name                string         `gorm:"type:text;not null;uniqueIndex:idx_clusters_name"`
	Description         string         `gorm:"type:text"`
	ServerURLs          []string       `gorm:"type:text;not null;serializer:json"`
	OperatorID          string         `gorm:"type:text;not null;index:idx_clusters_operator_id"`
	Operator            *OperatorModel `gorm:"foreignKey:OperatorID;references:ID;constraint:OnDelete:RESTRICT,OnUpdate:NO ACTION"`
	SystemAccountPubKey string         `gorm:"type:text"`
	EncryptedCreds      string         `gorm:"type:text"`
	SkipVerifyTLS       bool           `gorm:"type:boolean;not null;default:false"`
	Healthy             bool           `gorm:"type:boolean;not null;default:false"`
	LastHealthCheck     *time.Time     `gorm:"type:timestamp"`
	HealthCheckError    string         `gorm:"type:text;not null;default:''"`
	CreatedAt           time.Time      `gorm:"type:timestamp;not null;default:CURRENT_TIMESTAMP"`
	UpdatedAt           time.Time      `gorm:"type:timestamp;not null;default:CURRENT_TIMESTAMP"`
}

func (ClusterModel) TableName() string { return "clusters" }

func (m *ClusterModel) ToEntity() *entities.Cluster {
	return &entities.Cluster{
		ID:                  uuid.MustParse(m.ID),
		Name:                m.Name,
		Description:         m.Description,
		ServerURLs:          m.ServerURLs,
		OperatorID:          uuid.MustParse(m.OperatorID),
		SystemAccountPubKey: m.SystemAccountPubKey,
		EncryptedCreds:      m.EncryptedCreds,
		SkipVerifyTLS:       m.SkipVerifyTLS,
		Healthy:             m.Healthy,
		LastHealthCheck:     m.LastHealthCheck,
		HealthCheckError:    m.HealthCheckError,
		CreatedAt:           m.CreatedAt,
		UpdatedAt:           m.UpdatedAt,
	}
}

func ClusterModelFromEntity(e *entities.Cluster) *ClusterModel {
	return &ClusterModel{
		ID:                  e.ID.String(),
		Name:                e.Name,
		Description:         e.Description,
		ServerURLs:          e.ServerURLs,
		OperatorID:          e.OperatorID.String(),
		SystemAccountPubKey: e.SystemAccountPubKey,
		EncryptedCreds:      e.EncryptedCreds,
		SkipVerifyTLS:       e.SkipVerifyTLS,
		Healthy:             e.Healthy,
		LastHealthCheck:     e.LastHealthCheck,
		HealthCheckError:    e.HealthCheckError,
		CreatedAt:           e.CreatedAt,
		UpdatedAt:           e.UpdatedAt,
	}
}

// APIUserModel — api_users table.
//
// OrganizationID is nullable: platform admins (role='admin') are org-less.
// AuthSource is NOT NULL with a default of 'local'; OIDC-sourced users have
// 'oidc'. The CHECK constraint enforces that OIDC rows always carry an
// external_subject and an organization_id. The partial unique index
// (organization_id, external_subject) WHERE auth_source='oidc' is added by
// hand to the migration (atlas-provider-gorm can't emit partial indexes from
// struct tags) and registered in tools/atlas/loader.go manualExtras.
type APIUserModel struct {
	ID           string             `gorm:"primaryKey;type:text;not null"`
	Username     string             `gorm:"type:text;not null;uniqueIndex:idx_api_users_username"`
	PasswordHash string             `gorm:"type:text;not null"`
	Role         string             `gorm:"type:text;not null"`
	OperatorID   *string            `gorm:"type:text;index:idx_api_users_operator_id"`
	Operator     *OperatorModel     `gorm:"foreignKey:OperatorID;references:ID;constraint:OnDelete:CASCADE,OnUpdate:NO ACTION"`
	AccountID    *string            `gorm:"type:text;index:idx_api_users_account_id"`
	Account      *AccountModel      `gorm:"foreignKey:AccountID;references:ID;constraint:OnDelete:CASCADE,OnUpdate:NO ACTION"`
	// Org tenancy + OIDC columns added in 00009_add_organizations.
	OrganizationID  *string            `gorm:"column:organization_id;type:text;index:idx_api_users_org_id;check:chk_api_users_oidc_invariant,auth_source <> 'oidc' OR (external_subject IS NOT NULL AND organization_id IS NOT NULL)"`
	Organization    *OrganizationModel `gorm:"foreignKey:OrganizationID;references:ID;constraint:OnDelete:CASCADE,OnUpdate:NO ACTION"`
	AuthSource      string             `gorm:"column:auth_source;type:text;not null;default:'local'"`
	ExternalSubject *string            `gorm:"column:external_subject;type:text"`
	Email           *string            `gorm:"column:email;type:text"`
	CreatedAt       time.Time          `gorm:"type:timestamp;not null;default:CURRENT_TIMESTAMP"`
	UpdatedAt       time.Time          `gorm:"type:timestamp;not null;default:CURRENT_TIMESTAMP"`
}

func (APIUserModel) TableName() string { return "api_users" }

func (m *APIUserModel) ToEntity() *entities.APIUser {
	var operatorID *uuid.UUID
	if m.OperatorID != nil {
		id := uuid.MustParse(*m.OperatorID)
		operatorID = &id
	}

	var accountID *uuid.UUID
	if m.AccountID != nil {
		id := uuid.MustParse(*m.AccountID)
		accountID = &id
	}

	var organizationID *uuid.UUID
	if m.OrganizationID != nil {
		id := uuid.MustParse(*m.OrganizationID)
		organizationID = &id
	}

	authSource := m.AuthSource
	if authSource == "" {
		authSource = "local"
	}

	return &entities.APIUser{
		ID:              uuid.MustParse(m.ID),
		Username:        m.Username,
		PasswordHash:    m.PasswordHash,
		Role:            entities.APIUserRole(m.Role),
		OperatorID:      operatorID,
		AccountID:       accountID,
		OrganizationID:  organizationID,
		AuthSource:      authSource,
		ExternalSubject: m.ExternalSubject,
		Email:           m.Email,
		CreatedAt:       m.CreatedAt,
		UpdatedAt:       m.UpdatedAt,
	}
}

func APIUserModelFromEntity(e *entities.APIUser) *APIUserModel {
	var operatorID *string
	if e.OperatorID != nil {
		id := e.OperatorID.String()
		operatorID = &id
	}

	var accountID *string
	if e.AccountID != nil {
		id := e.AccountID.String()
		accountID = &id
	}

	var organizationID *string
	if e.OrganizationID != nil {
		id := e.OrganizationID.String()
		organizationID = &id
	}

	authSource := e.AuthSource
	if authSource == "" {
		authSource = "local"
	}

	return &APIUserModel{
		ID:              e.ID.String(),
		Username:        e.Username,
		PasswordHash:    e.PasswordHash,
		Role:            string(e.Role),
		OperatorID:      operatorID,
		AccountID:       accountID,
		OrganizationID:  organizationID,
		AuthSource:      authSource,
		ExternalSubject: e.ExternalSubject,
		Email:           e.Email,
		CreatedAt:       e.CreatedAt,
		UpdatedAt:       e.UpdatedAt,
	}
}

// APITokenModel — api_tokens table. created_by_user_id is ON DELETE SET NULL
// so offboarding a human api_user does not silently disable their CI tokens.
// OrganizationID is nullable: platform-admin tokens are org-less; org-scoped
// service-account tokens carry the org they were minted for.
type APITokenModel struct {
	ID              string             `gorm:"primaryKey;type:text;not null"`
	Name            string             `gorm:"type:text;not null;uniqueIndex:idx_api_tokens_name_per_creator,priority:2"`
	TokenHash       string             `gorm:"type:text;not null;uniqueIndex:idx_api_tokens_token_hash"`
	Prefix          string             `gorm:"type:text;not null"`
	Description     string             `gorm:"type:text;not null;default:''"`
	CreatedByUserID *string            `gorm:"type:text;index:idx_api_tokens_created_by_user_id;uniqueIndex:idx_api_tokens_name_per_creator,priority:1"`
	CreatedByUser   *APIUserModel      `gorm:"foreignKey:CreatedByUserID;references:ID;constraint:OnDelete:SET NULL,OnUpdate:NO ACTION"`
	Role            string             `gorm:"type:text;not null"`
	OperatorID      *string            `gorm:"type:text;index:idx_api_tokens_operator_id"`
	Operator        *OperatorModel     `gorm:"foreignKey:OperatorID;references:ID;constraint:OnDelete:CASCADE,OnUpdate:NO ACTION"`
	AccountID       *string            `gorm:"type:text;index:idx_api_tokens_account_id"`
	Account         *AccountModel      `gorm:"foreignKey:AccountID;references:ID;constraint:OnDelete:CASCADE,OnUpdate:NO ACTION"`
	OrganizationID  *string            `gorm:"column:organization_id;type:text;index:idx_api_tokens_org_id"`
	Organization    *OrganizationModel `gorm:"foreignKey:OrganizationID;references:ID;constraint:OnDelete:CASCADE,OnUpdate:NO ACTION"`
	ExpiresAt       *time.Time         `gorm:"type:timestamp"`
	LastUsedAt      *time.Time         `gorm:"type:timestamp"`
	RevokedAt       *time.Time         `gorm:"type:timestamp;index:idx_api_tokens_revoked_at"`
	CreatedAt       time.Time          `gorm:"type:timestamp;not null;default:CURRENT_TIMESTAMP"`
	UpdatedAt       time.Time          `gorm:"type:timestamp;not null;default:CURRENT_TIMESTAMP"`
}

func (APITokenModel) TableName() string { return "api_tokens" }

func (m *APITokenModel) ToEntity() *entities.APIToken {
	t := &entities.APIToken{
		ID:          uuid.MustParse(m.ID),
		Name:        m.Name,
		TokenHash:   m.TokenHash,
		Prefix:      m.Prefix,
		Description: m.Description,
		Role:        entities.APIUserRole(m.Role),
		ExpiresAt:   m.ExpiresAt,
		LastUsedAt:  m.LastUsedAt,
		RevokedAt:   m.RevokedAt,
		CreatedAt:   m.CreatedAt,
		UpdatedAt:   m.UpdatedAt,
	}
	if m.CreatedByUserID != nil {
		id := uuid.MustParse(*m.CreatedByUserID)
		t.CreatedByUserID = &id
	}
	if m.OperatorID != nil {
		id := uuid.MustParse(*m.OperatorID)
		t.OperatorID = &id
	}
	if m.AccountID != nil {
		id := uuid.MustParse(*m.AccountID)
		t.AccountID = &id
	}
	if m.OrganizationID != nil {
		id := uuid.MustParse(*m.OrganizationID)
		t.OrganizationID = &id
	}
	return t
}

func APITokenModelFromEntity(e *entities.APIToken) *APITokenModel {
	m := &APITokenModel{
		ID:          e.ID.String(),
		Name:        e.Name,
		TokenHash:   e.TokenHash,
		Prefix:      e.Prefix,
		Description: e.Description,
		Role:        string(e.Role),
		ExpiresAt:   e.ExpiresAt,
		LastUsedAt:  e.LastUsedAt,
		RevokedAt:   e.RevokedAt,
		CreatedAt:   e.CreatedAt,
		UpdatedAt:   e.UpdatedAt,
	}
	if e.CreatedByUserID != nil {
		id := e.CreatedByUserID.String()
		m.CreatedByUserID = &id
	}
	if e.OperatorID != nil {
		id := e.OperatorID.String()
		m.OperatorID = &id
	}
	if e.AccountID != nil {
		id := e.AccountID.String()
		m.AccountID = &id
	}
	if e.OrganizationID != nil {
		id := e.OrganizationID.String()
		m.OrganizationID = &id
	}
	return m
}

// EventModel — events table. Deliberately no FK on operator_id / account_id /
// actor_id — these refer to entities that may have been deleted, and the
// append-only audit log must survive that. resource_type+resource_id is the
// pointer to the (possibly-gone) subject.
type EventModel struct {
	ID           string    `gorm:"primaryKey;type:text;not null"`
	OccurredAt   time.Time `gorm:"type:timestamp;not null;default:CURRENT_TIMESTAMP;index:idx_events_occurred_at"`
	Type         string    `gorm:"type:text;not null;index:idx_events_type"`
	ActorType    string    `gorm:"type:text;not null"`
	ActorID      *string   `gorm:"type:text"`
	OperatorID   *string   `gorm:"type:text;index:idx_events_operator_id"`
	AccountID    *string   `gorm:"type:text"`
	ResourceType string    `gorm:"type:text;not null;index:idx_events_resource,priority:1"`
	ResourceID   string    `gorm:"type:text;not null;index:idx_events_resource,priority:2"`
	Payload      *string   `gorm:"type:text"`
	// Diff is the P1 field-level before/after change set produced by an
	// UPDATE mutation. JSON-encoded map[string][2]any keyed by field name.
	// Nullable — CREATE/DELETE events and updates with no audit-visible
	// change leave it NULL. See internal/application/events/diff.go.
	Diff *string `gorm:"type:text"`
}

func (EventModel) TableName() string { return "events" }

func (m *EventModel) ToEntity() *entities.Event {
	e := &entities.Event{
		ID:           uuid.MustParse(m.ID),
		OccurredAt:   m.OccurredAt,
		Type:         m.Type,
		ActorType:    entities.ActorType(m.ActorType),
		ResourceType: m.ResourceType,
		ResourceID:   m.ResourceID,
	}
	if m.ActorID != nil {
		id := uuid.MustParse(*m.ActorID)
		e.ActorID = &id
	}
	if m.OperatorID != nil {
		id := uuid.MustParse(*m.OperatorID)
		e.OperatorID = &id
	}
	if m.AccountID != nil {
		id := uuid.MustParse(*m.AccountID)
		e.AccountID = &id
	}
	if m.Payload != nil {
		e.Payload = json.RawMessage(*m.Payload)
	}
	if m.Diff != nil {
		e.Diff = json.RawMessage(*m.Diff)
	}
	return e
}

func EventModelFromEntity(e *entities.Event) *EventModel {
	m := &EventModel{
		ID:           e.ID.String(),
		OccurredAt:   e.OccurredAt,
		Type:         e.Type,
		ActorType:    string(e.ActorType),
		ResourceType: e.ResourceType,
		ResourceID:   e.ResourceID,
	}
	if e.ActorID != nil {
		id := e.ActorID.String()
		m.ActorID = &id
	}
	if e.OperatorID != nil {
		id := e.OperatorID.String()
		m.OperatorID = &id
	}
	if e.AccountID != nil {
		id := e.AccountID.String()
		m.AccountID = &id
	}
	if len(e.Payload) > 0 {
		s := string(e.Payload)
		m.Payload = &s
	}
	if len(e.Diff) > 0 {
		s := string(e.Diff)
		m.Diff = &s
	}
	return m
}

// WebhookSubscriptionModel — webhook_subscriptions table.
type WebhookSubscriptionModel struct {
	ID              string         `gorm:"primaryKey;type:text;not null"`
	OperatorID      string         `gorm:"type:text;not null;index:idx_webhook_subscriptions_operator_id;uniqueIndex:idx_webhook_subscriptions_operator_name,priority:1"`
	Operator        *OperatorModel `gorm:"foreignKey:OperatorID;references:ID;constraint:OnDelete:CASCADE,OnUpdate:NO ACTION"`
	Name            string         `gorm:"type:text;not null;uniqueIndex:idx_webhook_subscriptions_operator_name,priority:2"`
	Description     string         `gorm:"type:text"`
	URL             string         `gorm:"type:text;not null"`
	EncryptedSecret string         `gorm:"type:text;not null"`
	EventTypes      string         `gorm:"type:text;not null"` // JSON array
	// No `default:true` here: GORM would substitute it for the Go zero value
	// (false), turning an explicit "disabled" write into "enabled" silently.
	// Caught by TestSubscriptionRepo_ListEnabledForEvent_DisabledExcluded.
	Enabled         bool           `gorm:"type:boolean;not null;index:idx_webhook_subscriptions_enabled"`
	DisabledReason  string         `gorm:"type:text;not null;default:''"`
	CreatedAt       time.Time      `gorm:"type:timestamp;not null;default:CURRENT_TIMESTAMP"`
	UpdatedAt       time.Time      `gorm:"type:timestamp;not null;default:CURRENT_TIMESTAMP"`
}

func (WebhookSubscriptionModel) TableName() string { return "webhook_subscriptions" }

func (m *WebhookSubscriptionModel) ToEntity() *entities.WebhookSubscription {
	types, _ := entities.ParseEventTypesJSON(m.EventTypes)
	return &entities.WebhookSubscription{
		ID:              uuid.MustParse(m.ID),
		OperatorID:      uuid.MustParse(m.OperatorID),
		Name:            m.Name,
		Description:     m.Description,
		URL:             m.URL,
		EncryptedSecret: m.EncryptedSecret,
		EventTypes:      types,
		Enabled:         m.Enabled,
		DisabledReason:  m.DisabledReason,
		CreatedAt:       m.CreatedAt,
		UpdatedAt:       m.UpdatedAt,
	}
}

func WebhookSubscriptionModelFromEntity(e *entities.WebhookSubscription) *WebhookSubscriptionModel {
	eventTypes, _ := entities.EventTypesJSON(e.EventTypes)
	return &WebhookSubscriptionModel{
		ID:              e.ID.String(),
		OperatorID:      e.OperatorID.String(),
		Name:            e.Name,
		Description:     e.Description,
		URL:             e.URL,
		EncryptedSecret: e.EncryptedSecret,
		EventTypes:      eventTypes,
		Enabled:         e.Enabled,
		DisabledReason:  e.DisabledReason,
		CreatedAt:       e.CreatedAt,
		UpdatedAt:       e.UpdatedAt,
	}
}

// WebhookDeliveryModel — webhook_deliveries table.
type WebhookDeliveryModel struct {
	ID               string                    `gorm:"primaryKey;type:text;not null"`
	SubscriptionID   string                    `gorm:"type:text;not null;index:idx_webhook_deliveries_subscription_id"`
	Subscription     *WebhookSubscriptionModel `gorm:"foreignKey:SubscriptionID;references:ID;constraint:OnDelete:CASCADE,OnUpdate:NO ACTION"`
	EventID          string                    `gorm:"type:text;not null;index:idx_webhook_deliveries_event_id"`
	Event            *EventModel               `gorm:"foreignKey:EventID;references:ID;constraint:OnDelete:CASCADE,OnUpdate:NO ACTION"`
	Attempt          int                       `gorm:"type:integer;not null;default:0"`
	Status           string                    `gorm:"type:text;not null;index:idx_webhook_deliveries_status_next,priority:1"`
	NextAttemptAt    time.Time                 `gorm:"type:timestamp;not null;index:idx_webhook_deliveries_status_next,priority:2"`
	LastError        string                    `gorm:"type:text;not null;default:''"`
	LastResponseCode int                       `gorm:"type:integer;not null;default:0"`
	CreatedAt        time.Time                 `gorm:"type:timestamp;not null;default:CURRENT_TIMESTAMP"`
	UpdatedAt        time.Time                 `gorm:"type:timestamp;not null;default:CURRENT_TIMESTAMP"`
	CompletedAt      *time.Time                `gorm:"type:timestamp"`
}

func (WebhookDeliveryModel) TableName() string { return "webhook_deliveries" }

func (m *WebhookDeliveryModel) ToEntity() *entities.WebhookDelivery {
	return &entities.WebhookDelivery{
		ID:               uuid.MustParse(m.ID),
		SubscriptionID:   uuid.MustParse(m.SubscriptionID),
		EventID:          uuid.MustParse(m.EventID),
		Attempt:          m.Attempt,
		Status:           entities.DeliveryStatus(m.Status),
		NextAttemptAt:    m.NextAttemptAt,
		LastError:        m.LastError,
		LastResponseCode: m.LastResponseCode,
		CreatedAt:        m.CreatedAt,
		UpdatedAt:        m.UpdatedAt,
		CompletedAt:      m.CompletedAt,
	}
}

func WebhookDeliveryModelFromEntity(e *entities.WebhookDelivery) *WebhookDeliveryModel {
	return &WebhookDeliveryModel{
		ID:               e.ID.String(),
		SubscriptionID:   e.SubscriptionID.String(),
		EventID:          e.EventID.String(),
		Attempt:          e.Attempt,
		Status:           string(e.Status),
		NextAttemptAt:    e.NextAttemptAt,
		LastError:        e.LastError,
		LastResponseCode: e.LastResponseCode,
		CreatedAt:        e.CreatedAt,
		UpdatedAt:        e.UpdatedAt,
		CompletedAt:      e.CompletedAt,
	}
}

// JobModel — jobs table (A2 jobs substrate).
//
// Atlas/atlas-provider-gorm carve-out: the partial unique index
// `(type, dedup_key) WHERE status IN ('pending','running')` cannot be
// expressed as a GORM struct tag (atlas-provider-gorm only emits full
// unique constraints, not partial ones). It is hand-added to the
// migration files for both dialects AND emitted by tools/atlas/loader.go's
// `manualExtras` so the desired-schema dump matches reality — without
// that, every `make atlas-diff` would helpfully emit a DROP for the
// partial index in the next migration and silently break dedup. If you
// hand-edit a migration to add another not-GORM-expressible feature
// (partial index, deferred constraint, expression index, etc.), MUST
// also add it to `manualExtras` keyed by dialect.
type JobModel struct {
	ID           string `gorm:"primaryKey;type:text;not null"`
	Type         string `gorm:"type:text;not null;index:idx_jobs_type_status,priority:1"`
	// Payload is opaque (handler-specific JSON in practice). NOT NULL
	// + default '{}' so empty payloads round-trip without nil-vs-empty
	// confusion at the entity boundary.
	Payload string `gorm:"type:text;not null;default:'{}'"`
	Status  string `gorm:"type:text;not null;index:idx_jobs_status_scheduled,priority:1;index:idx_jobs_type_status,priority:2"`
	// No `default:` on ScheduledFor — entity layer always provides it
	// (zero time would be claimed immediately, surprising for delayed jobs).
	ScheduledFor time.Time  `gorm:"type:timestamp;not null;index:idx_jobs_status_scheduled,priority:2"`
	LockedBy     string     `gorm:"type:text;not null;default:''"`
	LockedUntil  *time.Time `gorm:"type:timestamp"`
	Attempts     int        `gorm:"type:integer;not null;default:0"`
	// No `default:` on MaxAttempts: per the SKILL non-zero-default
	// footgun rule. GORM would substitute the DB default for the Go
	// zero value (0), silently disabling retries. The entity layer
	// always provides MaxAttempts.
	MaxAttempts int    `gorm:"type:integer;not null"`
	LastError   string `gorm:"type:text;not null;default:''"`
	// DedupKey nullable: NULL means "no dedup". The partial unique
	// index lives in the migration (see carve-out above).
	DedupKey    *string    `gorm:"type:text"`
	CreatedAt   time.Time  `gorm:"type:timestamp;not null;default:CURRENT_TIMESTAMP"`
	UpdatedAt   time.Time  `gorm:"type:timestamp;not null;default:CURRENT_TIMESTAMP"`
	StartedAt   *time.Time `gorm:"type:timestamp"`
	CompletedAt *time.Time `gorm:"type:timestamp"`
}

func (JobModel) TableName() string { return "jobs" }

func (m *JobModel) ToEntity() *entities.Job {
	j := &entities.Job{
		ID:           uuid.MustParse(m.ID),
		Type:         m.Type,
		Payload:      []byte(m.Payload),
		Status:       entities.JobStatus(m.Status),
		ScheduledFor: m.ScheduledFor,
		LockedBy:     m.LockedBy,
		LockedUntil:  m.LockedUntil,
		Attempts:     m.Attempts,
		MaxAttempts:  m.MaxAttempts,
		LastError:    m.LastError,
		CreatedAt:    m.CreatedAt,
		UpdatedAt:    m.UpdatedAt,
		StartedAt:    m.StartedAt,
		CompletedAt:  m.CompletedAt,
	}
	if m.DedupKey != nil {
		j.DedupKey = *m.DedupKey
	}
	return j
}

func JobModelFromEntity(e *entities.Job) *JobModel {
	m := &JobModel{
		ID:           e.ID.String(),
		Type:         e.Type,
		Payload:      string(e.Payload),
		Status:       string(e.Status),
		ScheduledFor: e.ScheduledFor,
		LockedBy:     e.LockedBy,
		LockedUntil:  e.LockedUntil,
		Attempts:     e.Attempts,
		MaxAttempts:  e.MaxAttempts,
		LastError:    e.LastError,
		CreatedAt:    e.CreatedAt,
		UpdatedAt:    e.UpdatedAt,
		StartedAt:    e.StartedAt,
		CompletedAt:  e.CompletedAt,
	}
	if e.DedupKey != "" {
		s := e.DedupKey
		m.DedupKey = &s
	}
	if len(e.Payload) == 0 {
		m.Payload = "{}"
	}
	return m
}

// OperatorBackupModel — operator_backups table (P12 scheduled backups).
// One row per uploaded backup artifact. FK CASCADE on operator delete
// drops the rows; the S3 objects are NOT auto-removed (FK CASCADE
// bypasses the service layer, mirroring the A6 "audit-cascade gap"
// caveat). Operators reaching for "delete operator" should expect to
// either delete backups explicitly first or run a manual S3 cleanup.
// Documented as a v1 limitation.
type OperatorBackupModel struct {
	ID          string         `gorm:"primaryKey;type:text;not null"`
	OperatorID  string         `gorm:"type:text;not null;index:idx_operator_backups_operator_id"`
	Operator    *OperatorModel `gorm:"foreignKey:OperatorID;references:ID;constraint:OnDelete:CASCADE,OnUpdate:NO ACTION"`
	ObjectKey   string         `gorm:"type:text;not null;uniqueIndex:idx_operator_backups_object_key"`
	SizeBytes   int64          `gorm:"type:bigint;not null"`
	Sha256      string         `gorm:"type:text;not null"`
	TriggerKind string         `gorm:"column:trigger_kind;type:text;not null"`
	CreatedAt   time.Time      `gorm:"type:timestamp;not null;default:CURRENT_TIMESTAMP"`
}

func (OperatorBackupModel) TableName() string { return "operator_backups" }

func (m *OperatorBackupModel) ToEntity() *entities.OperatorBackup {
	return &entities.OperatorBackup{
		ID:          uuid.MustParse(m.ID),
		OperatorID:  uuid.MustParse(m.OperatorID),
		ObjectKey:   m.ObjectKey,
		SizeBytes:   m.SizeBytes,
		Sha256:      m.Sha256,
		TriggerKind: entities.BackupTriggerKind(m.TriggerKind),
		CreatedAt:   m.CreatedAt,
	}
}

func OperatorBackupModelFromEntity(e *entities.OperatorBackup) *OperatorBackupModel {
	return &OperatorBackupModel{
		ID:          e.ID.String(),
		OperatorID:  e.OperatorID.String(),
		ObjectKey:   e.ObjectKey,
		SizeBytes:   e.SizeBytes,
		Sha256:      e.Sha256,
		TriggerKind: string(e.TriggerKind),
		CreatedAt:   e.CreatedAt,
	}
}

// OperatorAgeRecipientModel — operator_age_recipients table (P15).
// Per-operator age recipient pubkeys. The unique constraint enforces "one
// recipient per (operator, pubkey)" so a re-add surfaces as ErrAlreadyExists.
// FK SET NULL on created_by_user_id mirrors api_tokens: offboarding the user
// who added the key does NOT invalidate the recipient.
//
// PublicKey is stored as the wire form (age1... or ssh-ed25519 ...) — the
// service layer validates via age.ParseRecipient before insert; the column
// is otherwise opaque to the DB.
type OperatorAgeRecipientModel struct {
	ID              string         `gorm:"primaryKey;type:text;not null"`
	OperatorID      string         `gorm:"type:text;not null;uniqueIndex:idx_operator_age_recipients_op_key,priority:1;index:idx_operator_age_recipients_operator_id"`
	Operator        *OperatorModel `gorm:"foreignKey:OperatorID;references:ID;constraint:OnDelete:CASCADE,OnUpdate:NO ACTION"`
	PublicKey       string         `gorm:"column:public_key;type:text;not null;uniqueIndex:idx_operator_age_recipients_op_key,priority:2"`
	Label           string         `gorm:"type:text;not null;default:''"`
	CreatedAt       time.Time      `gorm:"type:timestamp;not null;default:CURRENT_TIMESTAMP"`
	CreatedByUserID *string        `gorm:"column:created_by_user_id;type:text"`
	CreatedBy       *APIUserModel  `gorm:"foreignKey:CreatedByUserID;references:ID;constraint:OnDelete:SET NULL,OnUpdate:NO ACTION"`
}

func (OperatorAgeRecipientModel) TableName() string { return "operator_age_recipients" }

func (m *OperatorAgeRecipientModel) ToEntity() *entities.OperatorAgeRecipient {
	e := &entities.OperatorAgeRecipient{
		ID:         uuid.MustParse(m.ID),
		OperatorID: uuid.MustParse(m.OperatorID),
		PublicKey:  m.PublicKey,
		Label:      m.Label,
		CreatedAt:  m.CreatedAt,
	}
	if m.CreatedByUserID != nil {
		id := uuid.MustParse(*m.CreatedByUserID)
		e.CreatedByUserID = &id
	}
	return e
}

func OperatorAgeRecipientModelFromEntity(e *entities.OperatorAgeRecipient) *OperatorAgeRecipientModel {
	m := &OperatorAgeRecipientModel{
		ID:         e.ID.String(),
		OperatorID: e.OperatorID.String(),
		PublicKey:  e.PublicKey,
		Label:      e.Label,
		CreatedAt:  e.CreatedAt,
	}
	if e.CreatedByUserID != nil {
		s := e.CreatedByUserID.String()
		m.CreatedByUserID = &s
	}
	return m
}
