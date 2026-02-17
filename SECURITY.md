# Security Documentation

This document describes the security architecture, mechanisms, and best practices for the NATS Identity Service (NIS).

## Security Model

### What NIS Protects

NIS is a centralized credential management system for NATS. It generates, stores, and manages the following sensitive material:

- **NKey private keys** (Ed25519) -- Operator, account, and user signing keys used to produce NATS JWTs.
- **JWT signing keys** -- The keys that sign operator, account, and user JWTs. Compromise of an operator signing key would allow an attacker to issue arbitrary account and user credentials.
- **Scoped signing keys** -- Delegated keys that can issue user JWTs with specific permission constraints.
- **Cluster credentials** -- System user credentials used to push JWTs to NATS clusters via `$SYS.REQ.CLAIMS.UPDATE`.

### Trust Boundaries

```
Internet
  |
  v
[Reverse proxy (TLS termination)]
  |
  v
[NIS API server]  --->  [Database (SQLite / PostgreSQL)]
  |                        (encrypted keys at rest)
  v
[NATS Cluster]
  (receives signed JWTs)
```

1. **API boundary** -- All API access requires JWT authentication. Role-based access control (RBAC) restricts what each authenticated user can do.
2. **Database boundary** -- All private key material is encrypted before being written to the database. Even with direct database access, an attacker cannot recover private keys without the encryption key.
3. **NATS boundary** -- NIS connects to NATS clusters using system account credentials to push signed JWTs. These credentials should be scoped to `$SYS` subjects only.

## Encryption at Rest

### Algorithm

NIS uses **ChaCha20-Poly1305** (RFC 8439) for authenticated encryption of all private key material. This is an AEAD cipher that provides both confidentiality and integrity.

- **Key size**: 256 bits (32 bytes)
- **Nonce size**: 96 bits (12 bytes), randomly generated per encryption operation
- **Authentication tag**: 128 bits (16 bytes), appended to ciphertext

### Storage Format

Encrypted values are stored as a structured reference string:

```
encrypted:<key-id>:<base64-encoded-nonce-and-ciphertext>
```

The key ID is embedded in the storage reference, allowing the system to select the correct decryption key even after key rotation.

### Key Management

Encryption keys are provided in the configuration file as base64-encoded 32-byte values:

```yaml
encryption:
  current_key_id: "key-2025-01"
  keys:
    - id: "key-2025-01"
      key: "<base64-encoded-32-byte-key>"
```

Generate a key with:

```bash
openssl rand -base64 32
```

### Key Rotation

NIS supports seamless encryption key rotation without downtime:

1. Generate a new encryption key.
2. Add the new key to the `keys` list with a unique ID.
3. Set `current_key_id` to the new key ID.
4. Restart the service. All new encryptions use the new key; old data remains decryptable because old keys are still present.
5. Optionally re-encrypt existing data using the `RotateKey` API to eliminate dependency on old keys.
6. Once all data has been re-encrypted, remove old keys from the configuration.

A future `vault` storage type is planned for integration with external secret managers such as HashiCorp Vault.

## Authentication

### JWT-Based API Authentication

NIS issues JWT tokens for API access after successful username/password authentication.

- **Signing method**: HMAC-SHA256 (`HS256`)
- **Token lifetime**: Configurable via `auth.token_expiry` (default: 24 hours)
- **Issuer claim**: `nis`
- **Claims**: User ID, username, role, standard registered claims (exp, iat, nbf, iss, sub)

On every authenticated request, the token is validated and the user is looked up in the database to confirm the account still exists. Deleted users are immediately denied access even if they hold a valid token.

### Password Hashing

Passwords are hashed using **bcrypt** at the default cost factor (currently 10). Plaintext passwords are never stored or logged.

### Token Lifecycle

- Tokens are issued on successful login via `/login`.
- Tokens expire after the configured TTL.
- There is no refresh token mechanism; users must re-authenticate after expiry.
- To revoke access, delete the API user from the database. The next token validation will fail.

## Authorization

### Casbin RBAC Model

