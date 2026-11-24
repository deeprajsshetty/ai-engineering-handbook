#!/usr/bin/env bash
# Create deploy/compose/.env with random secrets and admin/operator/viewer API keys.
# Raw API keys are written once to deploy/compose/.env.keys (mode 600, git-ignored).
set -euo pipefail
cd "$(dirname "$0")/../deploy/compose"

if [[ -f .env && "${1:-}" != "--force" ]]; then
  echo ".env already exists (use --force to regenerate)"; exit 0
fi
umask 077
python3 - <<'PY'
import hashlib, re, secrets
from pathlib import Path

env = Path(".env.example").read_text()
keys = {role: "snt_" + secrets.token_urlsafe(32) for role in ("admin", "operator", "viewer")}
api_keys = ",".join(f"{r}:{r}:{hashlib.sha256(k.encode()).hexdigest()}" for r, k in keys.items())

def secret(match: re.Match) -> str:
    name = match.group(1)
    value = api_keys if name == "SENTINEL_API_KEYS" else secrets.token_urlsafe(32)
    return f"{name}={value}"

Path(".env").write_text(re.sub(r"^([A-Z_]+)=CHANGE_ME$", secret, env, flags=re.M))
Path(".env.keys").write_text("".join(f"SENTINEL_{r.upper()}_KEY={k}\n" for r, k in keys.items()))
print("wrote deploy/compose/.env and deploy/compose/.env.keys")
print("Console API keys (also in .env.keys):")
for r, k in keys.items():
    print(f"  {r:9s} {k}")
PY
