-- +goose Up
-- modify "scoped_signing_keys" table
ALTER TABLE "scoped_signing_keys" ADD COLUMN "track_latest" boolean NOT NULL DEFAULT false;

-- +goose Down
-- reverse: modify "scoped_signing_keys" table
ALTER TABLE "scoped_signing_keys" DROP COLUMN "track_latest";
