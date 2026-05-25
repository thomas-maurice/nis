-- +goose Up
-- add column "diff" to table: "events"
ALTER TABLE `events` ADD COLUMN `diff` text NULL;

-- +goose Down
-- reverse: add column "diff" to table: "events"
ALTER TABLE `events` DROP COLUMN `diff`;
