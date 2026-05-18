-- +goose Up
-- create "operators" table
CREATE TABLE `operators` (`id` text NOT NULL, `name` text NOT NULL, `description` text NULL, `encrypted_seed` text NOT NULL, `public_key` text NOT NULL, `jwt` text NOT NULL, `system_account_pub_key` text NULL, `user_jwt_ttl_seconds` bigint NOT NULL DEFAULT 0, `account_jwt_ttl_seconds` bigint NOT NULL DEFAULT 0, `jwt_warn_window_seconds` bigint NOT NULL, `jwt_auto_renew` boolean NOT NULL DEFAULT false, `created_at` timestamp NOT NULL DEFAULT (CURRENT_TIMESTAMP), `updated_at` timestamp NOT NULL DEFAULT (CURRENT_TIMESTAMP), PRIMARY KEY (`id`));
-- create index "idx_operators_public_key" to table: "operators"
CREATE UNIQUE INDEX `idx_operators_public_key` ON `operators` (`public_key`);
-- create index "idx_operators_name" to table: "operators"
CREATE UNIQUE INDEX `idx_operators_name` ON `operators` (`name`);
-- create "accounts" table
CREATE TABLE `accounts` (`id` text NOT NULL, `operator_id` text NOT NULL, `name` text NOT NULL, `description` text NULL, `encrypted_seed` text NOT NULL, `public_key` text NOT NULL, `jwt` text NOT NULL, `jetstream_enabled` boolean NOT NULL DEFAULT false, `jetstream_max_memory` bigint NOT NULL, `jetstream_max_storage` bigint NOT NULL, `jetstream_max_streams` bigint NOT NULL, `jetstream_max_consumers` bigint NOT NULL, `created_at` timestamp NOT NULL DEFAULT (CURRENT_TIMESTAMP), `updated_at` timestamp NOT NULL DEFAULT (CURRENT_TIMESTAMP), PRIMARY KEY (`id`), CONSTRAINT `fk_accounts_operator` FOREIGN KEY (`operator_id`) REFERENCES `operators` (`id`) ON UPDATE NO ACTION ON DELETE CASCADE);
-- create index "idx_accounts_public_key" to table: "accounts"
CREATE UNIQUE INDEX `idx_accounts_public_key` ON `accounts` (`public_key`);
-- create index "idx_accounts_operator_name" to table: "accounts"
CREATE UNIQUE INDEX `idx_accounts_operator_name` ON `accounts` (`operator_id`, `name`);
-- create index "idx_accounts_operator_id" to table: "accounts"
CREATE INDEX `idx_accounts_operator_id` ON `accounts` (`operator_id`);
-- create "scoped_signing_keys" table
CREATE TABLE `scoped_signing_keys` (`id` text NOT NULL, `account_id` text NOT NULL, `name` text NOT NULL, `description` text NULL, `encrypted_seed` text NOT NULL, `public_key` text NOT NULL, `pub_allow` text NULL, `pub_deny` text NULL, `sub_allow` text NULL, `sub_deny` text NULL, `response_max_msgs` integer NOT NULL DEFAULT 0, `response_ttl_seconds` bigint NOT NULL DEFAULT 0, `created_at` timestamp NOT NULL DEFAULT (CURRENT_TIMESTAMP), `updated_at` timestamp NOT NULL DEFAULT (CURRENT_TIMESTAMP), PRIMARY KEY (`id`), CONSTRAINT `fk_scoped_signing_keys_account` FOREIGN KEY (`account_id`) REFERENCES `accounts` (`id`) ON UPDATE NO ACTION ON DELETE CASCADE);
-- create index "idx_scoped_signing_keys_public_key" to table: "scoped_signing_keys"
CREATE UNIQUE INDEX `idx_scoped_signing_keys_public_key` ON `scoped_signing_keys` (`public_key`);
-- create index "idx_scoped_signing_keys_account_name" to table: "scoped_signing_keys"
CREATE UNIQUE INDEX `idx_scoped_signing_keys_account_name` ON `scoped_signing_keys` (`account_id`, `name`);
-- create index "idx_scoped_signing_keys_account_id" to table: "scoped_signing_keys"
CREATE INDEX `idx_scoped_signing_keys_account_id` ON `scoped_signing_keys` (`account_id`);
-- create "users" table
CREATE TABLE `users` (`id` text NOT NULL, `account_id` text NOT NULL, `name` text NOT NULL, `description` text NULL, `encrypted_seed` text NOT NULL, `public_key` text NOT NULL, `jwt` text NOT NULL, `scoped_signing_key_id` text NULL, `jwt_ttl_seconds` bigint NULL, `jwt_issued_at` timestamp NULL, `jwt_expires_at` timestamp NULL, `revoked_at` timestamp NULL, `revocation_reason` text NOT NULL DEFAULT '', `last_expiring_warn_iat` timestamp NULL, `last_expired_alert_iat` timestamp NULL, `created_at` timestamp NOT NULL DEFAULT (CURRENT_TIMESTAMP), `updated_at` timestamp NOT NULL DEFAULT (CURRENT_TIMESTAMP), PRIMARY KEY (`id`), CONSTRAINT `fk_users_scoped_signing_key` FOREIGN KEY (`scoped_signing_key_id`) REFERENCES `scoped_signing_keys` (`id`) ON UPDATE NO ACTION ON DELETE SET NULL, CONSTRAINT `fk_users_account` FOREIGN KEY (`account_id`) REFERENCES `accounts` (`id`) ON UPDATE NO ACTION ON DELETE CASCADE);
-- create index "idx_users_revoked_at" to table: "users"
CREATE INDEX `idx_users_revoked_at` ON `users` (`revoked_at`);
-- create index "idx_users_jwt_expires_at" to table: "users"
CREATE INDEX `idx_users_jwt_expires_at` ON `users` (`jwt_expires_at`);
-- create index "idx_users_scoped_signing_key_id" to table: "users"
CREATE INDEX `idx_users_scoped_signing_key_id` ON `users` (`scoped_signing_key_id`);
-- create index "idx_users_public_key" to table: "users"
CREATE UNIQUE INDEX `idx_users_public_key` ON `users` (`public_key`);
-- create index "idx_users_account_name" to table: "users"
CREATE UNIQUE INDEX `idx_users_account_name` ON `users` (`account_id`, `name`);
-- create index "idx_users_account_id" to table: "users"
CREATE INDEX `idx_users_account_id` ON `users` (`account_id`);
-- create "clusters" table
CREATE TABLE `clusters` (`id` text NOT NULL, `name` text NOT NULL, `description` text NULL, `server_urls` text NOT NULL, `operator_id` text NOT NULL, `system_account_pub_key` text NULL, `encrypted_creds` text NULL, `skip_verify_tls` boolean NOT NULL DEFAULT false, `healthy` boolean NOT NULL DEFAULT false, `last_health_check` timestamp NULL, `health_check_error` text NOT NULL DEFAULT '', `created_at` timestamp NOT NULL DEFAULT (CURRENT_TIMESTAMP), `updated_at` timestamp NOT NULL DEFAULT (CURRENT_TIMESTAMP), PRIMARY KEY (`id`), CONSTRAINT `fk_clusters_operator` FOREIGN KEY (`operator_id`) REFERENCES `operators` (`id`) ON UPDATE NO ACTION ON DELETE RESTRICT);
-- create index "idx_clusters_operator_id" to table: "clusters"
CREATE INDEX `idx_clusters_operator_id` ON `clusters` (`operator_id`);
-- create index "idx_clusters_name" to table: "clusters"
CREATE UNIQUE INDEX `idx_clusters_name` ON `clusters` (`name`);
-- create "api_users" table
CREATE TABLE `api_users` (`id` text NOT NULL, `username` text NOT NULL, `password_hash` text NOT NULL, `role` text NOT NULL, `operator_id` text NULL, `account_id` text NULL, `created_at` timestamp NOT NULL DEFAULT (CURRENT_TIMESTAMP), `updated_at` timestamp NOT NULL DEFAULT (CURRENT_TIMESTAMP), PRIMARY KEY (`id`), CONSTRAINT `fk_api_users_account` FOREIGN KEY (`account_id`) REFERENCES `accounts` (`id`) ON UPDATE NO ACTION ON DELETE CASCADE, CONSTRAINT `fk_api_users_operator` FOREIGN KEY (`operator_id`) REFERENCES `operators` (`id`) ON UPDATE NO ACTION ON DELETE CASCADE);
-- create index "idx_api_users_account_id" to table: "api_users"
CREATE INDEX `idx_api_users_account_id` ON `api_users` (`account_id`);
-- create index "idx_api_users_operator_id" to table: "api_users"
CREATE INDEX `idx_api_users_operator_id` ON `api_users` (`operator_id`);
-- create index "idx_api_users_username" to table: "api_users"
CREATE UNIQUE INDEX `idx_api_users_username` ON `api_users` (`username`);
-- create "api_tokens" table
CREATE TABLE `api_tokens` (`id` text NOT NULL, `name` text NOT NULL, `token_hash` text NOT NULL, `prefix` text NOT NULL, `description` text NOT NULL DEFAULT '', `created_by_user_id` text NULL, `role` text NOT NULL, `operator_id` text NULL, `account_id` text NULL, `expires_at` timestamp NULL, `last_used_at` timestamp NULL, `revoked_at` timestamp NULL, `created_at` timestamp NOT NULL DEFAULT (CURRENT_TIMESTAMP), `updated_at` timestamp NOT NULL DEFAULT (CURRENT_TIMESTAMP), PRIMARY KEY (`id`), CONSTRAINT `fk_api_tokens_operator` FOREIGN KEY (`operator_id`) REFERENCES `operators` (`id`) ON UPDATE NO ACTION ON DELETE CASCADE, CONSTRAINT `fk_api_tokens_created_by_user` FOREIGN KEY (`created_by_user_id`) REFERENCES `api_users` (`id`) ON UPDATE NO ACTION ON DELETE SET NULL, CONSTRAINT `fk_api_tokens_account` FOREIGN KEY (`account_id`) REFERENCES `accounts` (`id`) ON UPDATE NO ACTION ON DELETE CASCADE);
-- create index "idx_api_tokens_revoked_at" to table: "api_tokens"
CREATE INDEX `idx_api_tokens_revoked_at` ON `api_tokens` (`revoked_at`);
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
-- create "events" table
CREATE TABLE `events` (`id` text NOT NULL, `occurred_at` timestamp NOT NULL DEFAULT (CURRENT_TIMESTAMP), `type` text NOT NULL, `actor_type` text NOT NULL, `actor_id` text NULL, `operator_id` text NULL, `account_id` text NULL, `resource_type` text NOT NULL, `resource_id` text NOT NULL, `payload` text NULL, PRIMARY KEY (`id`));
-- create index "idx_events_resource" to table: "events"
CREATE INDEX `idx_events_resource` ON `events` (`resource_type`, `resource_id`);
-- create index "idx_events_operator_id" to table: "events"
CREATE INDEX `idx_events_operator_id` ON `events` (`operator_id`);
-- create index "idx_events_type" to table: "events"
CREATE INDEX `idx_events_type` ON `events` (`type`);
-- create index "idx_events_occurred_at" to table: "events"
CREATE INDEX `idx_events_occurred_at` ON `events` (`occurred_at`);
-- create "webhook_subscriptions" table
CREATE TABLE `webhook_subscriptions` (`id` text NOT NULL, `operator_id` text NOT NULL, `name` text NOT NULL, `description` text NULL, `url` text NOT NULL, `encrypted_secret` text NOT NULL, `event_types` text NOT NULL, `enabled` boolean NOT NULL, `disabled_reason` text NOT NULL DEFAULT '', `created_at` timestamp NOT NULL DEFAULT (CURRENT_TIMESTAMP), `updated_at` timestamp NOT NULL DEFAULT (CURRENT_TIMESTAMP), PRIMARY KEY (`id`), CONSTRAINT `fk_webhook_subscriptions_operator` FOREIGN KEY (`operator_id`) REFERENCES `operators` (`id`) ON UPDATE NO ACTION ON DELETE CASCADE);
-- create index "idx_webhook_subscriptions_enabled" to table: "webhook_subscriptions"
CREATE INDEX `idx_webhook_subscriptions_enabled` ON `webhook_subscriptions` (`enabled`);
-- create index "idx_webhook_subscriptions_operator_name" to table: "webhook_subscriptions"
CREATE UNIQUE INDEX `idx_webhook_subscriptions_operator_name` ON `webhook_subscriptions` (`operator_id`, `name`);
-- create index "idx_webhook_subscriptions_operator_id" to table: "webhook_subscriptions"
CREATE INDEX `idx_webhook_subscriptions_operator_id` ON `webhook_subscriptions` (`operator_id`);
-- create "webhook_deliveries" table
CREATE TABLE `webhook_deliveries` (`id` text NOT NULL, `subscription_id` text NOT NULL, `event_id` text NOT NULL, `attempt` integer NOT NULL DEFAULT 0, `status` text NOT NULL, `next_attempt_at` timestamp NOT NULL, `last_error` text NOT NULL DEFAULT '', `last_response_code` integer NOT NULL DEFAULT 0, `created_at` timestamp NOT NULL DEFAULT (CURRENT_TIMESTAMP), `updated_at` timestamp NOT NULL DEFAULT (CURRENT_TIMESTAMP), `completed_at` timestamp NULL, PRIMARY KEY (`id`), CONSTRAINT `fk_webhook_deliveries_event` FOREIGN KEY (`event_id`) REFERENCES `events` (`id`) ON UPDATE NO ACTION ON DELETE CASCADE, CONSTRAINT `fk_webhook_deliveries_subscription` FOREIGN KEY (`subscription_id`) REFERENCES `webhook_subscriptions` (`id`) ON UPDATE NO ACTION ON DELETE CASCADE);
-- create index "idx_webhook_deliveries_status_next" to table: "webhook_deliveries"
CREATE INDEX `idx_webhook_deliveries_status_next` ON `webhook_deliveries` (`status`, `next_attempt_at`);
-- create index "idx_webhook_deliveries_event_id" to table: "webhook_deliveries"
CREATE INDEX `idx_webhook_deliveries_event_id` ON `webhook_deliveries` (`event_id`);
-- create index "idx_webhook_deliveries_subscription_id" to table: "webhook_deliveries"
CREATE INDEX `idx_webhook_deliveries_subscription_id` ON `webhook_deliveries` (`subscription_id`);
-- create "user_jwt_revocations" table
CREATE TABLE `user_jwt_revocations` (`id` text NOT NULL, `account_id` text NOT NULL, `user_id` text NULL, `user_public_key` text NOT NULL, `revoked_at` timestamp NOT NULL, `jwt_exp` timestamp NOT NULL, `reason` text NOT NULL DEFAULT '', `pruned_at` timestamp NULL, `created_at` timestamp NOT NULL DEFAULT (CURRENT_TIMESTAMP), PRIMARY KEY (`id`), CONSTRAINT `fk_user_jwt_revocations_user` FOREIGN KEY (`user_id`) REFERENCES `users` (`id`) ON UPDATE NO ACTION ON DELETE SET NULL, CONSTRAINT `fk_user_jwt_revocations_account` FOREIGN KEY (`account_id`) REFERENCES `accounts` (`id`) ON UPDATE NO ACTION ON DELETE CASCADE);
-- create index "idx_user_jwt_revocations_pruned_at" to table: "user_jwt_revocations"
CREATE INDEX `idx_user_jwt_revocations_pruned_at` ON `user_jwt_revocations` (`pruned_at`);
-- create index "idx_user_jwt_revocations_jwt_exp" to table: "user_jwt_revocations"
CREATE INDEX `idx_user_jwt_revocations_jwt_exp` ON `user_jwt_revocations` (`jwt_exp`);
-- create index "idx_user_jwt_revocations_account_id" to table: "user_jwt_revocations"
CREATE INDEX `idx_user_jwt_revocations_account_id` ON `user_jwt_revocations` (`account_id`);

