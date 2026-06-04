-- +goose Up
-- HAND-EDITED: data backfill steps injected after the DDL changes.
-- See ORGS_SSO.md §5.1 for the NOT-NULL backfill strategy.

-- Step 1: Create organizations table (must exist before any FK additions).
-- create "organizations" table
CREATE TABLE "organizations" ("id" text NOT NULL, "name" text NOT NULL, "slug" text NOT NULL, "description" text NULL, "created_at" timestamp NOT NULL DEFAULT CURRENT_TIMESTAMP, "updated_at" timestamp NOT NULL DEFAULT CURRENT_TIMESTAMP, PRIMARY KEY ("id"));
-- create index "idx_organizations_name" to table: "organizations"
CREATE UNIQUE INDEX "idx_organizations_name" ON "organizations" ("name");
-- create index "idx_organizations_slug" to table: "organizations"
CREATE UNIQUE INDEX "idx_organizations_slug" ON "organizations" ("slug");

-- Step 2: Add org column to api_tokens (nullable; backfilled below).
-- modify "api_tokens" table
ALTER TABLE "api_tokens" ADD COLUMN "organization_id" text NULL, ADD CONSTRAINT "fk_api_tokens_organization" FOREIGN KEY ("organization_id") REFERENCES "organizations" ("id") ON UPDATE NO ACTION ON DELETE CASCADE;
-- create index "idx_api_tokens_org_id" to table: "api_tokens"
CREATE INDEX "idx_api_tokens_org_id" ON "api_tokens" ("organization_id");

-- Step 3: Add org + OIDC columns to api_users (nullable; backfilled below).
-- modify "api_users" table
ALTER TABLE "api_users" ADD COLUMN "organization_id" text NULL, ADD COLUMN "auth_source" text NOT NULL DEFAULT 'local', ADD COLUMN "external_subject" text NULL, ADD COLUMN "email" text NULL, ADD CONSTRAINT "fk_api_users_organization" FOREIGN KEY ("organization_id") REFERENCES "organizations" ("id") ON UPDATE NO ACTION ON DELETE CASCADE;
-- Add CHECK constraint separately (after nullable column exists, so no existing rows fail it).
ALTER TABLE "api_users" ADD CONSTRAINT "chk_api_users_oidc_invariant" CHECK ((auth_source <> 'oidc') OR ((external_subject IS NOT NULL) AND (organization_id IS NOT NULL)));
-- create index "idx_api_users_oidc_subject" (partial, hand-added — see tools/atlas/loader.go manualExtras)
CREATE UNIQUE INDEX "idx_api_users_oidc_subject" ON "api_users" ("organization_id", "external_subject") WHERE ("auth_source" = 'oidc');
-- create index "idx_api_users_org_id" to table: "api_users"
CREATE INDEX "idx_api_users_org_id" ON "api_users" ("organization_id");

-- Step 4: Create SSO helper tables (after organizations exists).
-- create "oidc_login_states" table
CREATE TABLE "oidc_login_states" ("state" text NOT NULL, "organization_id" text NOT NULL, "nonce" text NOT NULL, "pkce_verifier" text NOT NULL, "redirect_after" text NULL, "created_at" timestamp NOT NULL DEFAULT CURRENT_TIMESTAMP, "expires_at" timestamp NOT NULL, PRIMARY KEY ("state"), CONSTRAINT "fk_oidc_login_states_organization" FOREIGN KEY ("organization_id") REFERENCES "organizations" ("id") ON UPDATE NO ACTION ON DELETE CASCADE);
-- create index "idx_oidc_login_states_expires_at" to table: "oidc_login_states"
CREATE INDEX "idx_oidc_login_states_expires_at" ON "oidc_login_states" ("expires_at");
-- create index "idx_oidc_login_states_org_id" to table: "oidc_login_states"
CREATE INDEX "idx_oidc_login_states_org_id" ON "oidc_login_states" ("organization_id");

