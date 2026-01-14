# NIS Ansible Role

Ansible role to deploy NATS Identity Service (NIS) with PostgreSQL backend using Docker containers.

## Requirements

- Docker installed on target host
- `community.docker` Ansible collection

## Role Variables

### NIS Server

| Variable | Default | Description |
|----------|---------|-------------|
| `nis_image` | `mauricethomas/nis` | NIS Docker image |
| `nis_image_tag` | `latest` | NIS image tag |
| `nis_container_name` | `nis-server` | NIS container name |
| `nis_port` | `8080` | Host port for NIS API/UI |
| `nis_data_dir` | `/opt/nis/data` | Data directory on host |

### PostgreSQL

| Variable | Default | Description |
|----------|---------|-------------|
| `nis_postgres_image` | `postgres` | PostgreSQL image |
| `nis_postgres_image_tag` | `16-alpine` | PostgreSQL image tag |
| `nis_postgres_container_name` | `nis-postgres` | PostgreSQL container name |
| `nis_postgres_data_dir` | `/opt/nis/postgres` | PostgreSQL data directory |
| `nis_db_name` | `nis` | Database name |
| `nis_db_user` | `nis` | Database user |
| `nis_db_password` | `changeme` | Database password |

### NATS Server

| Variable | Default | Description |
|----------|---------|-------------|
| `nis_nats_enabled` | `true` | Deploy NATS server |
| `nis_nats_image` | `nats` | NATS image |
| `nis_nats_image_tag` | `2.10-alpine` | NATS image tag |
| `nis_nats_container_name` | `nis-nats` | NATS container name |
| `nis_nats_client_port` | `4222` | NATS client port |
| `nis_nats_monitoring_port` | `8222` | NATS monitoring port |
| `nis_nats_data_dir` | `/opt/nis/nats` | NATS data directory |

### Security

| Variable | Default | Description |
|----------|---------|-------------|
| `nis_jwt_secret` | `change-this-...` | JWT signing secret (min 32 bytes) |
| `nis_encryption_key` | `123456...` | Encryption key (exactly 32 bytes) - see below |
| `nis_admin_user` | `admin` | Admin username |
| `nis_admin_password` | `admin123` | Admin password |
| `nis_create_admin_user` | `true` | Create admin user on deploy |

#### Encryption Key

The `nis_encryption_key` is used to encrypt private keys stored in the database using ChaCha20-Poly1305.

**Requirements:**
- Must be **exactly 32 bytes** (256 bits)
- Use only printable ASCII characters

**Generate a secure key:**
```bash
# Option 1: Using openssl
openssl rand -hex 16 | tr -d '\n'

# Option 2: Using /dev/urandom
head -c 32 /dev/urandom | base64 | head -c 32

# Option 3: Using python
python3 -c "import secrets; print(secrets.token_urlsafe(24)[:32])"
```

### Docker

| Variable | Default | Description |
|----------|---------|-------------|
| `nis_docker_network` | `""` (default bridge) | Docker network name - empty uses default bridge with container links |
| `nis_restart_policy` | `unless-stopped` | Container restart policy |

## Example Playbook

```yaml
---
- hosts: nis_servers
  become: true
  roles:
    - role: nis
      vars:
        nis_db_password: "{{ vault_nis_db_password }}"
        nis_jwt_secret: "{{ vault_nis_jwt_secret }}"
        nis_encryption_key: "{{ vault_nis_encryption_key }}"
        nis_admin_password: "{{ vault_nis_admin_password }}"
```

## Post-Installation

After deployment, configure NIS with an operator:

```bash
# Login to NIS
docker exec nis-server ./nisctl login http://localhost:8080 --username admin --password admin123

# Create operator
docker exec nis-server ./nisctl operator create my-operator --description "My NATS operator"

# Generate NATS config with JWT auth
docker exec nis-server ./nisctl operator generate-include my-operator > /opt/nis/nats/nats-server.conf

# Restart NATS to load JWT auth
docker restart nis-nats

# Register cluster
docker exec nis-server ./nisctl cluster create my-cluster --operator my-operator --urls nats://nis-nats:4222
```

## License

MIT
