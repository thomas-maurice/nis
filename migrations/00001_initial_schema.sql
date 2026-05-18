-- +goose Up

-- Consolidated initial schema. Pre-release: no released databases exist so
-- prior numbered migrations were collapsed into this single file by goose
-- create + hand-written SQL. Column type is TIMESTAMP (not TIMESTAMPTZ):
-- mattn/go-sqlite3 only auto-parses TIMESTAMP/DATETIME column types into
-- time.Time; TIMESTAMPTZ falls through as a string. The discipline that
-- keeps timestamps correct lives at the Go layer:
--   * internal/clock.Now() returns UTC — use it instead of time.Now().
--   * GORM Config.NowFunc = clock.Now covers any auto-fill path.
--   * SQLite DSN sets _loc=UTC so the driver binds/reads as UTC.
--   * Repo-level normalizeXxxTimes helpers (user, user_jwt_revocation,
--     cluster) coerce externally-sourced time.Time to UTC before write as
--     belt-and-suspenders.
-- With every write going through clock.Now()-equivalent code, TIMESTAMP +
-- _loc=UTC + UTC-default Postgres round-trips correctly on both drivers.

-- Operators
CREATE TABLE operators (
    id TEXT PRIMARY KEY,
    name TEXT NOT NULL UNIQUE,
    description TEXT,
    encrypted_seed TEXT NOT NULL,
    public_key TEXT NOT NULL UNIQUE,
    jwt TEXT NOT NULL,
    system_account_pub_key TEXT,
    user_jwt_ttl_seconds BIGINT NOT NULL DEFAULT 0,
    account_jwt_ttl_seconds BIGINT NOT NULL DEFAULT 0,
    jwt_warn_window_seconds BIGINT NOT NULL DEFAULT 1209600,
    jwt_auto_renew BOOLEAN NOT NULL DEFAULT FALSE,
    created_at TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP,
    updated_at TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP
);

-- Accounts
CREATE TABLE accounts (
    id TEXT PRIMARY KEY,
    operator_id TEXT NOT NULL,
    name TEXT NOT NULL,
    description TEXT,
    encrypted_seed TEXT NOT NULL,
    public_key TEXT NOT NULL UNIQUE,
    jwt TEXT NOT NULL,
    jetstream_enabled BOOLEAN NOT NULL DEFAULT FALSE,
    jetstream_max_memory BIGINT NOT NULL DEFAULT -1,
    jetstream_max_storage BIGINT NOT NULL DEFAULT -1,
    jetstream_max_streams BIGINT NOT NULL DEFAULT -1,
    jetstream_max_consumers BIGINT NOT NULL DEFAULT -1,
    created_at TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP,
    updated_at TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP,
    FOREIGN KEY (operator_id) REFERENCES operators(id) ON DELETE CASCADE,
    UNIQUE(operator_id, name)
);

-- Scoped signing keys
CREATE TABLE scoped_signing_keys (
    id TEXT PRIMARY KEY,
    account_id TEXT NOT NULL,
    name TEXT NOT NULL,
    description TEXT,
    encrypted_seed TEXT NOT NULL,
    public_key TEXT NOT NULL UNIQUE,
    pub_allow TEXT,  -- JSON array
    pub_deny TEXT,   -- JSON array
    sub_allow TEXT,  -- JSON array
    sub_deny TEXT,   -- JSON array
    response_max_msgs INTEGER NOT NULL DEFAULT 0,
    response_ttl_seconds BIGINT NOT NULL DEFAULT 0,
    created_at TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP,
    updated_at TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP,
    FOREIGN KEY (account_id) REFERENCES accounts(id) ON DELETE CASCADE,
    UNIQUE(account_id, name)
);

-- Users
CREATE TABLE users (
    id TEXT PRIMARY KEY,
    account_id TEXT NOT NULL,
    name TEXT NOT NULL,
    description TEXT,
    encrypted_seed TEXT NOT NULL,
    public_key TEXT NOT NULL UNIQUE,
    jwt TEXT NOT NULL,
    scoped_signing_key_id TEXT,
    jwt_ttl_seconds BIGINT,
    jwt_issued_at TIMESTAMP,
    jwt_expires_at TIMESTAMP,
    revoked_at TIMESTAMP,
    revocation_reason TEXT NOT NULL DEFAULT '',
    last_expiring_warn_iat TIMESTAMP,
    last_expired_alert_iat TIMESTAMP,
    created_at TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP,
    updated_at TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP,
    FOREIGN KEY (account_id) REFERENCES accounts(id) ON DELETE CASCADE,
    FOREIGN KEY (scoped_signing_key_id) REFERENCES scoped_signing_keys(id) ON DELETE SET NULL,
    UNIQUE(account_id, name)
);

