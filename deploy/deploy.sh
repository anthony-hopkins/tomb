#!/usr/bin/env bash
# Rolls the stack onto a new image. Run on the VM by the deploy workflow:
#
#   sudo /opt/tomb/deploy.sh FULLY_QUALIFIED_IMAGE
#
# Deliberately small: a deploy is a pull and a restart, not a reconfiguration.
# Configuration comes from instance metadata at boot, via startup.sh.

set -euo pipefail

APP_DIR=/opt/tomb
IMAGE="${1:?usage: deploy.sh FULLY_QUALIFIED_IMAGE}"

log() { echo "[tomb-deploy] $*"; }

if [ ! -f "$APP_DIR/.env" ]; then
  echo "ERROR: $APP_DIR/.env is missing, so the startup script has not run." >&2
  echo "Reboot the instance, or re-run the startup script, before deploying." >&2
  exit 1
fi

# Authenticate to Artifact Registry as the VM itself.
TOKEN="$(curl -fsS -H 'Metadata-Flavor: Google' \
  'http://metadata.google.internal/computeMetadata/v1/instance/service-accounts/default/token' |
  python3 -c 'import json,sys; print(json.load(sys.stdin)["access_token"])')"
REGISTRY_HOST="${IMAGE%%/*}"
docker login -u oauth2accesstoken -p "$TOKEN" "https://$REGISTRY_HOST" >/dev/null 2>&1

log "pulling $IMAGE"
docker pull -q "$IMAGE"

# Refresh the Compose project from the new image, so a change to compose.yaml or
# the Caddyfile ships together with the code that expects it.
cid="$(docker create "$IMAGE")"
docker cp "$cid:/deploy/compose.yaml" "$APP_DIR/compose.yaml"
docker cp "$cid:/deploy/Caddyfile" "$APP_DIR/Caddyfile"
docker cp "$cid:/deploy/deploy.sh" "$APP_DIR/deploy.sh.new"
docker cp "$cid:/deploy/configure.sh" "$APP_DIR/configure.sh"
docker rm -f "$cid" >/dev/null
mv "$APP_DIR/deploy.sh.new" "$APP_DIR/deploy.sh"
chmod +x "$APP_DIR/deploy.sh" "$APP_DIR/configure.sh"

# Rewrite the whole environment from instance metadata and Secret Manager,
# rather than only swapping the image tag.
#
# This was previously a single sed on APP_IMAGE, which meant a configuration
# change in OpenTofu -- a new TOMB_DOMAIN, say -- updated the instance metadata
# and then did nothing: the stack kept serving the old hostname while the
# deploy reported success, because the health checks passed against the old URL.
"$APP_DIR/configure.sh" "$IMAGE"

log "restarting the stack"
cd "$APP_DIR"
docker compose --env-file "$APP_DIR/.env" up -d --remove-orphans

# Reclaim space on a 20 GB boot disk; images accumulate quickly otherwise.
docker image prune -f --filter "until=168h" >/dev/null 2>&1 || true

log "waiting for the app to report ready"
for i in $(seq 1 30); do
  if docker compose --env-file "$APP_DIR/.env" exec -T app /app/tomb -healthcheck 2>/dev/null; then
    log "ready after $i attempt(s)"
    exit 0
  fi
  sleep 4
done

echo "ERROR: the app never reported ready. Recent logs:" >&2
docker compose --env-file "$APP_DIR/.env" logs --tail 50 app >&2
exit 1
