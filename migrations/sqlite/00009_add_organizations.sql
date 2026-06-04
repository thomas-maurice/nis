-- +goose Up
-- +goose NO TRANSACTION
-- HAND-EDITED: re-ordered so organizations table exists before FK-referencing
-- rebuilds, and data backfill is injected between DDL steps.
-- See ORGS_SSO.md §5.1 for the NOT-NULL backfill hazard details.
--
-- NO TRANSACTION is required: SQLite silently ignores PRAGMA foreign_keys = OFF
-- inside a transaction (per SQLite docs). Without it, the ON DELETE SET NULL
-- cascade on api_tokens.created_by_user_id fires when api_users is dropped,
-- corrupting the backfill. Running outside a transaction lets PRAGMA work.

-- disable the enforcement of foreign-keys constraints
PRAGMA foreign_keys = off;

-- Step 1: Create organizations table and the SSO helper tables.
-- These must come first so any FK added during the rebuilds below can resolve.
-- create "organizations" table
CREATE TABLE `organizations` (`id` text NOT NULL, `name` text NOT NULL, `slug` text NOT NULL, `description` text NULL, `created_at` timestamp NOT NULL DEFAULT (CURRENT_TIMESTAMP), `updated_at` timestamp NOT NULL DEFAULT (CURRENT_TIMESTAMP), PRIMARY KEY (`id`));
-- create index "idx_organizations_slug" to table: "organizations"
CREATE UNIQUE INDEX `idx_organizations_slug` ON `organizations` (`slug`);
-- create index "idx_organizations_name" to table: "organizations"
CREATE UNIQUE INDEX `idx_organizations_name` ON `organizations` (`name`);

-- create "organization_sso_configs" table
CREATE TABLE `organization_sso_configs` (`id` text NOT NULL, `organization_id` text NOT NULL, `enabled` boolean NOT NULL DEFAULT false, `issuer_url` text NOT NULL DEFAULT '', `client_id` text NOT NULL DEFAULT '', `encrypted_client_secret` text NOT NULL DEFAULT '', `scopes` text NOT NULL, `group_claim` text NOT NULL, `default_role` text NULL, `created_at` timestamp NOT NULL DEFAULT (CURRENT_TIMESTAMP), `updated_at` timestamp NOT NULL DEFAULT (CURRENT_TIMESTAMP), PRIMARY KEY (`id`), CONSTRAINT `fk_organization_sso_configs_organization` FOREIGN KEY (`organization_id`) REFERENCES `organizations` (`id`) ON UPDATE NO ACTION ON DELETE CASCADE);
-- create index "idx_org_sso_configs_org_id_fk" to table: "organization_sso_configs"
CREATE INDEX `idx_org_sso_configs_org_id_fk` ON `organization_sso_configs` (`organization_id`);
-- create index "idx_org_sso_configs_org_id" to table: "organization_sso_configs"
CREATE UNIQUE INDEX `idx_org_sso_configs_org_id` ON `organization_sso_configs` (`organization_id`);

-- create "oidc_login_states" table
CREATE TABLE `oidc_login_states` (`state` text NOT NULL, `organization_id` text NOT NULL, `nonce` text NOT NULL, `pkce_verifier` text NOT NULL, `redirect_after` text NULL, `created_at` timestamp NOT NULL DEFAULT (CURRENT_TIMESTAMP), `expires_at` timestamp NOT NULL, PRIMARY KEY (`state`), CONSTRAINT `fk_oidc_login_states_organization` FOREIGN KEY (`organization_id`) REFERENCES `organizations` (`id`) ON UPDATE NO ACTION ON DELETE CASCADE);
-- create index "idx_oidc_login_states_expires_at" to table: "oidc_login_states"
CREATE INDEX `idx_oidc_login_states_expires_at` ON `oidc_login_states` (`expires_at`);
-- create index "idx_oidc_login_states_org_id" to table: "oidc_login_states"
CREATE INDEX `idx_oidc_login_states_org_id` ON `oidc_login_states` (`organization_id`);

-- Step 2: Insert the fixed-UUID default organization.
-- All existing operators + non-admin api_users will be backfilled to this org.
-- The UUID '00000000-0000-0000-0000-000000000001' is the constant
-- entities.DefaultOrganizationID defined in internal/domain/entities/organization.go.
INSERT INTO `organizations` (`id`, `name`, `slug`, `description`, `created_at`, `updated_at`)
VALUES ('00000000-0000-0000-0000-000000000001', 'default', 'default', 'Default organization (pre-existing data)', CURRENT_TIMESTAMP, CURRENT_TIMESTAMP);

