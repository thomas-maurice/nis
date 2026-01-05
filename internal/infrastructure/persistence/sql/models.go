package sql

import (
	"time"

	"github.com/google/uuid"
	"github.com/thomas-maurice/nis/internal/domain/entities"
)

// OperatorModel represents the GORM model for operators
type OperatorModel struct {
	ID                  string `gorm:"primaryKey;type:text"`
	Name                string `gorm:"type:text;uniqueIndex;not null"`
	Description         string `gorm:"type:text"`
	EncryptedSeed       string `gorm:"type:text;not null"`
	PublicKey           string `gorm:"type:text;uniqueIndex;not null"`
	JWT                 string `gorm:"type:text;not null"`
	SystemAccountPubKey string `gorm:"type:text"`
	CreatedAt           time.Time
	UpdatedAt           time.Time
}

func (OperatorModel) TableName() string {
	return "operators"
}

// ToEntity converts GORM model to domain entity
func (m *OperatorModel) ToEntity() *entities.Operator {
	return &entities.Operator{
		ID:                  uuid.MustParse(m.ID),
		Name:                m.Name,
		Description:         m.Description,
		EncryptedSeed:       m.EncryptedSeed,
		PublicKey:           m.PublicKey,
		JWT:                 m.JWT,
		SystemAccountPubKey: m.SystemAccountPubKey,
		CreatedAt:           m.CreatedAt,
		UpdatedAt:           m.UpdatedAt,
	}
}

// FromEntity converts domain entity to GORM model
func OperatorModelFromEntity(e *entities.Operator) *OperatorModel {
	return &OperatorModel{
		ID:                  e.ID.String(),
		Name:                e.Name,
		Description:         e.Description,
		EncryptedSeed:       e.EncryptedSeed,
		PublicKey:           e.PublicKey,
		JWT:                 e.JWT,
		SystemAccountPubKey: e.SystemAccountPubKey,
		CreatedAt:           e.CreatedAt,
		UpdatedAt:           e.UpdatedAt,
	}
}

// AccountModel represents the GORM model for accounts
type AccountModel struct {
	ID                    string `gorm:"primaryKey;type:text"`
	OperatorID            string `gorm:"type:text;not null;index:idx_accounts_operator_id"`
	Name                  string `gorm:"type:text;not null"`
	Description           string `gorm:"type:text"`
	EncryptedSeed         string `gorm:"type:text;not null"`
	PublicKey             string `gorm:"type:text;uniqueIndex;not null"`
	JWT                   string `gorm:"type:text;not null"`
	JetStreamEnabled      bool   `gorm:"column:jetstream_enabled;not null;default:false"`
	JetStreamMaxMemory    int64  `gorm:"column:jetstream_max_memory;not null;default:-1"`
	JetStreamMaxStorage   int64  `gorm:"column:jetstream_max_storage;not null;default:-1"`
	JetStreamMaxStreams   int64  `gorm:"column:jetstream_max_streams;not null;default:-1"`
	JetStreamMaxConsumers int64  `gorm:"column:jetstream_max_consumers;not null;default:-1"`
	CreatedAt             time.Time
	UpdatedAt             time.Time
}

func (AccountModel) TableName() string {
	return "accounts"
}

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

// ScopedSigningKeyModel represents the GORM model for scoped signing keys
type ScopedSigningKeyModel struct {
	ID               string   `gorm:"primaryKey;type:text"`
	AccountID        string   `gorm:"type:text;not null;index:idx_scoped_signing_keys_account_id"`
	Name             string   `gorm:"type:text;not null"`
	Description      string   `gorm:"type:text"`
	EncryptedSeed    string   `gorm:"type:text;not null"`
	PublicKey        string   `gorm:"type:text;uniqueIndex;not null"`
	PubAllow         []string `gorm:"type:text;serializer:json"`
	PubDeny          []string `gorm:"type:text;serializer:json"`
	SubAllow         []string `gorm:"type:text;serializer:json"`
	SubDeny          []string `gorm:"type:text;serializer:json"`
	ResponseMaxMsgs  int      `gorm:"not null;default:0"`
	ResponseTTLSecs  int64    `gorm:"column:response_ttl_seconds;not null;default:0"`
	CreatedAt        time.Time
	UpdatedAt        time.Time
}

func (ScopedSigningKeyModel) TableName() string {
	return "scoped_signing_keys"
}

func (m *ScopedSigningKeyModel) ToEntity() *entities.ScopedSigningKey {
	return &entities.ScopedSigningKey{
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
		CreatedAt:       m.CreatedAt,
		UpdatedAt:       m.UpdatedAt,
	}
}

func ScopedSigningKeyModelFromEntity(e *entities.ScopedSigningKey) *ScopedSigningKeyModel {
	return &ScopedSigningKeyModel{
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
		CreatedAt:       e.CreatedAt,
		UpdatedAt:       e.UpdatedAt,
	}
}

