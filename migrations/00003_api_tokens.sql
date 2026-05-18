-- +goose Up

-- api_tokens: long-lived opaque service-account tokens for automation (CI, nisctl).
-- Distinct from api_users (bcrypt'd password login) and from per-request JWT sessions.
-- Token plaintext is shown ONCE on create; only sha256(token) is stored.
CREATE TABLE api_tokens (
    id TEXT PRIMARY KEY,
    name TEXT NOT NULL,
    token_hash TEXT NOT NULL UNIQUE,    -- hex(sha256(plaintext))
    prefix TEXT NOT NULL,                -- "nis_pat_xxxxxxxx" — first 8 chars of the random part for display
    description TEXT NOT NULL DEFAULT '',
    -- created_by_user_id is nullable + ON DELETE SET NULL so deleting an offboarded
    -- human api_user does not silently disable their CI tokens. Operators can audit
    -- orphaned tokens and revoke explicitly.
    created_by_user_id TEXT,
    role TEXT NOT NULL,                  -- admin / operator-admin / account-admin
    operator_id TEXT,                    -- nullable; required when role=operator-admin
    account_id TEXT,                     -- nullable; required when role=account-admin
    expires_at TIMESTAMP,                -- nullable; NULL = never expires
    last_used_at TIMESTAMP,              -- nullable; updated by coalescing flusher
    revoked_at TIMESTAMP,                -- nullable; non-null = revoked, no longer accepted
    created_at TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP,
    updated_at TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP,
    FOREIGN KEY (created_by_user_id) REFERENCES api_users(id) ON DELETE SET NULL,
    FOREIGN KEY (operator_id) REFERENCES operators(id) ON DELETE CASCADE,
    FOREIGN KEY (account_id) REFERENCES accounts(id) ON DELETE CASCADE
);
CREATE INDEX idx_api_tokens_token_hash ON api_tokens(token_hash);
CREATE INDEX idx_api_tokens_created_by_user_id ON api_tokens(created_by_user_id);
CREATE INDEX idx_api_tokens_operator_id ON api_tokens(operator_id);
CREATE INDEX idx_api_tokens_account_id ON api_tokens(account_id);
CREATE INDEX idx_api_tokens_revoked_at ON api_tokens(revoked_at);
CREATE UNIQUE INDEX idx_api_tokens_name_per_creator ON api_tokens(created_by_user_id, name);

-- +goose Down

DROP TABLE IF EXISTS api_tokens;