-- Step 3: Rebuild api_users with the new columns + FK to organizations.
-- The INSERT copies existing rows; organization_id remains NULL here — it is
-- backfilled below after the rebuild completes.
-- create "new_api_users" table
CREATE TABLE `new_api_users` (`id` text NOT NULL, `username` text NOT NULL, `password_hash` text NOT NULL, `role` text NOT NULL, `operator_id` text NULL, `account_id` text NULL, `organization_id` text NULL, `auth_source` text NOT NULL DEFAULT 'local', `external_subject` text NULL, `email` text NULL, `created_at` timestamp NOT NULL DEFAULT (CURRENT_TIMESTAMP), `updated_at` timestamp NOT NULL DEFAULT (CURRENT_TIMESTAMP), PRIMARY KEY (`id`), CONSTRAINT `fk_api_users_organization` FOREIGN KEY (`organization_id`) REFERENCES `organizations` (`id`) ON UPDATE NO ACTION ON DELETE CASCADE, CONSTRAINT `fk_api_users_account` FOREIGN KEY (`account_id`) REFERENCES `accounts` (`id`) ON UPDATE NO ACTION ON DELETE CASCADE, CONSTRAINT `fk_api_users_operator` FOREIGN KEY (`operator_id`) REFERENCES `operators` (`id`) ON UPDATE NO ACTION ON DELETE CASCADE, CONSTRAINT `chk_api_users_oidc_invariant` CHECK (auth_source <> 'oidc' OR (external_subject IS NOT NULL AND organization_id IS NOT NULL)));
-- copy rows from old table "api_users" to new temporary table "new_api_users"
INSERT INTO `new_api_users` (`id`, `username`, `password_hash`, `role`, `operator_id`, `account_id`, `created_at`, `updated_at`) SELECT `id`, `username`, `password_hash`, `role`, `operator_id`, `account_id`, `created_at`, `updated_at` FROM `api_users`;
-- drop "api_users" table after copying rows
DROP TABLE `api_users`;
-- rename temporary table "new_api_users" to "api_users"
ALTER TABLE `new_api_users` RENAME TO `api_users`;
-- create index "idx_api_users_org_id" to table: "api_users"
CREATE INDEX `idx_api_users_org_id` ON `api_users` (`organization_id`);
-- create index "idx_api_users_account_id" to table: "api_users"
CREATE INDEX `idx_api_users_account_id` ON `api_users` (`account_id`);
-- create index "idx_api_users_operator_id" to table: "api_users"
CREATE INDEX `idx_api_users_operator_id` ON `api_users` (`operator_id`);
-- create index "idx_api_users_username" to table: "api_users"
CREATE UNIQUE INDEX `idx_api_users_username` ON `api_users` (`username`);
-- create index "idx_api_users_oidc_subject" (partial, hand-added)
-- See tools/atlas/loader.go manualExtras for the registration that prevents
-- the next atlas-diff from dropping this index.
CREATE UNIQUE INDEX `idx_api_users_oidc_subject` ON `api_users` (`organization_id`, `external_subject`) WHERE `auth_source` = 'oidc';

