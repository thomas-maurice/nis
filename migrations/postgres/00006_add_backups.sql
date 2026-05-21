-- +goose Up
-- modify "operators" table
ALTER TABLE "operators" ADD CONSTRAINT "chk_operators_backup_interval_seconds" CHECK ((backup_interval_seconds IS NULL) OR (backup_interval_seconds >= 3600)), ADD COLUMN "backup_enabled" boolean NOT NULL DEFAULT false, ADD COLUMN "backup_interval_seconds" bigint NULL, ADD COLUMN "backup_retention_count" bigint NULL, ADD COLUMN "last_backup_at" timestamp NULL;
-- create "operator_backups" table
CREATE TABLE "operator_backups" ("id" text NOT NULL, "operator_id" text NOT NULL, "object_key" text NOT NULL, "size_bytes" bigint NOT NULL, "sha256" text NOT NULL, "trigger_kind" text NOT NULL, "created_at" timestamp NOT NULL DEFAULT CURRENT_TIMESTAMP, PRIMARY KEY ("id"), CONSTRAINT "fk_operator_backups_operator" FOREIGN KEY ("operator_id") REFERENCES "operators" ("id") ON UPDATE NO ACTION ON DELETE CASCADE);
-- create index "idx_operator_backups_object_key" to table: "operator_backups"
CREATE UNIQUE INDEX "idx_operator_backups_object_key" ON "operator_backups" ("object_key");
-- create index "idx_operator_backups_operator_id" to table: "operator_backups"
CREATE INDEX "idx_operator_backups_operator_id" ON "operator_backups" ("operator_id");

-- +goose Down
-- reverse: create index "idx_operator_backups_operator_id" to table: "operator_backups"
DROP INDEX "idx_operator_backups_operator_id";
-- reverse: create index "idx_operator_backups_object_key" to table: "operator_backups"
DROP INDEX "idx_operator_backups_object_key";
-- reverse: create "operator_backups" table
DROP TABLE "operator_backups";
-- reverse: modify "operators" table
ALTER TABLE "operators" DROP COLUMN "last_backup_at", DROP COLUMN "backup_retention_count", DROP COLUMN "backup_interval_seconds", DROP COLUMN "backup_enabled", DROP CONSTRAINT "chk_operators_backup_interval_seconds";
