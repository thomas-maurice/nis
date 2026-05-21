-- +goose Up
-- create "templates" + "template_versions" BEFORE the scoped_signing_keys
-- recreation, because new_scoped_signing_keys declares an FK to
-- templates(id) and SQLite resolves referenced tables at table-creation
-- time even with `PRAGMA foreign_keys = off`. Atlas emits the SSK
-- recreation first; the order is hand-corrected here.
--
-- create "templates" table
CREATE TABLE `templates` (`id` text NOT NULL, `operator_id` text NOT NULL, `name` text NOT NULL, `description` text NULL, `latest_version` integer NOT NULL DEFAULT 0, `created_at` timestamp NOT NULL DEFAULT (CURRENT_TIMESTAMP), `updated_at` timestamp NOT NULL DEFAULT (CURRENT_TIMESTAMP), PRIMARY KEY (`id`), CONSTRAINT `fk_templates_operator` FOREIGN KEY (`operator_id`) REFERENCES `operators` (`id`) ON UPDATE NO ACTION ON DELETE CASCADE);
-- create index "idx_templates_operator_name" to table: "templates"
CREATE UNIQUE INDEX `idx_templates_operator_name` ON `templates` (`operator_id`, `name`);
-- create index "idx_templates_operator_id" to table: "templates"
CREATE INDEX `idx_templates_operator_id` ON `templates` (`operator_id`);
-- create "template_versions" table
CREATE TABLE `template_versions` (`id` text NOT NULL, `template_id` text NOT NULL, `version_number` integer NOT NULL, `pub_allow` text NULL, `pub_deny` text NULL, `sub_allow` text NULL, `sub_deny` text NULL, `response_max_msgs` integer NOT NULL DEFAULT 0, `response_ttl_seconds` bigint NOT NULL DEFAULT 0, `change_note` text NOT NULL DEFAULT '', `created_at` timestamp NOT NULL DEFAULT (CURRENT_TIMESTAMP), `created_by_user_id` text NULL, PRIMARY KEY (`id`), CONSTRAINT `fk_template_versions_created_by_user` FOREIGN KEY (`created_by_user_id`) REFERENCES `api_users` (`id`) ON UPDATE NO ACTION ON DELETE SET NULL, CONSTRAINT `fk_template_versions_template` FOREIGN KEY (`template_id`) REFERENCES `templates` (`id`) ON UPDATE NO ACTION ON DELETE CASCADE);
-- create index "idx_template_versions_created_by_user_id" to table: "template_versions"
CREATE INDEX `idx_template_versions_created_by_user_id` ON `template_versions` (`created_by_user_id`);
-- create index "idx_template_versions_template_number" to table: "template_versions"
CREATE UNIQUE INDEX `idx_template_versions_template_number` ON `template_versions` (`template_id`, `version_number`);
-- create index "idx_template_versions_template_id" to table: "template_versions"
CREATE INDEX `idx_template_versions_template_id` ON `template_versions` (`template_id`);
-- disable the enforcement of foreign-keys constraints
PRAGMA foreign_keys = off;
-- create "new_scoped_signing_keys" table
CREATE TABLE `new_scoped_signing_keys` (`id` text NOT NULL, `account_id` text NOT NULL, `name` text NOT NULL, `description` text NULL, `encrypted_seed` text NOT NULL, `public_key` text NOT NULL, `pub_allow` text NULL, `pub_deny` text NULL, `sub_allow` text NULL, `sub_deny` text NULL, `response_max_msgs` integer NOT NULL DEFAULT 0, `response_ttl_seconds` bigint NOT NULL DEFAULT 0, `template_id` text NULL, `template_version` integer NULL, `template_drifted` boolean NOT NULL DEFAULT false, `created_at` timestamp NOT NULL DEFAULT (CURRENT_TIMESTAMP), `updated_at` timestamp NOT NULL DEFAULT (CURRENT_TIMESTAMP), PRIMARY KEY (`id`), CONSTRAINT `fk_scoped_signing_keys_template` FOREIGN KEY (`template_id`) REFERENCES `templates` (`id`) ON UPDATE NO ACTION ON DELETE SET NULL, CONSTRAINT `fk_scoped_signing_keys_account` FOREIGN KEY (`account_id`) REFERENCES `accounts` (`id`) ON UPDATE NO ACTION ON DELETE CASCADE, CONSTRAINT `chk_scoped_signing_keys_template_pair` CHECK ((template_id IS NULL) = (template_version IS NULL)));
-- copy rows from old table "scoped_signing_keys" to new temporary table "new_scoped_signing_keys"
INSERT INTO `new_scoped_signing_keys` (`id`, `account_id`, `name`, `description`, `encrypted_seed`, `public_key`, `pub_allow`, `pub_deny`, `sub_allow`, `sub_deny`, `response_max_msgs`, `response_ttl_seconds`, `created_at`, `updated_at`) SELECT `id`, `account_id`, `name`, `description`, `encrypted_seed`, `public_key`, `pub_allow`, `pub_deny`, `sub_allow`, `sub_deny`, `response_max_msgs`, `response_ttl_seconds`, `created_at`, `updated_at` FROM `scoped_signing_keys`;
-- drop "scoped_signing_keys" table after copying rows
DROP TABLE `scoped_signing_keys`;
-- rename temporary table "new_scoped_signing_keys" to "scoped_signing_keys"
ALTER TABLE `new_scoped_signing_keys` RENAME TO `scoped_signing_keys`;
-- create index "idx_scoped_signing_keys_template_id" to table: "scoped_signing_keys"
CREATE INDEX `idx_scoped_signing_keys_template_id` ON `scoped_signing_keys` (`template_id`);
-- create index "idx_scoped_signing_keys_public_key" to table: "scoped_signing_keys"
CREATE UNIQUE INDEX `idx_scoped_signing_keys_public_key` ON `scoped_signing_keys` (`public_key`);
-- create index "idx_scoped_signing_keys_account_name" to table: "scoped_signing_keys"
CREATE UNIQUE INDEX `idx_scoped_signing_keys_account_name` ON `scoped_signing_keys` (`account_id`, `name`);
-- create index "idx_scoped_signing_keys_account_id" to table: "scoped_signing_keys"
CREATE INDEX `idx_scoped_signing_keys_account_id` ON `scoped_signing_keys` (`account_id`);
-- enable back the enforcement of foreign-keys constraints
PRAGMA foreign_keys = on;