-- Step 4: Rebuild api_tokens with the new organization_id column.
-- create "new_api_tokens" table
CREATE TABLE `new_api_tokens` (`id` text NOT NULL, `name` text NOT NULL, `token_hash` text NOT NULL, `prefix` text NOT NULL, `description` text NOT NULL DEFAULT '', `created_by_user_id` text NULL, `role` text NOT NULL, `operator_id` text NULL, `account_id` text NULL, `organization_id` text NULL, `expires_at` timestamp NULL, `last_used_at` timestamp NULL, `revoked_at` timestamp NULL, `created_at` timestamp NOT NULL DEFAULT (CURRENT_TIMESTAMP), `updated_at` timestamp NOT NULL DEFAULT (CURRENT_TIMESTAMP), PRIMARY KEY (`id`), CONSTRAINT `fk_api_tokens_organization` FOREIGN KEY (`organization_id`) REFERENCES `organizations` (`id`) ON UPDATE NO ACTION ON DELETE CASCADE, CONSTRAINT `fk_api_tokens_account` FOREIGN KEY (`account_id`) REFERENCES `accounts` (`id`) ON UPDATE NO ACTION ON DELETE CASCADE, CONSTRAINT `fk_api_tokens_operator` FOREIGN KEY (`operator_id`) REFERENCES `operators` (`id`) ON UPDATE NO ACTION ON DELETE CASCADE, CONSTRAINT `fk_api_tokens_created_by_user` FOREIGN KEY (`created_by_user_id`) REFERENCES `api_users` (`id`) ON UPDATE NO ACTION ON DELETE SET NULL);
-- copy rows from old table "api_tokens" to new temporary table "new_api_tokens"
INSERT INTO `new_api_tokens` (`id`, `name`, `token_hash`, `prefix`, `description`, `created_by_user_id`, `role`, `operator_id`, `account_id`, `expires_at`, `last_used_at`, `revoked_at`, `created_at`, `updated_at`) SELECT `id`, `name`, `token_hash`, `prefix`, `description`, `created_by_user_id`, `role`, `operator_id`, `account_id`, `expires_at`, `last_used_at`, `revoked_at`, `created_at`, `updated_at` FROM `api_tokens`;
-- drop "api_tokens" table after copying rows
DROP TABLE `api_tokens`;
-- rename temporary table "new_api_tokens" to "api_tokens"
ALTER TABLE `new_api_tokens` RENAME TO `api_tokens`;
-- create index "idx_api_tokens_revoked_at" to table: "api_tokens"
CREATE INDEX `idx_api_tokens_revoked_at` ON `api_tokens` (`revoked_at`);
-- create index "idx_api_tokens_org_id" to table: "api_tokens"
CREATE INDEX `idx_api_tokens_org_id` ON `api_tokens` (`organization_id`);
-- create index "idx_api_tokens_account_id" to table: "api_tokens"
CREATE INDEX `idx_api_tokens_account_id` ON `api_tokens` (`account_id`);
-- create index "idx_api_tokens_operator_id" to table: "api_tokens"
CREATE INDEX `idx_api_tokens_operator_id` ON `api_tokens` (`operator_id`);
-- create index "idx_api_tokens_created_by_user_id" to table: "api_tokens"
CREATE INDEX `idx_api_tokens_created_by_user_id` ON `api_tokens` (`created_by_user_id`);
-- create index "idx_api_tokens_token_hash" to table: "api_tokens"
CREATE UNIQUE INDEX `idx_api_tokens_token_hash` ON `api_tokens` (`token_hash`);
-- create index "idx_api_tokens_name_per_creator" to table: "api_tokens"
CREATE UNIQUE INDEX `idx_api_tokens_name_per_creator` ON `api_tokens` (`created_by_user_id`, `name`);

-- Step 5: Rebuild operators with organization_id NOT NULL.
-- The INSERT populates organization_id directly with the default org UUID so the
-- NOT NULL constraint is satisfied on the first row that lands in the new table.
-- create "new_operators" table
CREATE TABLE `new_operators` (`id` text NOT NULL, `name` text NOT NULL, `description` text NULL, `encrypted_seed` text NOT NULL, `public_key` text NOT NULL, `jwt` text NOT NULL, `system_account_pub_key` text NULL, `user_jwt_ttl_seconds` bigint NOT NULL DEFAULT 0, `account_jwt_ttl_seconds` bigint NOT NULL DEFAULT 0, `jwt_warn_window_seconds` bigint NOT NULL, `jwt_auto_renew` boolean NOT NULL DEFAULT false, `backup_enabled` boolean NOT NULL DEFAULT false, `backup_interval_seconds` bigint NULL, `backup_retention_count` integer NULL, `last_backup_at` timestamp NULL, `organization_id` text NOT NULL, `created_at` timestamp NOT NULL DEFAULT (CURRENT_TIMESTAMP), `updated_at` timestamp NOT NULL DEFAULT (CURRENT_TIMESTAMP), PRIMARY KEY (`id`), CONSTRAINT `fk_operators_organization` FOREIGN KEY (`organization_id`) REFERENCES `organizations` (`id`) ON UPDATE NO ACTION ON DELETE CASCADE, CONSTRAINT `chk_operators_backup_interval_seconds` CHECK (backup_interval_seconds IS NULL OR backup_interval_seconds >= 3600));
-- copy rows from old table "operators" to new temporary table "new_operators",
-- backfilling organization_id with the default org UUID for all existing rows.
INSERT INTO `new_operators` (`id`, `name`, `description`, `encrypted_seed`, `public_key`, `jwt`, `system_account_pub_key`, `user_jwt_ttl_seconds`, `account_jwt_ttl_seconds`, `jwt_warn_window_seconds`, `jwt_auto_renew`, `backup_enabled`, `backup_interval_seconds`, `backup_retention_count`, `last_backup_at`, `organization_id`, `created_at`, `updated_at`) SELECT `id`, `name`, `description`, `encrypted_seed`, `public_key`, `jwt`, `system_account_pub_key`, `user_jwt_ttl_seconds`, `account_jwt_ttl_seconds`, `jwt_warn_window_seconds`, `jwt_auto_renew`, `backup_enabled`, `backup_interval_seconds`, `backup_retention_count`, `last_backup_at`, '00000000-0000-0000-0000-000000000001', `created_at`, `updated_at` FROM `operators`;
-- drop "operators" table after copying rows
DROP TABLE `operators`;
-- rename temporary table "new_operators" to "operators"
ALTER TABLE `new_operators` RENAME TO `operators`;
-- create index "idx_operators_org_id" to table: "operators"
CREATE INDEX `idx_operators_org_id` ON `operators` (`organization_id`);
-- create index "idx_operators_public_key" to table: "operators"
CREATE UNIQUE INDEX `idx_operators_public_key` ON `operators` (`public_key`);
-- create index "idx_operators_name" to table: "operators"
CREATE UNIQUE INDEX `idx_operators_name` ON `operators` (`name`);