// UserModel represents the GORM model for users
type UserModel struct {
	ID                  string  `gorm:"primaryKey;type:text"`
	AccountID           string  `gorm:"type:text;not null;index:idx_users_account_id"`
	Name                string  `gorm:"type:text;not null"`
	Description         string  `gorm:"type:text"`
	EncryptedSeed       string  `gorm:"type:text;not null"`
	PublicKey           string  `gorm:"type:text;uniqueIndex;not null"`
	JWT                 string  `gorm:"type:text;not null"`
	ScopedSigningKeyID  *string `gorm:"type:text;index:idx_users_scoped_signing_key_id"`
	CreatedAt           time.Time
	UpdatedAt           time.Time
}

func (UserModel) TableName() string {
	return "users"
}

func (m *UserModel) ToEntity() *entities.User {
	var scopedKeyID *uuid.UUID
	if m.ScopedSigningKeyID != nil && *m.ScopedSigningKeyID != "" {
		id := uuid.MustParse(*m.ScopedSigningKeyID)
		scopedKeyID = &id
	}

	return &entities.User{
		ID:                 uuid.MustParse(m.ID),
		AccountID:          uuid.MustParse(m.AccountID),
		Name:               m.Name,
		Description:        m.Description,
		EncryptedSeed:      m.EncryptedSeed,
		PublicKey:          m.PublicKey,
		JWT:                m.JWT,
		ScopedSigningKeyID: scopedKeyID,
		CreatedAt:          m.CreatedAt,
		UpdatedAt:          m.UpdatedAt,
	}
}

func UserModelFromEntity(e *entities.User) *UserModel {
	var scopedKeyID *string
	if e.ScopedSigningKeyID != nil {
		s := e.ScopedSigningKeyID.String()
		scopedKeyID = &s
	}

	return &UserModel{
		ID:                 e.ID.String(),
		AccountID:          e.AccountID.String(),
		Name:               e.Name,
		Description:        e.Description,
		EncryptedSeed:      e.EncryptedSeed,
		PublicKey:          e.PublicKey,
		JWT:                e.JWT,
		ScopedSigningKeyID: scopedKeyID,
		CreatedAt:          e.CreatedAt,
		UpdatedAt:          e.UpdatedAt,
	}
}

// ClusterModel represents the GORM model for clusters
type ClusterModel struct {
	ID                  string   `gorm:"primaryKey;type:text"`
	Name                string   `gorm:"type:text;uniqueIndex;not null"`
	Description         string   `gorm:"type:text"`
	ServerURLs          []string `gorm:"type:text;not null;serializer:json"`
	OperatorID          string   `gorm:"type:text;not null;index:idx_clusters_operator_id"`
	SystemAccountPubKey string   `gorm:"type:text"`
	EncryptedCreds      string   `gorm:"type:text"`
	Healthy             bool     `gorm:"type:boolean;not null;default:false"`
	LastHealthCheck     *time.Time `gorm:"type:datetime"`
	HealthCheckError    string   `gorm:"type:text;not null;default:''"`
	CreatedAt           time.Time
	UpdatedAt           time.Time
}

func (ClusterModel) TableName() string {
	return "clusters"
}

func (m *ClusterModel) ToEntity() *entities.Cluster {
	return &entities.Cluster{
		ID:                  uuid.MustParse(m.ID),
		Name:                m.Name,
		Description:         m.Description,
		ServerURLs:          m.ServerURLs,
		OperatorID:          uuid.MustParse(m.OperatorID),
		SystemAccountPubKey: m.SystemAccountPubKey,
		EncryptedCreds:      m.EncryptedCreds,
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
		Healthy:             e.Healthy,
		LastHealthCheck:     e.LastHealthCheck,
		HealthCheckError:    e.HealthCheckError,
		CreatedAt:           e.CreatedAt,
		UpdatedAt:           e.UpdatedAt,
	}
}

// APIUserModel represents the GORM model for API users
type APIUserModel struct {
	ID           string  `gorm:"primaryKey;type:text"`
	Username     string  `gorm:"type:text;uniqueIndex;not null"`
	PasswordHash string  `gorm:"type:text;not null"`
	Role         string  `gorm:"type:text;not null"`
	OperatorID   *string `gorm:"type:text;index"`
	AccountID    *string `gorm:"type:text;index"`
	CreatedAt    time.Time
	UpdatedAt    time.Time
}

func (APIUserModel) TableName() string {
	return "api_users"
}

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

	return &entities.APIUser{
		ID:           uuid.MustParse(m.ID),
		Username:     m.Username,
		PasswordHash: m.PasswordHash,
		Role:         entities.APIUserRole(m.Role),
		OperatorID:   operatorID,
		AccountID:    accountID,
		CreatedAt:    m.CreatedAt,
		UpdatedAt:    m.UpdatedAt,
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

	return &APIUserModel{
		ID:           e.ID.String(),
		Username:     e.Username,
		PasswordHash: e.PasswordHash,
		Role:         string(e.Role),
		OperatorID:   operatorID,
		AccountID:    accountID,
		CreatedAt:    e.CreatedAt,
		UpdatedAt:    e.UpdatedAt,
	}
}