-- Step 5: Insert the fixed-UUID default organization.
-- The UUID '00000000-0000-0000-0000-000000000001' is the constant
-- entities.DefaultOrganizationID in internal/domain/entities/organization.go.
INSERT INTO "organizations" ("id", "name", "slug", "description", "created_at", "updated_at")
VALUES ('00000000-0000-0000-0000-000000000001', 'default', 'default', 'Default organization (pre-existing data)', NOW(), NOW());

-- Step 6: Data backfill for api_users.
-- Non-admin users get the default org. Platform admins (role='admin') stay NULL.
UPDATE "api_users" SET "organization_id" = '00000000-0000-0000-0000-000000000001'
WHERE "role" <> 'admin';

-- Step 7: Data backfill for api_tokens.
-- Tokens created by org-scoped users get the default org.
UPDATE "api_tokens" SET "organization_id" = '00000000-0000-0000-0000-000000000001'
WHERE "created_by_user_id" IN (
    SELECT "id" FROM "api_users" WHERE "role" <> 'admin'
);

-- Step 8: Add organization_id to operators (NOT NULL; all existing rows backfilled via DEFAULT).
-- Postgres can add a NOT NULL column with a DEFAULT inline, which avoids a
-- table rebuild. The DEFAULT is removed after backfill to enforce the
-- app-layer invariant that CreateOperator always supplies the value.
ALTER TABLE "operators" ADD COLUMN "organization_id" text NOT NULL DEFAULT '00000000-0000-0000-0000-000000000001', ADD CONSTRAINT "fk_operators_organization" FOREIGN KEY ("organization_id") REFERENCES "organizations" ("id") ON UPDATE NO ACTION ON DELETE CASCADE;
-- Remove the default now that all rows are populated; app layer always provides the value.
ALTER TABLE "operators" ALTER COLUMN "organization_id" DROP DEFAULT;
-- create index "idx_operators_org_id" to table: "operators"
CREATE INDEX "idx_operators_org_id" ON "operators" ("organization_id");

-- Step 9: SSO config and mapping tables (after operators/accounts exist for FK).
-- create "organization_sso_configs" table
CREATE TABLE "organization_sso_configs" ("id" text NOT NULL, "organization_id" text NOT NULL, "enabled" boolean NOT NULL DEFAULT false, "issuer_url" text NOT NULL DEFAULT '', "client_id" text NOT NULL DEFAULT '', "encrypted_client_secret" text NOT NULL DEFAULT '', "scopes" text NOT NULL, "group_claim" text NOT NULL, "default_role" text NULL, "created_at" timestamp NOT NULL DEFAULT CURRENT_TIMESTAMP, "updated_at" timestamp NOT NULL DEFAULT CURRENT_TIMESTAMP, PRIMARY KEY ("id"), CONSTRAINT "fk_organization_sso_configs_organization" FOREIGN KEY ("organization_id") REFERENCES "organizations" ("id") ON UPDATE NO ACTION ON DELETE CASCADE);
-- create index "idx_org_sso_configs_org_id" to table: "organization_sso_configs"
CREATE UNIQUE INDEX "idx_org_sso_configs_org_id" ON "organization_sso_configs" ("organization_id");
-- create index "idx_org_sso_configs_org_id_fk" to table: "organization_sso_configs"
CREATE INDEX "idx_org_sso_configs_org_id_fk" ON "organization_sso_configs" ("organization_id");
-- create "organization_sso_role_mappings" table
CREATE TABLE "organization_sso_role_mappings" ("id" text NOT NULL, "organization_id" text NOT NULL, "group_value" text NOT NULL, "role" text NOT NULL, "scope_operator_id" text NULL, "scope_account_id" text NULL, "priority" integer NOT NULL DEFAULT 0, "created_at" timestamp NOT NULL DEFAULT CURRENT_TIMESTAMP, PRIMARY KEY ("id"), CONSTRAINT "fk_organization_sso_role_mappings_organization" FOREIGN KEY ("organization_id") REFERENCES "organizations" ("id") ON UPDATE NO ACTION ON DELETE CASCADE, CONSTRAINT "fk_organization_sso_role_mappings_scope_account" FOREIGN KEY ("scope_account_id") REFERENCES "accounts" ("id") ON UPDATE NO ACTION ON DELETE SET NULL, CONSTRAINT "fk_organization_sso_role_mappings_scope_operator" FOREIGN KEY ("scope_operator_id") REFERENCES "operators" ("id") ON UPDATE NO ACTION ON DELETE SET NULL);
-- create index "idx_sso_role_mappings_org_group" to table: "organization_sso_role_mappings"
CREATE UNIQUE INDEX "idx_sso_role_mappings_org_group" ON "organization_sso_role_mappings" ("organization_id", "group_value");
-- create index "idx_sso_role_mappings_org_id" to table: "organization_sso_role_mappings"
CREATE INDEX "idx_sso_role_mappings_org_id" ON "organization_sso_role_mappings" ("organization_id");
-- create index "idx_sso_role_mappings_scope_acc_id" to table: "organization_sso_role_mappings"
CREATE INDEX "idx_sso_role_mappings_scope_acc_id" ON "organization_sso_role_mappings" ("scope_account_id");
-- create index "idx_sso_role_mappings_scope_op_id" to table: "organization_sso_role_mappings"
CREATE INDEX "idx_sso_role_mappings_scope_op_id" ON "organization_sso_role_mappings" ("scope_operator_id");

