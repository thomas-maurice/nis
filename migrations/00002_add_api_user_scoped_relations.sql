-- +goose Up

-- Add operator_id and account_id columns to api_users table
ALTER TABLE api_users ADD COLUMN operator_id TEXT;
ALTER TABLE api_users ADD COLUMN account_id TEXT;

-- Add foreign key constraints with CASCADE delete
-- When an operator is deleted, all operator-admin users for that operator are deleted
-- When an account is deleted, all account-admin users for that account are deleted
CREATE INDEX idx_api_users_operator_id ON api_users(operator_id);
CREATE INDEX idx_api_users_account_id ON api_users(account_id);

-- Note: SQLite doesn't support adding foreign keys to existing tables via ALTER TABLE
-- So we need to recreate the table with foreign keys

-- Create new table with foreign keys
CREATE TABLE api_users_new (
    id TEXT PRIMARY KEY,
    username TEXT NOT NULL UNIQUE,
    password_hash TEXT NOT NULL,
    role TEXT NOT NULL,
    operator_id TEXT,
    account_id TEXT,
    created_at TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP,
    updated_at TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP,
    FOREIGN KEY (operator_id) REFERENCES operators(id) ON DELETE CASCADE,
    FOREIGN KEY (account_id) REFERENCES accounts(id) ON DELETE CASCADE
);

-- Copy data from old table
INSERT INTO api_users_new (id, username, password_hash, role, created_at, updated_at)
SELECT id, username, password_hash, role, created_at, updated_at
FROM api_users;

-- Drop old table
DROP TABLE api_users;

-- Rename new table
ALTER TABLE api_users_new RENAME TO api_users;

-- Recreate indexes
CREATE INDEX idx_api_users_operator_id ON api_users(operator_id);
CREATE INDEX idx_api_users_account_id ON api_users(account_id);

-- +goose Down

-- Recreate original table without foreign keys
CREATE TABLE api_users_old (
    id TEXT PRIMARY KEY,
    username TEXT NOT NULL UNIQUE,
    password_hash TEXT NOT NULL,
    role TEXT NOT NULL,
    created_at TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP,
    updated_at TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP
);

-- Copy data back (excluding operator_id and account_id)
INSERT INTO api_users_old (id, username, password_hash, role, created_at, updated_at)
SELECT id, username, password_hash, role, created_at, updated_at
FROM api_users;

-- Drop modified table
DROP TABLE api_users;

-- Rename back
ALTER TABLE api_users_old RENAME TO api_users;
