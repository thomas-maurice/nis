-- +goose Up
-- create "jobs" table
CREATE TABLE `jobs` (`id` text NOT NULL, `type` text NOT NULL, `payload` text NOT NULL DEFAULT '{}', `status` text NOT NULL, `scheduled_for` timestamp NOT NULL, `locked_by` text NOT NULL DEFAULT '', `locked_until` timestamp NULL, `attempts` integer NOT NULL DEFAULT 0, `max_attempts` integer NOT NULL, `last_error` text NOT NULL DEFAULT '', `dedup_key` text NULL, `created_at` timestamp NOT NULL DEFAULT (CURRENT_TIMESTAMP), `updated_at` timestamp NOT NULL DEFAULT (CURRENT_TIMESTAMP), `started_at` timestamp NULL, `completed_at` timestamp NULL, PRIMARY KEY (`id`));
-- create index "idx_jobs_status_scheduled" to table: "jobs"
CREATE INDEX `idx_jobs_status_scheduled` ON `jobs` (`status`, `scheduled_for`);
-- create index "idx_jobs_type_status" to table: "jobs"
CREATE INDEX `idx_jobs_type_status` ON `jobs` (`type`, `status`);
-- hand-added partial unique index: prevents two enqueues for the same
-- (type, dedup_key) while one is pending or running. NULL dedup_key
-- intentionally allows multiple rows (SQL UNIQUE treats NULLs as
-- distinct) — that's the "no dedup" path. Cannot be expressed via
-- atlas-provider-gorm tags; if you regenerate, re-apply by hand.
CREATE UNIQUE INDEX `idx_jobs_dedup_active` ON `jobs` (`type`, `dedup_key`) WHERE `status` IN ('pending', 'running');

-- +goose Down
-- reverse: hand-added partial unique index
DROP INDEX `idx_jobs_dedup_active`;
-- reverse: create index "idx_jobs_type_status" to table: "jobs"
DROP INDEX `idx_jobs_type_status`;
-- reverse: create index "idx_jobs_status_scheduled" to table: "jobs"
DROP INDEX `idx_jobs_status_scheduled`;
-- reverse: create "jobs" table
DROP TABLE `jobs`;
