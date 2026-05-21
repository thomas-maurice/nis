-- +goose Up
-- disable the enforcement of foreign-keys constraints
PRAGMA foreign_keys = off;
-- create "new_operators" table
CREATE TABLE `new_operators` (`id` text NOT NULL, `name` text NOT NULL, `description` text NULL, `encrypted_seed` text NOT NULL, `public_key` text NOT NULL, `jwt` text NOT NULL, `system_account_pub_key` text NULL, `user_jwt_ttl_seconds` bigint NOT NULL DEFAULT 0, `account_jwt_ttl_seconds` bigint NOT NULL DEFAULT 0, `jwt_warn_window_seconds` bigint NOT NULL, `jwt_auto_renew` boolean NOT NULL DEFAULT false, `backup_enabled` boolean NOT NULL DEFAULT false, `backup_interval_seconds` bigint NULL, `backup_retention_count` integer NULL, `last_backup_at` timestamp NULL, `created_at` timestamp NOT NULL DEFAULT (CURRENT_TIMESTAMP), `updated_at` timestamp NOT NULL DEFAULT (CURRENT_TIMESTAMP), PRIMARY KEY (`id`), CONSTRAINT `chk_operators_backup_interval_seconds` CHECK (backup_interval_seconds IS NULL OR backup_interval_seconds >= 3600));
-- copy rows from old table "operators" to new temporary table "new_operators"
INSERT INTO `new_operators` (`id`, `name`, `description`, `encrypted_seed`, `public_key`, `jwt`, `system_account_pub_key`, `user_jwt_ttl_seconds`, `account_jwt_ttl_seconds`, `jwt_warn_window_seconds`, `jwt_auto_renew`, `created_at`, `updated_at`) SELECT `id`, `name`, `description`, `encrypted_seed`, `public_key`, `jwt`, `system_account_pub_key`, `user_jwt_ttl_seconds`, `account_jwt_ttl_seconds`, `jwt_warn_window_seconds`, `jwt_auto_renew`, `created_at`, `updated_at` FROM `operators`;
-- drop "operators" table after copying rows
DROP TABLE `operators`;
-- rename temporary table "new_operators" to "operators"
ALTER TABLE `new_operators` RENAME TO `operators`;
-- create index "idx_operators_public_key" to table: "operators"
CREATE UNIQUE INDEX `idx_operators_public_key` ON `operators` (`public_key`);
-- create index "idx_operators_name" to table: "operators"
CREATE UNIQUE INDEX `idx_operators_name` ON `operators` (`name`);
-- create "operator_backups" table
CREATE TABLE `operator_backups` (`id` text NOT NULL, `operator_id` text NOT NULL, `object_key` text NOT NULL, `size_bytes` bigint NOT NULL, `sha256` text NOT NULL, `trigger_kind` text NOT NULL, `created_at` timestamp NOT NULL DEFAULT (CURRENT_TIMESTAMP), PRIMARY KEY (`id`), CONSTRAINT `fk_operator_backups_operator` FOREIGN KEY (`operator_id`) REFERENCES `operators` (`id`) ON UPDATE NO ACTION ON DELETE CASCADE);
-- create index "idx_operator_backups_object_key" to table: "operator_backups"
CREATE UNIQUE INDEX `idx_operator_backups_object_key` ON `operator_backups` (`object_key`);
-- create index "idx_operator_backups_operator_id" to table: "operator_backups"
CREATE INDEX `idx_operator_backups_operator_id` ON `operator_backups` (`operator_id`);
-- enable back the enforcement of foreign-keys constraints
PRAGMA foreign_keys = on;

-- +goose Down
-- reverse: create index "idx_operator_backups_operator_id" to table: "operator_backups"
DROP INDEX `idx_operator_backups_operator_id`;
-- reverse: create index "idx_operator_backups_object_key" to table: "operator_backups"
DROP INDEX `idx_operator_backups_object_key`;
-- reverse: create "operator_backups" table
DROP TABLE `operator_backups`;
-- Recreate operators table without backup_* columns and the CHECK constraint.
-- SQLite cannot drop a CHECK constraint via ALTER, so we round-trip via a
-- shadow table; the atlas-generated Down referenced `new_operators` which
-- doesn't exist in the post-up state and was unrunnable as a result.
PRAGMA foreign_keys = off;
CREATE TABLE `down_operators` (`id` text NOT NULL, `name` text NOT NULL, `description` text NULL, `encrypted_seed` text NOT NULL, `public_key` text NOT NULL, `jwt` text NOT NULL, `system_account_pub_key` text NULL, `user_jwt_ttl_seconds` bigint NOT NULL DEFAULT 0, `account_jwt_ttl_seconds` bigint NOT NULL DEFAULT 0, `jwt_warn_window_seconds` bigint NOT NULL, `jwt_auto_renew` boolean NOT NULL DEFAULT false, `created_at` timestamp NOT NULL DEFAULT (CURRENT_TIMESTAMP), `updated_at` timestamp NOT NULL DEFAULT (CURRENT_TIMESTAMP), PRIMARY KEY (`id`));
INSERT INTO `down_operators` (`id`, `name`, `description`, `encrypted_seed`, `public_key`, `jwt`, `system_account_pub_key`, `user_jwt_ttl_seconds`, `account_jwt_ttl_seconds`, `jwt_warn_window_seconds`, `jwt_auto_renew`, `created_at`, `updated_at`) SELECT `id`, `name`, `description`, `encrypted_seed`, `public_key`, `jwt`, `system_account_pub_key`, `user_jwt_ttl_seconds`, `account_jwt_ttl_seconds`, `jwt_warn_window_seconds`, `jwt_auto_renew`, `created_at`, `updated_at` FROM `operators`;
DROP TABLE `operators`;
ALTER TABLE `down_operators` RENAME TO `operators`;
CREATE UNIQUE INDEX `idx_operators_public_key` ON `operators` (`public_key`);
CREATE UNIQUE INDEX `idx_operators_name` ON `operators` (`name`);
PRAGMA foreign_keys = on;
