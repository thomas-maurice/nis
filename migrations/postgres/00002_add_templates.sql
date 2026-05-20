-- +goose Up
-- create "templates" table
CREATE TABLE "templates" ("id" text NOT NULL, "operator_id" text NOT NULL, "name" text NOT NULL, "description" text NULL, "latest_version" integer NOT NULL DEFAULT 0, "created_at" timestamp NOT NULL DEFAULT CURRENT_TIMESTAMP, "updated_at" timestamp NOT NULL DEFAULT CURRENT_TIMESTAMP, PRIMARY KEY ("id"), CONSTRAINT "fk_templates_operator" FOREIGN KEY ("operator_id") REFERENCES "operators" ("id") ON UPDATE NO ACTION ON DELETE CASCADE);
-- create index "idx_templates_operator_id" to table: "templates"
CREATE INDEX "idx_templates_operator_id" ON "templates" ("operator_id");
-- create index "idx_templates_operator_name" to table: "templates"
CREATE UNIQUE INDEX "idx_templates_operator_name" ON "templates" ("operator_id", "name");
-- modify "scoped_signing_keys" table
-- Order matters: ADD COLUMN must precede the CHECK that references those
-- columns, otherwise Postgres errors on the constraint with "column does
-- not exist". Atlas reshuffles the ALTER pieces; hand-fixed here.
ALTER TABLE "scoped_signing_keys" ADD COLUMN "template_id" text NULL, ADD COLUMN "template_version" integer NULL, ADD COLUMN "template_drifted" boolean NOT NULL DEFAULT false, ADD CONSTRAINT "fk_scoped_signing_keys_template" FOREIGN KEY ("template_id") REFERENCES "templates" ("id") ON UPDATE NO ACTION ON DELETE SET NULL, ADD CONSTRAINT "chk_scoped_signing_keys_template_pair" CHECK ((template_id IS NULL) = (template_version IS NULL));
-- create index "idx_scoped_signing_keys_template_id" to table: "scoped_signing_keys"
CREATE INDEX "idx_scoped_signing_keys_template_id" ON "scoped_signing_keys" ("template_id");
-- create "template_versions" table
CREATE TABLE "template_versions" ("id" text NOT NULL, "template_id" text NOT NULL, "version_number" integer NOT NULL, "pub_allow" text NULL, "pub_deny" text NULL, "sub_allow" text NULL, "sub_deny" text NULL, "response_max_msgs" integer NOT NULL DEFAULT 0, "response_ttl_seconds" bigint NOT NULL DEFAULT 0, "change_note" text NOT NULL DEFAULT '', "created_at" timestamp NOT NULL DEFAULT CURRENT_TIMESTAMP, "created_by_user_id" text NULL, PRIMARY KEY ("id"), CONSTRAINT "fk_template_versions_created_by_user" FOREIGN KEY ("created_by_user_id") REFERENCES "api_users" ("id") ON UPDATE NO ACTION ON DELETE SET NULL, CONSTRAINT "fk_template_versions_template" FOREIGN KEY ("template_id") REFERENCES "templates" ("id") ON UPDATE NO ACTION ON DELETE CASCADE);
-- create index "idx_template_versions_created_by_user_id" to table: "template_versions"
CREATE INDEX "idx_template_versions_created_by_user_id" ON "template_versions" ("created_by_user_id");
-- create index "idx_template_versions_template_id" to table: "template_versions"
CREATE INDEX "idx_template_versions_template_id" ON "template_versions" ("template_id");
-- create index "idx_template_versions_template_number" to table: "template_versions"
CREATE UNIQUE INDEX "idx_template_versions_template_number" ON "template_versions" ("template_id", "version_number");

-- +goose Down
-- reverse: create index "idx_template_versions_template_number" to table: "template_versions"
DROP INDEX "idx_template_versions_template_number";
-- reverse: create index "idx_template_versions_template_id" to table: "template_versions"
DROP INDEX "idx_template_versions_template_id";
-- reverse: create index "idx_template_versions_created_by_user_id" to table: "template_versions"
DROP INDEX "idx_template_versions_created_by_user_id";
-- reverse: create "template_versions" table
DROP TABLE "template_versions";
-- reverse: create index "idx_scoped_signing_keys_template_id" to table: "scoped_signing_keys"
DROP INDEX "idx_scoped_signing_keys_template_id";
-- reverse: modify "scoped_signing_keys" table
-- Drop the CHECK before the columns it references; mirrors the Up
-- ordering fix above.
ALTER TABLE "scoped_signing_keys" DROP CONSTRAINT "chk_scoped_signing_keys_template_pair", DROP CONSTRAINT "fk_scoped_signing_keys_template", DROP COLUMN "template_drifted", DROP COLUMN "template_version", DROP COLUMN "template_id";
-- reverse: create index "idx_templates_operator_name" to table: "templates"
DROP INDEX "idx_templates_operator_name";
-- reverse: create index "idx_templates_operator_id" to table: "templates"
DROP INDEX "idx_templates_operator_id";
-- reverse: create "templates" table
DROP TABLE "templates";
