# Contributing

Thank you for helping improve StunDeck.

## Before opening a pull request

```bash
make test
make build
```

Network changes need tests that do not depend on a contributor's real Cloudflare account or home router. Use `httptest` for Cloudflare API contracts and fake NATMap processes for supervisor behavior.

Never submit real API Tokens, account IDs, Zone IDs, hostnames, public IP mappings, LAN addresses, databases, screenshots containing private infrastructure, or generated `master.key` files.

## Scope

Keep provider code behind explicit interfaces. Firewall, UPnP/NAT-PMP and Cloudflare Tunnel automation must be opt-in and must not require a blanket privileged container.