NIS uses [Casbin](https://casbin.org/) for role-based access control. The model follows a standard RBAC pattern:

```
request:  subject, object, action
policy:   subject, object, action
matcher:  role(subject) AND object AND action must match
```

### Roles

| Role | Scope | Description |
|------|-------|-------------|
| `admin` | Global | Full access to all resources and operations. Can manage API users, operators, accounts, users, clusters, signing keys, and exports. |
| `operator-admin` | Single operator | Can read operators, manage accounts, users, and scoped signing keys within their assigned operator. Can read clusters and trigger syncs. Cannot create or delete operators or clusters. |
| `account-admin` | Single account | Can read operators and accounts, create and manage users within their assigned account. Can read clusters. Cannot manage signing keys, clusters, or operators. |

### Permission Matrix

| Resource | Action | admin | operator-admin | account-admin |
|----------|--------|-------|----------------|---------------|
| operator | create | Y | | |
| operator | read | Y | Y | Y |
| operator | update | Y | | |
| operator | delete | Y | | |
| account | create | Y | Y | |
| account | read | Y | Y | Y |
| account | update | Y | Y | |
| account | delete | Y | | |
| user | create | Y | Y | Y |
| user | read | Y | Y | Y |
| user | update | Y | Y | Y |
| user | delete | Y | Y | |
| scoped_key | create | Y | Y | |
| scoped_key | read | Y | Y | |
| scoped_key | update | Y | Y | |
| scoped_key | delete | Y | | |
| cluster | create | Y | | |
| cluster | read | Y | Y | Y |
| cluster | update | Y | | |
| cluster | delete | Y | | |
| cluster | sync | | Y | |
| export | create | Y | | |
| export | read | Y | Y | |
| api_user | create | Y | | |
| api_user | read | Y | | |
| api_user | update | Y | | |
| api_user | delete | Y | | |

### Scope Enforcement

In addition to Casbin policy checks, `operator-admin` and `account-admin` roles have scope enforcement. An `operator-admin` can only access resources belonging to their assigned operator. An `account-admin` can only access resources belonging to their assigned account. These scopes are enforced at the service layer.

## Secrets Management

### Production Recommendations

**Encryption keys**:
- Generate with `openssl rand -base64 32`.
- Store outside the configuration file using environment variables or a secret manager.
- The `--encryption-key` flag or `ENCRYPTION_KEY` environment variable can be used for single-key setups.
- For multi-key rotation, use the config file with keys injected at deploy time (e.g., via Kubernetes Secrets, HashiCorp Vault, or SOPS-encrypted files).
- Never commit encryption keys to version control.

**JWT signing secret**:
- Must be at least 32 bytes.
- Provide via the `--jwt-secret` flag, `AUTH_JWT_SECRET` environment variable, or config file.
- Changing the JWT secret invalidates all existing tokens, effectively logging out all users.

**Database credentials** (PostgreSQL):
- Use a dedicated database user with minimal privileges (SELECT, INSERT, UPDATE, DELETE on NIS tables).
- Provide credentials via environment variables or a secret manager rather than in config files.
- Enable SSL/TLS for the database connection (`sslmode: require` or `sslmode: verify-full`).

**NATS credentials**:
- System user credentials files (`.creds`) should have restrictive file permissions (`chmod 600`).
- Rotate NATS credentials periodically by re-creating the system user and syncing to the cluster.

### Environment Variables

For production deployments, prefer environment variables over config file values for secrets:

```bash
export AUTH_JWT_SECRET="your-jwt-secret-min-32-bytes"
export ENCRYPTION_KEY="your-32-byte-encryption-key"
```

## Network Security

### TLS

NIS does not terminate TLS natively. In production, always deploy NIS behind a TLS-terminating reverse proxy.

#### Nginx Example

```nginx
server {
    listen 443 ssl http2;
    server_name nis.example.com;

    ssl_certificate     /etc/ssl/certs/nis.crt;
    ssl_certificate_key /etc/ssl/private/nis.key;
    ssl_protocols       TLSv1.2 TLSv1.3;
    ssl_ciphers         HIGH:!aNULL:!MD5;

    # Required for gRPC-Web / Connect-RPC
    location / {
        proxy_pass http://127.0.0.1:8080;
        proxy_http_version 1.1;
        proxy_set_header Host $host;
        proxy_set_header X-Real-IP $remote_addr;
        proxy_set_header X-Forwarded-For $proxy_add_x_forwarded_for;
        proxy_set_header X-Forwarded-Proto $scheme;
    }
}
```

#### Caddy Example

```caddyfile
nis.example.com {
    reverse_proxy localhost:8080
}
```

Caddy automatically provisions and renews TLS certificates via Let's Encrypt.

### Additional Recommendations

- **Bind to localhost** in production (`host: "127.0.0.1"` in config) and let the reverse proxy handle external traffic.
- **Firewall rules** -- Restrict access to the NIS port (default 8080) to only the reverse proxy.
- **NATS connections** -- Use TLS for connections between NIS and NATS clusters. Configure NATS with TLS certificates and point NIS at `nats://` or `tls://` URLs accordingly.
- **Database connections** -- Use TLS for PostgreSQL connections in production (`sslmode: require`).
- **Rate limiting** -- Configure rate limiting in your reverse proxy to protect the login endpoint from brute-force attacks.
- **CORS** -- If the UI is served from a different origin, configure CORS headers appropriately in the reverse proxy.

## Vulnerability Reporting

If you discover a security vulnerability in NIS, please report it responsibly.

### Responsible Disclosure Process

1. **Do not** open a public GitHub issue for security vulnerabilities.
2. Send an email to the project maintainer with the following information:
   - Description of the vulnerability
   - Steps to reproduce
   - Potential impact assessment
   - Suggested fix (if any)
3. You will receive an acknowledgment within 48 hours.
4. A fix will be developed and tested privately.
5. A security advisory will be published alongside the fix release.

### Contact

- **Email**: Report vulnerabilities via the contact information listed in the repository's GitHub profile or by opening a private security advisory on GitHub using the "Report a vulnerability" button under the Security tab.

### Scope

The following are in scope for security reports:

- Authentication or authorization bypass
- Encryption weaknesses or key exposure
- Injection attacks (SQL, command, etc.)
- Sensitive data exposure (keys, credentials, tokens)
- Cross-site scripting (XSS) or cross-site request forgery (CSRF) in the web UI

The following are out of scope:

- Denial of service (DoS) without a practical exploit
- Issues in third-party dependencies (report these upstream, but let us know so we can update)
- Issues requiring physical access to the server