-- +goose Down
-- Reverse: drop new tables, remove columns from altered tables.
-- Data backfilled into organization_id columns is permanently lost on rollback.

-- Drop SSO mapping and config tables
DROP INDEX IF EXISTS "idx_sso_role_mappings_scope_op_id";
DROP INDEX IF EXISTS "idx_sso_role_mappings_scope_acc_id";
DROP INDEX IF EXISTS "idx_sso_role_mappings_org_id";
DROP INDEX IF EXISTS "idx_sso_role_mappings_org_group";
DROP TABLE IF EXISTS "organization_sso_role_mappings";
DROP INDEX IF EXISTS "idx_org_sso_configs_org_id_fk";
DROP INDEX IF EXISTS "idx_org_sso_configs_org_id";
DROP TABLE IF EXISTS "organization_sso_configs";

-- Remove organization_id from operators
DROP INDEX IF EXISTS "idx_operators_org_id";
ALTER TABLE "operators" DROP CONSTRAINT IF EXISTS "fk_operators_organization", DROP COLUMN IF EXISTS "organization_id";

-- Drop oidc_login_states
DROP INDEX IF EXISTS "idx_oidc_login_states_org_id";
DROP INDEX IF EXISTS "idx_oidc_login_states_expires_at";
DROP TABLE IF EXISTS "oidc_login_states";

-- Remove org + OIDC columns from api_users
DROP INDEX IF EXISTS "idx_api_users_org_id";
DROP INDEX IF EXISTS "idx_api_users_oidc_subject";
ALTER TABLE "api_users" DROP CONSTRAINT IF EXISTS "fk_api_users_organization", DROP CONSTRAINT IF EXISTS "chk_api_users_oidc_invariant", DROP COLUMN IF EXISTS "email", DROP COLUMN IF EXISTS "external_subject", DROP COLUMN IF EXISTS "auth_source", DROP COLUMN IF EXISTS "organization_id";

-- Remove organization_id from api_tokens
DROP INDEX IF EXISTS "idx_api_tokens_org_id";
ALTER TABLE "api_tokens" DROP CONSTRAINT IF EXISTS "fk_api_tokens_organization", DROP COLUMN IF EXISTS "organization_id";

-- Drop organizations table
DROP INDEX IF EXISTS "idx_organizations_slug";
DROP INDEX IF EXISTS "idx_organizations_name";
DROP TABLE IF EXISTS "organizations";
