# Manifest examples

Each file in this directory is a valid `nis/v1` manifest. They can be applied
directly against a running NIS server with `nisctl apply`.

## Files

| File | What it declares |
|---|---|
| `operator.yaml` | A single Operator with a JWT lifecycle policy |
| `cluster.yaml` | A single Cluster pointing at three NATS server URLs |
| `account.yaml` | A single Account with JetStream enabled and resource limits |
| `scoped-signing-key.yaml` | A ScopedSigningKey that restricts pub/sub to the `payments.*` hierarchy |
| `user.yaml` | A User pinned to a ScopedSigningKey with a per-user JWT TTL |
| `full-stack.yaml` | Multi-doc: all five kinds, two accounts, a coherent ACME production tree |

## How to apply

```bash
# Preview changes without applying them
nisctl diff -f full-stack.yaml

# Apply, with confirmation prompt
nisctl apply -f full-stack.yaml

# Apply without confirmation (CI/scripting)
nisctl apply -f full-stack.yaml -y

# Delete everything declared in the file (reverse topo order)
nisctl delete -f full-stack.yaml
```

## How to capture existing state as a manifest

```bash
# Dump an entire operator tree to stdout
nisctl dump operator acme-prod

# Save to a file, then diff and apply
nisctl dump operator acme-prod > acme-prod.yaml
# ... edit acme-prod.yaml ...
nisctl diff  -f acme-prod.yaml
nisctl apply -f acme-prod.yaml
```

`dump` excludes the `$SYS` account, the `system` user, but includes the `default`
ScopedSigningKey so its permissions can be round-tripped. Use `--kinds` to
restrict what is emitted (e.g. `--kinds=Account,User`).

## Reserved names

The following names cannot be declared or deleted via manifest:

| Kind | Reserved name | Why |
|---|---|---|
| Account | `$SYS` | NATS system account, auto-created per operator |
| User | `system` | NATS system user, auto-created per operator |
| ScopedSigningKey | `default` | Auto-created per account; may be declared to update permissions, but cannot be deleted |

## v1 limitations

- Cluster spec fields cannot be updated via manifest. Use `nisctl cluster update` or the UI.
- User `scopedKey` reassignment via apply is not supported — changing the field would
  silently invalidate existing `.creds` files. Delete and recreate the user instead.
