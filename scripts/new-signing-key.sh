#!/bin/bash
# Makes a new release signing key, saves it in 1Password, and puts it on
# the controller and every add-on repo. The key is never printed; the
# temporary file is deleted at the end.
#
#   ./scripts/new-signing-key.sh
set -euo pipefail
cd "$(dirname "$0")/.."

if ! op whoami >/dev/null 2>&1; then
  eval "$(op signin)" 2>/dev/null || true
fi
if ! op whoami >/dev/null 2>&1; then
  echo "1Password CLI isn't connected. In the 1Password app: Settings → Developer →"
  echo "turn on \"Integrate with 1Password CLI\", then run this again."
  exit 1
fi

TITLE="PTZ Controller release signing key"
if op item get "$TITLE" >/dev/null 2>&1; then
  echo "1Password already has \"$TITLE\". To reuse it, run:"
  echo "  ./scripts/set-signing-secrets.sh \"op://<vault>/$TITLE/password\""
  exit 1
fi

TMP="$(mktemp -d)"
trap 'rm -rf "$TMP"' EXIT
go run ./cmd/ptzsign keygen "$TMP/release.key" >/dev/null

ITEM="$(op item create --category=password --title="$TITLE" \
  "password=$(cat "$TMP/release.key")" \
  "notes[text]=ed25519 seed for signing PTZ Controller and add-on releases (GitHub secret PTZ_SIGNING_KEY on the toolingdev repos)." \
  --format json)"
REF="$(printf %s "$ITEM" | python3 -c 'import json,sys; i=json.load(sys.stdin); print("op://%s/%s/password" % (i["vault"]["id"], i["id"]))')"
echo "Saved in 1Password as \"$TITLE\"."

./scripts/set-signing-secrets.sh "$REF"
