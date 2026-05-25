-- +goose Up
-- modify "events" table
ALTER TABLE "events" ADD COLUMN "diff" text NULL;

-- +goose Down
-- reverse: modify "events" table
ALTER TABLE "events" DROP COLUMN "diff";
