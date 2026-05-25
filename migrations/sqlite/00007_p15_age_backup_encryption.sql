-- +goose Up
-- P15 (2026-05-25) — scheduled-backup artifacts are encrypted with age before
-- S3 upload. Pre-P15 (P12 era) rows in operator_backups refer to plaintext
-- objects that the new restore path (age-decrypt → ImportOperator) cannot
-- service. There is intentionally NO compatibility layer (clean break, per
-- proposal review). The DELETE below drops the DB index of those rows.
--
-- The S3 objects themselves are NOT removed by this migration — NIS does
-- not own the operator's bucket. Bucket lifecycle policies, or a manual
-- `aws s3 rm`, are the operator's responsibility post-upgrade.
--
-- Operators currently running scheduled backups should expect their next
-- sweep to fail with `operator.backup.failed` reason="no_recipients_configured"
-- until they add at least one age recipient via:
--   nisctl operator backup add-recipient OPERATOR --pubkey age1...
--
-- Pre-P15 events referencing the deleted backup IDs (operator.backup.succeeded
-- / .deleted) will linger in the events table until events retention sweeps
-- them (default 30d). The dangling ResourceID strings will not resolve to a
-- live backup row; this is acceptable audit-trail noise post-upgrade.
DELETE FROM `operator_backups`;

-- create "operator_age_recipients" table
CREATE TABLE `operator_age_recipients` (`id` text NOT NULL, `operator_id` text NOT NULL, `public_key` text NOT NULL, `label` text NOT NULL DEFAULT '', `created_at` timestamp NOT NULL DEFAULT (CURRENT_TIMESTAMP), `created_by_user_id` text NULL, PRIMARY KEY (`id`), CONSTRAINT `fk_operator_age_recipients_created_by` FOREIGN KEY (`created_by_user_id`) REFERENCES `api_users` (`id`) ON UPDATE NO ACTION ON DELETE SET NULL, CONSTRAINT `fk_operator_age_recipients_operator` FOREIGN KEY (`operator_id`) REFERENCES `operators` (`id`) ON UPDATE NO ACTION ON DELETE CASCADE);
-- create index "idx_operator_age_recipients_operator_id" to table: "operator_age_recipients"
CREATE INDEX `idx_operator_age_recipients_operator_id` ON `operator_age_recipients` (`operator_id`);
-- create index "idx_operator_age_recipients_op_key" to table: "operator_age_recipients"
CREATE UNIQUE INDEX `idx_operator_age_recipients_op_key` ON `operator_age_recipients` (`operator_id`, `public_key`);

-- +goose Down
-- reverse: create index "idx_operator_age_recipients_op_key" to table: "operator_age_recipients"
DROP INDEX `idx_operator_age_recipients_op_key`;
-- reverse: create index "idx_operator_age_recipients_operator_id" to table: "operator_age_recipients"
DROP INDEX `idx_operator_age_recipients_operator_id`;
-- reverse: create "operator_age_recipients" table
DROP TABLE `operator_age_recipients`;
-- The Up's DELETE FROM operator_backups is intentionally one-way; the rows
-- it removed referred to plaintext-encoded S3 objects that the post-P15
-- code cannot service anyway. Down does NOT re-populate.
