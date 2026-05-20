-- +goose Up
-- add column "is_plain_signer" to table: "scoped_signing_keys"
ALTER TABLE `scoped_signing_keys` ADD COLUMN `is_plain_signer` boolean NOT NULL DEFAULT false;
-- Backfill: every SKK that was imported from NSC carries a stable
-- description string. Mark those as plain signers so the system user
-- minted against them stops getting SetScoped'd into a subs:0 lockout
-- (the cluster healthcheck on imported operators silently timed out
-- on CLAIMS.LIST + JWT push hit "maximum payload exceeded" until this
-- flag was introduced).
UPDATE `scoped_signing_keys`
   SET `is_plain_signer` = 1
 WHERE `description` = 'Scoped signing key imported from NSC';

-- +goose Down
-- reverse: add column "is_plain_signer" to table: "scoped_signing_keys"
ALTER TABLE `scoped_signing_keys` DROP COLUMN `is_plain_signer`;
