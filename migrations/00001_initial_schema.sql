-- +goose Up

-- Operators table
CREATE TABLE operators (
    id TEXT PRIMARY KEY,
    name TEXT NOT NULL UNIQUE,
    description TEXT,
    encrypted_seed TEXT NOT NULL,
    public_key TEXT NOT NULL UNIQUE,
    jwt TEXT NOT NULL,
    system_account_pub_key TEXT,
    created_at TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP,
    updated_at TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP
);

-- Accounts table
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

-- Scoped signing keys table (role column removed)
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

-- Users table
CREATE TABLE users (
    id TEXT PRIMARY KEY,
    account_id TEXT NOT NULL,
    name TEXT NOT NULL,
    description TEXT,
    encrypted_seed TEXT NOT NULL,
    public_key TEXT NOT NULL UNIQUE,
    jwt TEXT NOT NULL,
    scoped_signing_key_id TEXT,
    created_at TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP,
    updated_at TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP,
    FOREIGN KEY (account_id) REFERENCES accounts(id) ON DELETE CASCADE,
    FOREIGN KEY (scoped_signing_key_id) REFERENCES scoped_signing_keys(id) ON DELETE SET NULL,
    UNIQUE(account_id, name)
);

-- Clusters table (with health fields)
CREATE TABLE clusters (
    id TEXT PRIMARY KEY,
    name TEXT NOT NULL UNIQUE,
    description TEXT,
    server_urls TEXT NOT NULL,  -- JSON array
    operator_id TEXT NOT NULL,
    system_account_pub_key TEXT,
    encrypted_creds TEXT,
    healthy BOOLEAN NOT NULL DEFAULT false,
    last_health_check DATETIME,
    health_check_error TEXT NOT NULL DEFAULT '',
    created_at TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP,
    updated_at TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP,
    FOREIGN KEY (operator_id) REFERENCES operators(id) ON DELETE RESTRICT
);

-- API users table (for API authentication)
CREATE TABLE api_users (
    id TEXT PRIMARY KEY,
    username TEXT NOT NULL UNIQUE,
    password_hash TEXT NOT NULL,
    role TEXT NOT NULL,  -- admin, operator-admin, account-admin
    created_at TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP,
    updated_at TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP
);

-- Indexes
CREATE INDEX idx_accounts_operator_id ON accounts(operator_id);
CREATE INDEX idx_users_account_id ON users(account_id);
CREATE INDEX idx_users_scoped_signing_key_id ON users(scoped_signing_key_id);
CREATE INDEX idx_scoped_signing_keys_account_id ON scoped_signing_keys(account_id);
CREATE INDEX idx_clusters_operator_id ON clusters(operator_id);

-- +goose Down

DROP TABLE IF EXISTS clusters;
DROP TABLE IF EXISTS users;
DROP TABLE IF EXISTS scoped_signing_keys;
DROP TABLE IF EXISTS accounts;
DROP TABLE IF EXISTS operators;
DROP TABLE IF EXISTS api_users;