-- +goose Down
-- reverse: create index "idx_user_jwt_revocations_account_id" to table: "user_jwt_revocations"
DROP INDEX `idx_user_jwt_revocations_account_id`;
-- reverse: create index "idx_user_jwt_revocations_jwt_exp" to table: "user_jwt_revocations"
DROP INDEX `idx_user_jwt_revocations_jwt_exp`;
-- reverse: create index "idx_user_jwt_revocations_pruned_at" to table: "user_jwt_revocations"
DROP INDEX `idx_user_jwt_revocations_pruned_at`;
-- reverse: create "user_jwt_revocations" table
DROP TABLE `user_jwt_revocations`;
-- reverse: create index "idx_webhook_deliveries_subscription_id" to table: "webhook_deliveries"
DROP INDEX `idx_webhook_deliveries_subscription_id`;
-- reverse: create index "idx_webhook_deliveries_event_id" to table: "webhook_deliveries"
DROP INDEX `idx_webhook_deliveries_event_id`;
-- reverse: create index "idx_webhook_deliveries_status_next" to table: "webhook_deliveries"
DROP INDEX `idx_webhook_deliveries_status_next`;
-- reverse: create "webhook_deliveries" table
DROP TABLE `webhook_deliveries`;
-- reverse: create index "idx_webhook_subscriptions_operator_id" to table: "webhook_subscriptions"
DROP INDEX `idx_webhook_subscriptions_operator_id`;
-- reverse: create index "idx_webhook_subscriptions_operator_name" to table: "webhook_subscriptions"
DROP INDEX `idx_webhook_subscriptions_operator_name`;
-- reverse: create index "idx_webhook_subscriptions_enabled" to table: "webhook_subscriptions"
DROP INDEX `idx_webhook_subscriptions_enabled`;
-- reverse: create "webhook_subscriptions" table
DROP TABLE `webhook_subscriptions`;
-- reverse: create index "idx_events_occurred_at" to table: "events"
DROP INDEX `idx_events_occurred_at`;
-- reverse: create index "idx_events_type" to table: "events"
DROP INDEX `idx_events_type`;
-- reverse: create index "idx_events_operator_id" to table: "events"
DROP INDEX `idx_events_operator_id`;
-- reverse: create index "idx_events_resource" to table: "events"
DROP INDEX `idx_events_resource`;
-- reverse: create "events" table
DROP TABLE `events`;
-- reverse: create index "idx_api_tokens_name_per_creator" to table: "api_tokens"
DROP INDEX `idx_api_tokens_name_per_creator`;
-- reverse: create index "idx_api_tokens_token_hash" to table: "api_tokens"
DROP INDEX `idx_api_tokens_token_hash`;
-- reverse: create index "idx_api_tokens_created_by_user_id" to table: "api_tokens"
DROP INDEX `idx_api_tokens_created_by_user_id`;
-- reverse: create index "idx_api_tokens_operator_id" to table: "api_tokens"
DROP INDEX `idx_api_tokens_operator_id`;
-- reverse: create index "idx_api_tokens_account_id" to table: "api_tokens"
DROP INDEX `idx_api_tokens_account_id`;
-- reverse: create index "idx_api_tokens_revoked_at" to table: "api_tokens"
DROP INDEX `idx_api_tokens_revoked_at`;
-- reverse: create "api_tokens" table
DROP TABLE `api_tokens`;
-- reverse: create index "idx_api_users_username" to table: "api_users"
DROP INDEX `idx_api_users_username`;
-- reverse: create index "idx_api_users_operator_id" to table: "api_users"
DROP INDEX `idx_api_users_operator_id`;
-- reverse: create index "idx_api_users_account_id" to table: "api_users"
DROP INDEX `idx_api_users_account_id`;
-- reverse: create "api_users" table
DROP TABLE `api_users`;
-- reverse: create index "idx_clusters_name" to table: "clusters"
DROP INDEX `idx_clusters_name`;
-- reverse: create index "idx_clusters_operator_id" to table: "clusters"
DROP INDEX `idx_clusters_operator_id`;
-- reverse: create "clusters" table
DROP TABLE `clusters`;
-- reverse: create index "idx_users_account_id" to table: "users"
DROP INDEX `idx_users_account_id`;
-- reverse: create index "idx_users_account_name" to table: "users"
DROP INDEX `idx_users_account_name`;
-- reverse: create index "idx_users_public_key" to table: "users"
DROP INDEX `idx_users_public_key`;
-- reverse: create index "idx_users_scoped_signing_key_id" to table: "users"
DROP INDEX `idx_users_scoped_signing_key_id`;
-- reverse: create index "idx_users_jwt_expires_at" to table: "users"
DROP INDEX `idx_users_jwt_expires_at`;
-- reverse: create index "idx_users_revoked_at" to table: "users"
DROP INDEX `idx_users_revoked_at`;
-- reverse: create "users" table
DROP TABLE `users`;
-- reverse: create index "idx_scoped_signing_keys_account_id" to table: "scoped_signing_keys"
DROP INDEX `idx_scoped_signing_keys_account_id`;
-- reverse: create index "idx_scoped_signing_keys_account_name" to table: "scoped_signing_keys"
DROP INDEX `idx_scoped_signing_keys_account_name`;
-- reverse: create index "idx_scoped_signing_keys_public_key" to table: "scoped_signing_keys"
DROP INDEX `idx_scoped_signing_keys_public_key`;
-- reverse: create "scoped_signing_keys" table
DROP TABLE `scoped_signing_keys`;
-- reverse: create index "idx_accounts_operator_id" to table: "accounts"
DROP INDEX `idx_accounts_operator_id`;
-- reverse: create index "idx_accounts_operator_name" to table: "accounts"
DROP INDEX `idx_accounts_operator_name`;
-- reverse: create index "idx_accounts_public_key" to table: "accounts"
DROP INDEX `idx_accounts_public_key`;
-- reverse: create "accounts" table
DROP TABLE `accounts`;
-- reverse: create index "idx_operators_name" to table: "operators"
DROP INDEX `idx_operators_name`;
-- reverse: create index "idx_operators_public_key" to table: "operators"
DROP INDEX `idx_operators_public_key`;
-- reverse: create "operators" table
DROP TABLE `operators`;
