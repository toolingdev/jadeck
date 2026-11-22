#!/bin/bash
# Puts the release signing key on the controller and every add-on repo,
# straight from 1Password: the key is never printed or written to disk.
#
#   ./scripts/set-signing-secrets.sh "op://<vault>/<item>/<field>"
#
# Get the reference in 1Password: open the item, click the field's ▾ →
# Copy Secret Reference.
set -euo pipefail
cd "$(dirname "$0")/.."

REF="${1:-}"
[ -n "$REF" ] || { echo "usage: $0 \"op://<vault>/<item>/<field>\""; exit 1; }
OWNER=toolingdev
REPOS="jadeck jadeck-addon-ai-controller jadeck-addon-behringer-wing jadeck-addon-chat jadeck-addon-http-api jadeck-addon-midi jadeck-addon-proclaim jadeck-addon-simulated jadeck-addon-vmix"

[ "$(gh api user -q .login)" = "$OWNER" ] || { echo "gh is not signed in as $OWNER (run: gh auth switch -h github.com -u $OWNER)"; exit 1; }

KEY="$(op read "$REF")"
PUB="$(printf %s "$KEY" | go run ./cmd/ptzsign pub -)" || { echo "that 1Password field is not the signing key"; exit 1; }

for r in $REPOS; do
  printf %s "$KEY" | gh secret set PTZ_SIGNING_KEY --repo "$OWNER/$r"
done
unset KEY
gh variable set PTZ_UPDATE_PUBKEY --repo "$OWNER/jadeck" --body "$PUB"

echo
echo "Done. Public key (safe to share): $PUB"
echo "Secrets set on: $REPOS"
