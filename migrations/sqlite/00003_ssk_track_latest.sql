-- +goose Up
-- add column "track_latest" to table: "scoped_signing_keys"
ALTER TABLE `scoped_signing_keys` ADD COLUMN `track_latest` boolean NOT NULL DEFAULT false;

-- +goose Down
-- reverse: add column "track_latest" to table: "scoped_signing_keys"
ALTER TABLE `scoped_signing_keys` DROP COLUMN `track_latest`;
