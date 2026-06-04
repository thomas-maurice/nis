-- +goose Up
-- drop index "idx_operators_name" from table: "operators"
DROP INDEX `idx_operators_name`;
-- create index "idx_operators_org_name" to table: "operators"
CREATE UNIQUE INDEX `idx_operators_org_name` ON `operators` (`organization_id`, `name`);

-- +goose Down
-- reverse: create index "idx_operators_org_name" to table: "operators"
DROP INDEX `idx_operators_org_name`;
-- reverse: drop index "idx_operators_name" from table: "operators"
CREATE UNIQUE INDEX `idx_operators_name` ON `operators` (`name`);