-- +goose Down
-- Rebuild scoped_signing_keys WITHOUT the three template columns so the
-- table is back in the shape 00001 created and 00001's Down can drop it
-- cleanly. SQLite ALTER TABLE DROP COLUMN exists from 3.35 but can fail
-- on a table referenced by a FOREIGN KEY in another table, so we use the
-- same recreate/copy/rename pattern the Up uses.
PRAGMA foreign_keys = off;
CREATE TABLE `old_scoped_signing_keys` (`id` text NOT NULL, `account_id` text NOT NULL, `name` text NOT NULL, `description` text NULL, `encrypted_seed` text NOT NULL, `public_key` text NOT NULL, `pub_allow` text NULL, `pub_deny` text NULL, `sub_allow` text NULL, `sub_deny` text NULL, `response_max_msgs` integer NOT NULL DEFAULT 0, `response_ttl_seconds` bigint NOT NULL DEFAULT 0, `created_at` timestamp NOT NULL DEFAULT (CURRENT_TIMESTAMP), `updated_at` timestamp NOT NULL DEFAULT (CURRENT_TIMESTAMP), PRIMARY KEY (`id`), CONSTRAINT `fk_scoped_signing_keys_account` FOREIGN KEY (`account_id`) REFERENCES `accounts` (`id`) ON UPDATE NO ACTION ON DELETE CASCADE);
INSERT INTO `old_scoped_signing_keys` (`id`, `account_id`, `name`, `description`, `encrypted_seed`, `public_key`, `pub_allow`, `pub_deny`, `sub_allow`, `sub_deny`, `response_max_msgs`, `response_ttl_seconds`, `created_at`, `updated_at`) SELECT `id`, `account_id`, `name`, `description`, `encrypted_seed`, `public_key`, `pub_allow`, `pub_deny`, `sub_allow`, `sub_deny`, `response_max_msgs`, `response_ttl_seconds`, `created_at`, `updated_at` FROM `scoped_signing_keys`;
DROP TABLE `scoped_signing_keys`;
ALTER TABLE `old_scoped_signing_keys` RENAME TO `scoped_signing_keys`;
CREATE UNIQUE INDEX `idx_scoped_signing_keys_public_key` ON `scoped_signing_keys` (`public_key`);
CREATE UNIQUE INDEX `idx_scoped_signing_keys_account_name` ON `scoped_signing_keys` (`account_id`, `name`);
CREATE INDEX `idx_scoped_signing_keys_account_id` ON `scoped_signing_keys` (`account_id`);
PRAGMA foreign_keys = on;
-- reverse: create "template_versions" table
DROP TABLE `template_versions`;
-- reverse: create "templates" table
DROP TABLE `templates`;