-- create "organization_sso_role_mappings" table (after operators/accounts exist for FK)
CREATE TABLE `organization_sso_role_mappings` (`id` text NOT NULL, `organization_id` text NOT NULL, `group_value` text NOT NULL, `role` text NOT NULL, `scope_operator_id` text NULL, `scope_account_id` text NULL, `priority` integer NOT NULL DEFAULT 0, `created_at` timestamp NOT NULL DEFAULT (CURRENT_TIMESTAMP), PRIMARY KEY (`id`), CONSTRAINT `fk_organization_sso_role_mappings_organization` FOREIGN KEY (`organization_id`) REFERENCES `organizations` (`id`) ON UPDATE NO ACTION ON DELETE CASCADE, CONSTRAINT `fk_organization_sso_role_mappings_scope_account` FOREIGN KEY (`scope_account_id`) REFERENCES `accounts` (`id`) ON UPDATE NO ACTION ON DELETE SET NULL, CONSTRAINT `fk_organization_sso_role_mappings_scope_operator` FOREIGN KEY (`scope_operator_id`) REFERENCES `operators` (`id`) ON UPDATE NO ACTION ON DELETE SET NULL);
-- create index "idx_sso_role_mappings_scope_acc_id" to table: "organization_sso_role_mappings"
CREATE INDEX `idx_sso_role_mappings_scope_acc_id` ON `organization_sso_role_mappings` (`scope_account_id`);
-- create index "idx_sso_role_mappings_scope_op_id" to table: "organization_sso_role_mappings"
CREATE INDEX `idx_sso_role_mappings_scope_op_id` ON `organization_sso_role_mappings` (`scope_operator_id`);
-- create index "idx_sso_role_mappings_org_group" to table: "organization_sso_role_mappings"
CREATE UNIQUE INDEX `idx_sso_role_mappings_org_group` ON `organization_sso_role_mappings` (`organization_id`, `group_value`);
-- create index "idx_sso_role_mappings_org_id" to table: "organization_sso_role_mappings"
CREATE INDEX `idx_sso_role_mappings_org_id` ON `organization_sso_role_mappings` (`organization_id`);

-- enable back the enforcement of foreign-keys constraints
PRAGMA foreign_keys = on;

-- Step 6: Data backfill (FKs re-enabled; all constraints now enforced).
-- Assign non-admin api_users to the default org. Platform admins (role='admin')
-- stay NULL (org-less by design).
UPDATE `api_users` SET `organization_id` = '00000000-0000-0000-0000-000000000001'
WHERE `role` <> 'admin';

-- Assign api_tokens to the default org when their creator is an org-scoped user.
UPDATE `api_tokens` SET `organization_id` = '00000000-0000-0000-0000-000000000001'
WHERE `created_by_user_id` IN (
    SELECT `id` FROM `api_users` WHERE `role` <> 'admin'
);