-- Clusters
CREATE TABLE clusters (
    id TEXT PRIMARY KEY,
    name TEXT NOT NULL UNIQUE,
    description TEXT,
    server_urls TEXT NOT NULL,  -- JSON array
    operator_id TEXT NOT NULL,
    system_account_pub_key TEXT,
    encrypted_creds TEXT,
    skip_verify_tls BOOLEAN NOT NULL DEFAULT FALSE,
    healthy BOOLEAN NOT NULL DEFAULT FALSE,
    last_health_check TIMESTAMP,
    health_check_error TEXT NOT NULL DEFAULT '',
    created_at TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP,
    updated_at TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP,
    FOREIGN KEY (operator_id) REFERENCES operators(id) ON DELETE RESTRICT
);

-- API users (password-based admin/operator-admin/account-admin login)
CREATE TABLE api_users (
    id TEXT PRIMARY KEY,
    username TEXT NOT NULL UNIQUE,
    password_hash TEXT NOT NULL,
    role TEXT NOT NULL,  -- admin, operator-admin, account-admin
    operator_id TEXT,
    account_id TEXT,
    created_at TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP,
    updated_at TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP,
    FOREIGN KEY (operator_id) REFERENCES operators(id) ON DELETE CASCADE,
    FOREIGN KEY (account_id) REFERENCES accounts(id) ON DELETE CASCADE
);

-- API tokens (long-lived opaque service-account bearer credentials)
CREATE TABLE api_tokens (
    id TEXT PRIMARY KEY,
    name TEXT NOT NULL,
    token_hash TEXT NOT NULL UNIQUE,
    prefix TEXT NOT NULL,
    description TEXT NOT NULL DEFAULT '',
    created_by_user_id TEXT,
    role TEXT NOT NULL,
    operator_id TEXT,
    account_id TEXT,
    expires_at TIMESTAMP,
    last_used_at TIMESTAMP,
    revoked_at TIMESTAMP,
    created_at TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP,
    updated_at TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP,
    FOREIGN KEY (created_by_user_id) REFERENCES api_users(id) ON DELETE SET NULL,
    FOREIGN KEY (operator_id) REFERENCES operators(id) ON DELETE CASCADE,
    FOREIGN KEY (account_id) REFERENCES accounts(id) ON DELETE CASCADE
);

-- Events (append-only audit log + source of truth for webhook fanout)
CREATE TABLE events (
    id TEXT PRIMARY KEY,
    occurred_at TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP,
    type TEXT NOT NULL,
    actor_type TEXT NOT NULL,
    actor_id TEXT,
    operator_id TEXT,
    account_id TEXT,
    resource_type TEXT NOT NULL,
    resource_id TEXT NOT NULL,
    payload TEXT
);

-- Webhook subscriptions (operator-scoped HTTP receivers)
CREATE TABLE webhook_subscriptions (
    id TEXT PRIMARY KEY,
    operator_id TEXT NOT NULL,
    name TEXT NOT NULL,
    description TEXT,
    url TEXT NOT NULL,
    encrypted_secret TEXT NOT NULL,
    event_types TEXT NOT NULL,
    enabled BOOLEAN NOT NULL DEFAULT TRUE,
    disabled_reason TEXT NOT NULL DEFAULT '',
    created_at TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP,
    updated_at TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP,
    FOREIGN KEY (operator_id) REFERENCES operators(id) ON DELETE CASCADE,
    UNIQUE(operator_id, name)
);

