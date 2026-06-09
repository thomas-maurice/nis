# NIS Ansible Role

Ansible role to deploy a complete NATS Identity Service (NIS) instance with Docker
containers (NIS + optional PostgreSQL + optional NATS).

## How configuration works

The role renders the **entire** NIS configuration to a file from
[`templates/config.yaml.j2`](templates/config.yaml.j2), writes it to
`{{ nis_config_dir }}/config.yaml` on the host (mode `0600`, owned by uid/gid
`1000` — the in-container `nis` user), mounts it read-only at `/app/config.yaml`,
and starts the server with `serve --config /app/config.yaml`.

Every NIS config key is exposed as a `nis_*` variable. The authoritative,
fully-commented list of variables and their defaults lives in
[`defaults/main.yml`](defaults/main.yml) — read it; this README only highlights
the ones you must set and the non-obvious ones. Override variables via group/host
vars (use `ansible-vault` for the secrets).

Changing any `nis_*` config variable re-renders the file and triggers the
`Restart NIS` handler, which is flushed before the health-wait/admin steps so the
server comes back up on the new config within the same play.

## Requirements

- Docker installed on target host
- `community.docker` Ansible collection

## Variables you MUST set (secrets)

Put these in an `ansible-vault`'d file — never commit real values.

| Variable | Description |
|----------|-------------|
| `nis_jwt_secret` | JWT signing secret, min 32 bytes. `openssl rand -base64 32` |
| `nis_encryption_key` | At-rest encryption key, **exactly 32 raw bytes** (see below) |
| `nis_db_password` | PostgreSQL password (postgres driver only) |
| `nis_admin_password` | Bootstrap admin password |

### Encryption key (read carefully)

`nis_encryption_key` encrypts private NKey seeds at rest (ChaCha20-Poly1305). The
single-key form requires a value that is **exactly 32 bytes long** — NIS
base64-encodes it internally, so do **not** pre-base64 it. A `openssl rand -base64 32`
string is 44 chars and will fail startup with "encryption key must be exactly 32
bytes".

```bash
openssl rand -base64 24    # 24 random bytes -> 32 chars
openssl rand -hex 16       # 16 random bytes -> 32 chars
```

#### Key rotation (multi-key form)

To support rotation, define `nis_encryption_keys` as a non-empty list instead.
Each `key` here **is** base64 of 32 random bytes (`openssl rand -base64 32`). When
the list is non-empty NIS uses it and ignores `nis_encryption_key`:

```yaml
nis_encryption_current_key_id: "key-2025-01"
nis_encryption_keys:
  - id: "key-2025-01"
    key: "{{ vault_nis_key_2025_01 }}"   # base64 of 32 bytes
  - id: "key-2024-12"                      # old key, kept so old data decrypts
    key: "{{ vault_nis_key_2024_12 }}"
```

## Common variables

### Server / database

| Variable | Default | Description |
|----------|---------|-------------|
| `nis_image` / `nis_image_tag` | `mauricethomas/nis` / `latest` | NIS image |
| `nis_port` | `8080` | Host port for the NIS API/UI |
| `nis_data_dir` | `/opt/nis/data` | Persisted data dir (mounted at `/data`) |
| `nis_config_dir` | `/opt/nis/config` | Where the rendered `config.yaml` is written |
| `nis_public_url` | `""` | External base URL — **required for OIDC SSO** |
| `nis_enable_ui` | `true` | Serve the embedded web UI |
| `nis_db_driver` | `postgres` | `postgres` or `sqlite` |
| `nis_db_*` | — | Postgres host/port/name/user/password/sslmode |
| `nis_sqlite_path` | `/data/nis.db` | SQLite DB path (sqlite driver only) |
| `nis_auto_migrate` | `false` | Apply migrations on startup (dev only) |

With `nis_db_driver: sqlite` the PostgreSQL sidecar container is not started.

### Optional NATS sidecar

| Variable | Default | Description |
|----------|---------|-------------|
| `nis_nats_enabled` | `false` | Deploy a NATS container alongside NIS |
| `nis_nats_image_tag` | `2.10-alpine` | NATS image tag |
| `nis_nats_client_port` / `nis_nats_monitoring_port` | `4222` / `8222` | Ports |

### SSO / backups / tuning

These are all `nis_*`-prefixed and documented inline in
[`defaults/main.yml`](defaults/main.yml). Highlights:

- **OIDC SSO:** set `nis_public_url`; optionally `nis_sso_default_org` for a
  single-org slug-less login button.
- **Scheduled backups:** `nis_backups_enabled: true` plus the `nis_backups_s3_*`
  S3/MinIO endpoint, bucket and credentials.
- **Retention / sweeps / leases:** `nis_jobs_*`, `nis_events_*`,
  `nis_revocations_*`, `nis_webhooks_*`, `nis_jwt_policy_*`.
- **Observability:** `nis_metrics_*`, `nis_tracing_*`, `nis_log_level`.

## Example Playbook

```yaml
---
- hosts: nis_servers
  become: true
  roles:
    - role: nis
      vars:
        nis_public_url: "https://nis.example.com"
        nis_db_password: "{{ vault_nis_db_password }}"
        nis_jwt_secret: "{{ vault_nis_jwt_secret }}"
        nis_encryption_key: "{{ vault_nis_encryption_key }}"
        nis_admin_password: "{{ vault_nis_admin_password }}"
        # Enable scheduled backups to MinIO
        nis_backups_enabled: true
        nis_backups_s3_endpoint: "http://minio:9000"
        nis_backups_s3_bucket: "nis-backups"
        nis_backups_s3_access_key_id: "{{ vault_minio_access_key }}"
        nis_backups_s3_secret_access_key: "{{ vault_minio_secret_key }}"
```

## Post-Installation

After deployment, configure NIS with an operator. The bootstrap `admin` user is a
**platform admin** and is not bound to any organization, so operator and cluster
commands must name the target organization with `--org`. Use the built-in default
organization (seeded on first migration) unless you have created another:

```bash
# Default organization ID (seeded on first migration)
ORG=00000000-0000-0000-0000-000000000001

# Login to NIS
docker exec nis-server ./nisctl login http://localhost:8080 --username admin --password admin123

# Create operator
docker exec nis-server ./nisctl operator create my-operator --org "$ORG" --description "My NATS operator"

# Generate NATS config with JWT auth
docker exec nis-server ./nisctl operator generate-include my-operator --org "$ORG" > /opt/nis/nats/nats-server.conf

# Restart NATS to load JWT auth
docker restart nis-nats

# Register cluster
docker exec nis-server ./nisctl cluster create my-cluster --operator my-operator --org "$ORG" --urls nats://nis-nats:4222
```

## License

MIT
