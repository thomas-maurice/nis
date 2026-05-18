package sql

import (
	"encoding/json"
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
	SkipVerifyTLS       bool     `gorm:"type:boolean;not null;default:false"`
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

// APITokenModel represents the GORM model for service-account API tokens.
type APITokenModel struct {
	ID              string     `gorm:"primaryKey;type:text"`
	Name            string     `gorm:"type:text;not null"`
	TokenHash       string     `gorm:"type:text;not null;uniqueIndex"`
	Prefix          string     `gorm:"type:text;not null"`
	Description     string     `gorm:"type:text;not null;default:''"`
	CreatedByUserID *string    `gorm:"type:text;index"`
	Role            string     `gorm:"type:text;not null"`
	OperatorID      *string    `gorm:"type:text;index"`
	AccountID       *string    `gorm:"type:text;index"`
	ExpiresAt       *time.Time `gorm:"type:timestamp"`
	LastUsedAt      *time.Time `gorm:"type:timestamp"`
	RevokedAt       *time.Time `gorm:"type:timestamp;index"`
	CreatedAt       time.Time
	UpdatedAt       time.Time
}

func (APITokenModel) TableName() string {
	return "api_tokens"
}

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
	return m
}

// EventModel represents the GORM model for events
type EventModel struct {
	ID           string  `gorm:"primaryKey;type:text"`
	OccurredAt   time.Time `gorm:"type:timestamp;not null;index"`
	Type         string  `gorm:"type:text;not null;index"`
	ActorType    string  `gorm:"type:text;not null"`
	ActorID      *string `gorm:"type:text"`
	OperatorID   *string `gorm:"type:text;index"`
	AccountID    *string `gorm:"type:text"`
	ResourceType string  `gorm:"type:text;not null"`
	ResourceID   string  `gorm:"type:text;not null"`
	Payload      *string `gorm:"type:text"`
}

func (EventModel) TableName() string {
	return "events"
}

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
	return m
}

// WebhookSubscriptionModel represents the GORM model for webhook_subscriptions
type WebhookSubscriptionModel struct {
	ID              string `gorm:"primaryKey;type:text"`
	OperatorID      string `gorm:"type:text;not null;index"`
	Name            string `gorm:"type:text;not null"`
	Description     string `gorm:"type:text"`
	URL             string `gorm:"type:text;not null"`
	EncryptedSecret string `gorm:"type:text;not null"`
	EventTypes      string `gorm:"type:text;not null"` // JSON array
	Enabled         bool   `gorm:"not null;index"`
	DisabledReason  string `gorm:"type:text;not null;default:''"`
	CreatedAt       time.Time
	UpdatedAt       time.Time
}

func (WebhookSubscriptionModel) TableName() string {
	return "webhook_subscriptions"
}

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

// WebhookDeliveryModel represents the GORM model for webhook_deliveries
type WebhookDeliveryModel struct {
	ID               string     `gorm:"primaryKey;type:text"`
	SubscriptionID   string     `gorm:"type:text;not null;index"`
	EventID          string     `gorm:"type:text;not null;index"`
	Attempt          int        `gorm:"not null;default:0"`
	Status           string     `gorm:"type:text;not null"`
	NextAttemptAt    time.Time  `gorm:"type:timestamp;not null"`
	LastError        string     `gorm:"type:text;not null;default:''"`
	LastResponseCode int        `gorm:"not null;default:0"`
	CreatedAt        time.Time
	UpdatedAt        time.Time
	CompletedAt      *time.Time `gorm:"type:timestamp"`
}

func (WebhookDeliveryModel) TableName() string {
	return "webhook_deliveries"
}

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

