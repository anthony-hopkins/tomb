#!/usr/bin/env bash
# Writes /opt/tomb/.env from instance metadata and Secret Manager, and adjusts
# the Caddyfile for optional values.
#
# Called by BOTH startup.sh (on every boot) and deploy.sh (on every deploy), so
# a configuration change made in OpenTofu reaches the running stack on the next
# deploy rather than waiting for a reboot.
#
# That sharing is the point. When deploy.sh only rewrote APP_IMAGE, changing
# TOMB_DOMAIN updated the instance metadata and then did nothing: the stack kept
# serving the old hostname while the deploy reported success, because the health
# checks passed against the old URL.
#
# Usage: configure.sh FULLY_QUALIFIED_IMAGE

set -euo pipefail

APP_DIR=/opt/tomb
IMAGE="${1:?usage: configure.sh FULLY_QUALIFIED_IMAGE}"

log() { echo "[tomb-configure] $*"; }

meta() {
  curl -fsS -H "Metadata-Flavor: Google" \
    "http://metadata.google.internal/computeMetadata/v1/instance/attributes/$1" 2>/dev/null || true
}

TOMB_DOMAIN="$(meta tomb-domain)"
PUBLIC_URL="$(meta tomb-public-url)"
BNET_REGION="$(meta tomb-bnet-region)"
BNET_CLIENT_ID="$(meta tomb-bnet-client-id)"
TOMB_GUILD_NAME="$(meta tomb-guild-name)"
TOMB_GUILD_REALM="$(meta tomb-guild-realm)"
ACME_EMAIL="$(meta tomb-acme-email)"
# Optional application configuration: empty when unset, and the app reads
# empty as its default.
TOMB_GUILD_RANKS="$(meta tomb-guild-ranks)"
TOMB_GUILD_ROSTER_TTL="$(meta tomb-guild-roster-ttl)"
TOMB_GUILD_OFFICER_RANK="$(meta tomb-guild-officer-rank)"
TOMB_TIMEZONE="$(meta tomb-timezone)"
TOMB_ADMIN="$(meta tomb-admin)"
DB_SECRET="$(meta tomb-db-secret)"
BNET_SECRET="$(meta tomb-bnet-secret)"

TOKEN="$(curl -fsS -H 'Metadata-Flavor: Google' \
  'http://metadata.google.internal/computeMetadata/v1/instance/service-accounts/default/token' |
  python3 -c 'import json,sys; print(json.load(sys.stdin)["access_token"])')"
PROJECT="$(curl -fsS -H 'Metadata-Flavor: Google' \
  'http://metadata.google.internal/computeMetadata/v1/project/project-id')"

fetch_secret() {
  curl -fsS -H "Authorization: Bearer $TOKEN" \
    "https://secretmanager.googleapis.com/v1/projects/$PROJECT/secrets/$1/versions/latest:access" |
    python3 -c 'import json,sys,base64; print(base64.b64decode(json.load(sys.stdin)["payload"]["data"]).decode(), end="")'
}

log "fetching secrets"
DB_PASSWORD="$(fetch_secret "$DB_SECRET")"

# The Battle.net secret is added by hand after the first apply, so no version on
# an early boot is expected rather than fatal. The app requires the variable to
# be non-empty, so a placeholder keeps it starting; sign-in fails until a real
# version exists, which is the honest behaviour.
if ! BNET_CLIENT_SECRET="$(fetch_secret "$BNET_SECRET" 2>/dev/null)"; then
  log "WARNING: secret $BNET_SECRET has no version yet; sign-in will fail until one is added"
  BNET_CLIENT_SECRET="unset"
fi

# Holds the database password and the client secret.
umask 077
{
  echo "APP_IMAGE=$IMAGE"
  echo "TOMB_DOMAIN=$TOMB_DOMAIN"
  echo "PUBLIC_URL=$PUBLIC_URL"
  echo "ACME_EMAIL=$ACME_EMAIL"
  echo "BNET_REGION=$BNET_REGION"
  echo "BNET_CLIENT_ID=$BNET_CLIENT_ID"
  echo "BNET_CLIENT_SECRET=$BNET_CLIENT_SECRET"
  echo "TOMB_GUILD_NAME=$TOMB_GUILD_NAME"
  echo "TOMB_GUILD_REALM=$TOMB_GUILD_REALM"
  echo "TOMB_GUILD_RANKS=$TOMB_GUILD_RANKS"
  echo "TOMB_GUILD_ROSTER_TTL=$TOMB_GUILD_ROSTER_TTL"
  echo "TOMB_GUILD_OFFICER_RANK=$TOMB_GUILD_OFFICER_RANK"
  echo "TOMB_TIMEZONE=$TOMB_TIMEZONE"
  echo "TOMB_ADMIN=$TOMB_ADMIN"
  echo "DB_PASSWORD=$DB_PASSWORD"
} >"$APP_DIR/.env"
chmod 600 "$APP_DIR/.env"
umask 022

# An `email` directive with no argument is a Caddyfile parse error, so Caddy
# would crash-loop and nothing would listen on 443. Drop the line when no
# address is configured; Caddy then registers with ACME anonymously.
if [ -z "$ACME_EMAIL" ]; then
  sed -i '/{\$ACME_EMAIL}/d' "$APP_DIR/Caddyfile"
fi

# Prove the Caddyfile parses BEFORE anything restarts on it.
#
# A bad Caddyfile does not degrade the site, it deletes it: Caddy exits during
# config load, so nothing binds 80 or 443 and every request is refused at the
# TCP level. `docker compose up -d` reports success anyway -- it starts the
# container, it does not wait to see whether the container stays up -- so a
# parse error reaches production looking exactly like a clean deploy.
#
# That is how the empty `email` directive shipped. It was caught by a human
# reading a verification job twenty retries later, with no logs, rather than
# here, where the error message names the line.
#
# Same image Compose runs, read out of compose.yaml so the two cannot drift.
CADDY_IMAGE="$(awk '$1=="caddy:"{f=1;next} f&&$1=="image:"{print $2;exit} f&&/^  [a-z]/{exit}'   "$APP_DIR/compose.yaml")"

if [ -z "$CADDY_IMAGE" ]; then
  log "ERROR: could not find the caddy image in $APP_DIR/compose.yaml"
  exit 1
fi

log "validating the Caddyfile with $CADDY_IMAGE"
if ! validation="$(docker run --rm   -e TOMB_DOMAIN="$TOMB_DOMAIN"   -e ACME_EMAIL="$ACME_EMAIL"   -v "$APP_DIR/Caddyfile:/etc/caddy/Caddyfile:ro"   "$CADDY_IMAGE" caddy validate --adapter caddyfile --config /etc/caddy/Caddyfile 2>&1)"; then
  log "ERROR: the generated Caddyfile is not valid, so Caddy would not start."
  log "Refusing to restart the stack on it; the current site keeps serving."
  echo "$validation" >&2
  echo "--- the file that failed to parse ---" >&2
  cat -n "$APP_DIR/Caddyfile" >&2
  exit 1
fi

log "configured for domain '$TOMB_DOMAIN', image '$IMAGE'"