-- +goose Down
-- Reverse the migration. We drop the new tables/indexes/columns in reverse
-- order and rebuild the three altered tables back to their original shape.
-- Note: data backfilled into organization_id is lost on rollback (expected).

PRAGMA foreign_keys = off;

-- Drop new tables (no dependents at this point after rebuilds below)
DROP INDEX IF EXISTS `idx_sso_role_mappings_org_id`;
DROP INDEX IF EXISTS `idx_sso_role_mappings_org_group`;
DROP INDEX IF EXISTS `idx_sso_role_mappings_scope_op_id`;
DROP INDEX IF EXISTS `idx_sso_role_mappings_scope_acc_id`;
DROP TABLE IF EXISTS `organization_sso_role_mappings`;

-- Rebuild operators without organization_id
CREATE TABLE `down_operators` (`id` text NOT NULL, `name` text NOT NULL, `description` text NULL, `encrypted_seed` text NOT NULL, `public_key` text NOT NULL, `jwt` text NOT NULL, `system_account_pub_key` text NULL, `user_jwt_ttl_seconds` bigint NOT NULL DEFAULT 0, `account_jwt_ttl_seconds` bigint NOT NULL DEFAULT 0, `jwt_warn_window_seconds` bigint NOT NULL, `jwt_auto_renew` boolean NOT NULL DEFAULT false, `backup_enabled` boolean NOT NULL DEFAULT false, `backup_interval_seconds` bigint NULL, `backup_retention_count` integer NULL, `last_backup_at` timestamp NULL, `created_at` timestamp NOT NULL DEFAULT (CURRENT_TIMESTAMP), `updated_at` timestamp NOT NULL DEFAULT (CURRENT_TIMESTAMP), PRIMARY KEY (`id`), CONSTRAINT `chk_operators_backup_interval_seconds` CHECK (backup_interval_seconds IS NULL OR backup_interval_seconds >= 3600));
INSERT INTO `down_operators` (`id`, `name`, `description`, `encrypted_seed`, `public_key`, `jwt`, `system_account_pub_key`, `user_jwt_ttl_seconds`, `account_jwt_ttl_seconds`, `jwt_warn_window_seconds`, `jwt_auto_renew`, `backup_enabled`, `backup_interval_seconds`, `backup_retention_count`, `last_backup_at`, `created_at`, `updated_at`) SELECT `id`, `name`, `description`, `encrypted_seed`, `public_key`, `jwt`, `system_account_pub_key`, `user_jwt_ttl_seconds`, `account_jwt_ttl_seconds`, `jwt_warn_window_seconds`, `jwt_auto_renew`, `backup_enabled`, `backup_interval_seconds`, `backup_retention_count`, `last_backup_at`, `created_at`, `updated_at` FROM `operators`;
DROP INDEX `idx_operators_org_id`;
DROP INDEX `idx_operators_public_key`;
DROP INDEX `idx_operators_name`;
DROP TABLE `operators`;
ALTER TABLE `down_operators` RENAME TO `operators`;
CREATE UNIQUE INDEX `idx_operators_public_key` ON `operators` (`public_key`);
CREATE UNIQUE INDEX `idx_operators_name` ON `operators` (`name`);