-- Webhook deliveries (durable retry queue)
CREATE TABLE webhook_deliveries (
    id TEXT PRIMARY KEY,
    subscription_id TEXT NOT NULL,
    event_id TEXT NOT NULL,
    attempt INTEGER NOT NULL DEFAULT 0,
    status TEXT NOT NULL,
    next_attempt_at TIMESTAMP NOT NULL,
    last_error TEXT NOT NULL DEFAULT '',
    last_response_code INTEGER NOT NULL DEFAULT 0,
    created_at TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP,
    updated_at TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP,
    completed_at TIMESTAMP,
    FOREIGN KEY (subscription_id) REFERENCES webhook_subscriptions(id) ON DELETE CASCADE,
    FOREIGN KEY (event_id) REFERENCES events(id) ON DELETE CASCADE
);

-- User JWT revocations (flattened into account JWT Revocations map on regen)
CREATE TABLE user_jwt_revocations (
    id TEXT PRIMARY KEY,
    account_id TEXT NOT NULL,
    user_id TEXT,
    user_public_key TEXT NOT NULL,
    revoked_at TIMESTAMP NOT NULL,
    jwt_exp TIMESTAMP NOT NULL,
    reason TEXT NOT NULL DEFAULT '',
    pruned_at TIMESTAMP,
    created_at TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP,
    FOREIGN KEY (account_id) REFERENCES accounts(id) ON DELETE CASCADE,
    FOREIGN KEY (user_id) REFERENCES users(id) ON DELETE SET NULL
);

-- Indexes
CREATE INDEX idx_accounts_operator_id ON accounts(operator_id);
CREATE INDEX idx_users_account_id ON users(account_id);
CREATE INDEX idx_users_scoped_signing_key_id ON users(scoped_signing_key_id);
CREATE INDEX idx_users_jwt_expires_at ON users(jwt_expires_at);
CREATE INDEX idx_users_revoked_at ON users(revoked_at);
CREATE INDEX idx_scoped_signing_keys_account_id ON scoped_signing_keys(account_id);
CREATE INDEX idx_clusters_operator_id ON clusters(operator_id);
CREATE INDEX idx_api_users_operator_id ON api_users(operator_id);
CREATE INDEX idx_api_users_account_id ON api_users(account_id);
CREATE INDEX idx_api_tokens_token_hash ON api_tokens(token_hash);
CREATE INDEX idx_api_tokens_created_by_user_id ON api_tokens(created_by_user_id);
CREATE INDEX idx_api_tokens_operator_id ON api_tokens(operator_id);
CREATE INDEX idx_api_tokens_account_id ON api_tokens(account_id);
CREATE INDEX idx_api_tokens_revoked_at ON api_tokens(revoked_at);
CREATE UNIQUE INDEX idx_api_tokens_name_per_creator ON api_tokens(created_by_user_id, name);
CREATE INDEX idx_events_occurred_at ON events(occurred_at);
CREATE INDEX idx_events_type ON events(type);
CREATE INDEX idx_events_operator_id ON events(operator_id);
CREATE INDEX idx_events_resource ON events(resource_type, resource_id);
CREATE INDEX idx_webhook_subscriptions_operator_id ON webhook_subscriptions(operator_id);
CREATE INDEX idx_webhook_subscriptions_enabled ON webhook_subscriptions(enabled);
CREATE INDEX idx_webhook_deliveries_status_next ON webhook_deliveries(status, next_attempt_at);
CREATE INDEX idx_webhook_deliveries_subscription_id ON webhook_deliveries(subscription_id);
CREATE INDEX idx_webhook_deliveries_event_id ON webhook_deliveries(event_id);
CREATE INDEX idx_user_jwt_revocations_account_id ON user_jwt_revocations(account_id);
CREATE INDEX idx_user_jwt_revocations_pruned_at ON user_jwt_revocations(pruned_at);
CREATE INDEX idx_user_jwt_revocations_jwt_exp ON user_jwt_revocations(jwt_exp);

-- +goose Down

DROP TABLE IF EXISTS user_jwt_revocations;
DROP TABLE IF EXISTS webhook_deliveries;
DROP TABLE IF EXISTS webhook_subscriptions;
DROP TABLE IF EXISTS events;
DROP TABLE IF EXISTS api_tokens;
DROP TABLE IF EXISTS api_users;
DROP TABLE IF EXISTS clusters;
DROP TABLE IF EXISTS users;
DROP TABLE IF EXISTS scoped_signing_keys;
DROP TABLE IF EXISTS accounts;
DROP TABLE IF EXISTS operators;
