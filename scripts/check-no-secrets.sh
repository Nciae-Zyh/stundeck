#!/usr/bin/env bash
set -euo pipefail

if git grep -nE -- '-----BEGIN (RSA |EC |OPENSSH )?PRIVATE KEY-----|gh[pousr]_[A-Za-z0-9_]{20,}|AKIA[0-9A-Z]{16}' ':!scripts/check-no-secrets.sh'; then
  echo "Potential committed credential detected." >&2
  exit 1
fi

if git grep -nE -- '(CLOUDFLARE_API_TOKEN|CF_API_TOKEN|CLOUDFLARE_GLOBAL_API_KEY|STUNDECK_MASTER_KEY)[[:space:]]*[:=][[:space:]]*[A-Za-z0-9_-]{20,}' ':!scripts/check-no-secrets.sh'; then
  echo "A secret-looking value is assigned to a credential variable." >&2
  exit 1
fi

echo "No committed credential patterns detected."
