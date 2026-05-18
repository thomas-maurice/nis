-- +goose Up

-- P2 — JWT lifecycle: per-operator TTL policy, per-user JWT iat/exp metadata,
-- user revocations table (NATS-native AccountClaims.Revocations entries), and
-- the dedup marker for "expiring soon" alerts.
--
-- Default policy is intentionally "no expiry" (0 = never) so this migration is
-- back-compat: existing user JWTs continue to have no `exp` set and keep
-- working exactly as before. Operators opt in by setting user_jwt_ttl_seconds
-- to a positive value.
--
-- SQLite + Postgres compatible: TEXT PKs, plain BIGINT/TIMESTAMP, no SERIAL.

ALTER TABLE operators ADD COLUMN user_jwt_ttl_seconds BIGINT NOT NULL DEFAULT 0;
ALTER TABLE operators ADD COLUMN account_jwt_ttl_seconds BIGINT NOT NULL DEFAULT 0;
ALTER TABLE operators ADD COLUMN jwt_warn_window_seconds BIGINT NOT NULL DEFAULT 1209600;
ALTER TABLE operators ADD COLUMN jwt_auto_renew BOOLEAN NOT NULL DEFAULT FALSE;

ALTER TABLE users ADD COLUMN jwt_ttl_seconds BIGINT;
ALTER TABLE users ADD COLUMN jwt_issued_at TIMESTAMP;
ALTER TABLE users ADD COLUMN jwt_expires_at TIMESTAMP;
ALTER TABLE users ADD COLUMN revoked_at TIMESTAMP;
ALTER TABLE users ADD COLUMN revocation_reason TEXT NOT NULL DEFAULT '';
ALTER TABLE users ADD COLUMN last_expiring_warn_iat TIMESTAMP;
ALTER TABLE users ADD COLUMN last_expired_alert_iat TIMESTAMP;

CREATE INDEX idx_users_jwt_expires_at ON users(jwt_expires_at);
CREATE INDEX idx_users_revoked_at ON users(revoked_at);

-- user_jwt_revocations: one row per active or recently-pruned NATS user JWT
-- revocation. The active rows (pruned_at IS NULL) are flattened into the
-- parent account JWT's Revocations map on every account-JWT regen. Once the
-- revoked JWT's `exp` has elapsed, the sweeper marks the row pruned and
-- re-signs the account JWT without it (NATS rejects on exp anyway, so the
-- entry would be dead weight).
--
-- user_id is nullable + ON DELETE SET NULL so revoking a user and then hard-
-- deleting the user row does not silently drop the revocation: an attacker
-- still holding the .creds must keep being rejected until exp.
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
CREATE INDEX idx_user_jwt_revocations_account_id ON user_jwt_revocations(account_id);
CREATE INDEX idx_user_jwt_revocations_pruned_at ON user_jwt_revocations(pruned_at);
CREATE INDEX idx_user_jwt_revocations_jwt_exp ON user_jwt_revocations(jwt_exp);

-- +goose Down

DROP INDEX IF EXISTS idx_user_jwt_revocations_jwt_exp;
DROP INDEX IF EXISTS idx_user_jwt_revocations_pruned_at;
DROP INDEX IF EXISTS idx_user_jwt_revocations_account_id;
DROP TABLE IF EXISTS user_jwt_revocations;

DROP INDEX IF EXISTS idx_users_revoked_at;
DROP INDEX IF EXISTS idx_users_jwt_expires_at;

ALTER TABLE users DROP COLUMN last_expired_alert_iat;
ALTER TABLE users DROP COLUMN last_expiring_warn_iat;
ALTER TABLE users DROP COLUMN revocation_reason;
ALTER TABLE users DROP COLUMN revoked_at;
ALTER TABLE users DROP COLUMN jwt_expires_at;
ALTER TABLE users DROP COLUMN jwt_issued_at;
ALTER TABLE users DROP COLUMN jwt_ttl_seconds;

ALTER TABLE operators DROP COLUMN jwt_auto_renew;
ALTER TABLE operators DROP COLUMN jwt_warn_window_seconds;
ALTER TABLE operators DROP COLUMN account_jwt_ttl_seconds;
ALTER TABLE operators DROP COLUMN user_jwt_ttl_seconds;