-- Rebuild api_tokens without organization_id
CREATE TABLE `down_api_tokens` (`id` text NOT NULL, `name` text NOT NULL, `token_hash` text NOT NULL, `prefix` text NOT NULL, `description` text NOT NULL DEFAULT '', `created_by_user_id` text NULL, `role` text NOT NULL, `operator_id` text NULL, `account_id` text NULL, `expires_at` timestamp NULL, `last_used_at` timestamp NULL, `revoked_at` timestamp NULL, `created_at` timestamp NOT NULL DEFAULT (CURRENT_TIMESTAMP), `updated_at` timestamp NOT NULL DEFAULT (CURRENT_TIMESTAMP), PRIMARY KEY (`id`), CONSTRAINT `fk_api_tokens_account` FOREIGN KEY (`account_id`) REFERENCES `accounts` (`id`) ON UPDATE NO ACTION ON DELETE CASCADE, CONSTRAINT `fk_api_tokens_operator` FOREIGN KEY (`operator_id`) REFERENCES `operators` (`id`) ON UPDATE NO ACTION ON DELETE CASCADE, CONSTRAINT `fk_api_tokens_created_by_user` FOREIGN KEY (`created_by_user_id`) REFERENCES `api_users` (`id`) ON UPDATE NO ACTION ON DELETE SET NULL);
INSERT INTO `down_api_tokens` (`id`, `name`, `token_hash`, `prefix`, `description`, `created_by_user_id`, `role`, `operator_id`, `account_id`, `expires_at`, `last_used_at`, `revoked_at`, `created_at`, `updated_at`) SELECT `id`, `name`, `token_hash`, `prefix`, `description`, `created_by_user_id`, `role`, `operator_id`, `account_id`, `expires_at`, `last_used_at`, `revoked_at`, `created_at`, `updated_at` FROM `api_tokens`;
DROP INDEX `idx_api_tokens_revoked_at`;
DROP INDEX `idx_api_tokens_org_id`;
DROP INDEX `idx_api_tokens_account_id`;
DROP INDEX `idx_api_tokens_operator_id`;
DROP INDEX `idx_api_tokens_created_by_user_id`;
DROP INDEX `idx_api_tokens_token_hash`;
DROP INDEX `idx_api_tokens_name_per_creator`;
DROP TABLE `api_tokens`;
ALTER TABLE `down_api_tokens` RENAME TO `api_tokens`;
CREATE INDEX `idx_api_tokens_revoked_at` ON `api_tokens` (`revoked_at`);
CREATE INDEX `idx_api_tokens_account_id` ON `api_tokens` (`account_id`);
CREATE INDEX `idx_api_tokens_operator_id` ON `api_tokens` (`operator_id`);
CREATE INDEX `idx_api_tokens_created_by_user_id` ON `api_tokens` (`created_by_user_id`);
CREATE UNIQUE INDEX `idx_api_tokens_token_hash` ON `api_tokens` (`token_hash`);
CREATE UNIQUE INDEX `idx_api_tokens_name_per_creator` ON `api_tokens` (`created_by_user_id`, `name`);

-- Rebuild api_users without the org/oidc columns
CREATE TABLE `down_api_users` (`id` text NOT NULL, `username` text NOT NULL, `password_hash` text NOT NULL, `role` text NOT NULL, `operator_id` text NULL, `account_id` text NULL, `created_at` timestamp NOT NULL DEFAULT (CURRENT_TIMESTAMP), `updated_at` timestamp NOT NULL DEFAULT (CURRENT_TIMESTAMP), PRIMARY KEY (`id`), CONSTRAINT `fk_api_users_account` FOREIGN KEY (`account_id`) REFERENCES `accounts` (`id`) ON UPDATE NO ACTION ON DELETE CASCADE, CONSTRAINT `fk_api_users_operator` FOREIGN KEY (`operator_id`) REFERENCES `operators` (`id`) ON UPDATE NO ACTION ON DELETE CASCADE);
INSERT INTO `down_api_users` (`id`, `username`, `password_hash`, `role`, `operator_id`, `account_id`, `created_at`, `updated_at`) SELECT `id`, `username`, `password_hash`, `role`, `operator_id`, `account_id`, `created_at`, `updated_at` FROM `api_users`;
DROP INDEX `idx_api_users_oidc_subject`;
DROP INDEX `idx_api_users_username`;
DROP INDEX `idx_api_users_operator_id`;
DROP INDEX `idx_api_users_account_id`;
DROP INDEX `idx_api_users_org_id`;
DROP TABLE `api_users`;
ALTER TABLE `down_api_users` RENAME TO `api_users`;
CREATE UNIQUE INDEX `idx_api_users_username` ON `api_users` (`username`);
CREATE INDEX `idx_api_users_operator_id` ON `api_users` (`operator_id`);
CREATE INDEX `idx_api_users_account_id` ON `api_users` (`account_id`);

-- Drop SSO tables and organizations
DROP INDEX IF EXISTS `idx_oidc_login_states_org_id`;
DROP INDEX IF EXISTS `idx_oidc_login_states_expires_at`;
DROP TABLE IF EXISTS `oidc_login_states`;
DROP INDEX IF EXISTS `idx_org_sso_configs_org_id`;
DROP INDEX IF EXISTS `idx_org_sso_configs_org_id_fk`;
DROP TABLE IF EXISTS `organization_sso_configs`;
DROP INDEX IF EXISTS `idx_organizations_name`;
DROP INDEX IF EXISTS `idx_organizations_slug`;
DROP TABLE IF EXISTS `organizations`;

PRAGMA foreign_keys = on;
