# Security Policy

## Supported versions

Until the first stable release, security fixes are applied to the latest `main` branch and latest container tag only.

## Reporting a vulnerability

Please use GitHub private vulnerability reporting when enabled. Do not include live Cloudflare Tokens, Global API Keys, public IP mappings, LAN topology, session cookies, databases or `master.key` files in a public issue.

## Secret handling

- No credential is committed to this repository or baked into images.
- Cloudflare Tokens and Webhook Secrets are encrypted with AES-256-GCM.
- The encryption key is generated locally on first boot and stored separately from SQLite.
- HTTP responses never return saved Cloudflare Tokens.
- Authentication sessions are server-side and store only a SHA-256 hash of the cookie token.
- Administrator passwords use Argon2id; optional TOTP secrets are encrypted with AES-256-GCM.
- First-run setup accepts only loopback or private-network clients.
- Local/LAN/public source policies and exact or wildcard Host allowlists run before application routing.
- State-changing APIs require the session CSRF token.

An attacker who can read both the database and `master.key` can decrypt saved provider credentials. Protect the entire data directory with host filesystem permissions and encrypted backups.
