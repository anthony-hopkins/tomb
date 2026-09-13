#!/usr/bin/env bash
# Rolls the stack onto a new image. Run on the VM by the deploy workflow:
#
#   sudo /opt/tomb/deploy.sh FULLY_QUALIFIED_IMAGE
#
# Pull the image, refresh the Compose project and the configuration from
# instance metadata (configure.sh), restart, and then PROVE the site serves
# before reporting success. The last step is not ceremony: the two most recent
# production failures both had a healthy app container behind a dead Caddy, and
# this script called that a successful deploy.

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

# Does the image's deploy.sh differ from the one currently executing? Ask before
# overwriting it, while "$0" still refers to the old contents.
self_changed=""
cmp -s "$0" "$APP_DIR/deploy.sh.new" || self_changed=1

mv "$APP_DIR/deploy.sh.new" "$APP_DIR/deploy.sh"
chmod +x "$APP_DIR/deploy.sh" "$APP_DIR/configure.sh"

# Hand over to the version just installed.
#
# /opt/tomb/deploy.sh is always the script from the PREVIOUS image: the workflow
# SSHes in and runs whatever is on disk, and only then does that script copy its
# replacement out of the new image. So a fix to this file used to land one
# deploy late -- the deploy that shipped it installed it and ran the old logic
# anyway.
#
# That is not theoretical. It is exactly how the empty-ACME_EMAIL fix failed:
# the image carried the fix and the matching configure.sh, the old script on
# disk knew about neither, so it copied the unpatched Caddyfile into place,
# skipped the fixup that image expected, and restarted Caddy into the very parse
# error that had just been fixed. Caddy crash-looped for 36 minutes while the
# deploy reported success.
#
# Re-exec so the script that ships with an image is the script that deploys it.
# TOMB_DEPLOY_REEXEC bounds this to a single hop.
if [ -n "$self_changed" ] && [ "${TOMB_DEPLOY_REEXEC:-}" != "1" ]; then
  log "deploy.sh changed in $IMAGE; handing over to the new version"
  export TOMB_DEPLOY_REEXEC=1
  exec "$APP_DIR/deploy.sh" "$IMAGE"
fi

# Rewrite the whole environment from instance metadata and Secret Manager,
# rather than only swapping the image tag.
#
# This was previously a single sed on APP_IMAGE, which meant a configuration
# change in OpenTofu -- a new TOMB_DOMAIN, say -- updated the instance metadata
# and then did nothing: the stack kept serving the old hostname while the
# deploy reported success, because the health checks passed against the old URL.
"$APP_DIR/configure.sh" "$IMAGE"

dc() { docker compose --env-file "$APP_DIR/.env" "$@"; }

log "restarting the stack"
cd "$APP_DIR"
dc up -d --remove-orphans

# Caddy reads its configuration once, at startup. Compose recreates a container
# only when the service definition it hashes changes -- image, ports, volumes,
# environment -- and the Caddyfile's CONTENTS are none of those: it arrives
# through a bind mount, so rewriting it above changes nothing Compose looks at.
#
# So `up -d` leaves an already-running Caddy serving the configuration it
# started with, however many times the file underneath it is rewritten. Same
# silent no-op commit 7266dee fixed for .env, still live for the Caddyfile.
#
# `--no-deps` so this does not drag app and db through another restart.
log "recreating caddy so it reads the Caddyfile written above"
dc up -d --force-recreate --no-deps caddy

# Reclaim space on a 20 GB boot disk; images accumulate quickly otherwise.
docker image prune -f --filter "until=168h" >/dev/null 2>&1 || true

fail() {
  echo "ERROR: $1" >&2
  echo "--- container state ---" >&2
  dc ps >&2 || true
  for svc in caddy app db; do
    echo "--- $svc (last 50 lines) ---" >&2
    dc logs --tail 50 "$svc" >&2 || true
  done
  exit 1
}

log "waiting for the app to report ready"
ready=""
for i in $(seq 1 30); do
  if dc exec -T app /app/tomb -healthcheck 2>/dev/null; then
    log "app ready after $i attempt(s)"
    ready=1
    break
  fi
  sleep 4
done
[ -n "$ready" ] || fail "the app never reported ready."

# The app being healthy says nothing about whether the site is reachable, and
# that gap is exactly where the last two failures lived: Caddy was dead, app and
# db were fine, and the deploy reported success with nothing listening on 443.
#
# Probe the real edge -- TLS on 443, for the configured hostname, proxied
# through to the app -- pinned to the loopback so it tests this VM rather than
# whatever DNS happens to point at. A pass here means Caddy parsed its config,
# holds a certificate for the name, and can reach the app.
TOMB_DOMAIN="$(grep -E '^TOMB_DOMAIN=' "$APP_DIR/.env" | cut -d= -f2-)"
[ -n "$TOMB_DOMAIN" ] || fail "TOMB_DOMAIN is missing from $APP_DIR/.env."

# Generous: on a hostname change Caddy has to complete an ACME order first.
log "waiting for caddy to serve https://$TOMB_DOMAIN/healthz"
for i in $(seq 1 30); do
  if curl -fsS --max-time 10     --resolve "$TOMB_DOMAIN:443:127.0.0.1"     "https://$TOMB_DOMAIN/healthz" | grep -qx ok; then
    log "caddy serving after $i attempt(s)"
    log "deployed $IMAGE"
    exit 0
  fi
  sleep 6
done

fail "caddy never served https://$TOMB_DOMAIN/healthz from this VM.
  Nothing on 443 usually means Caddy exited during config load, or could not
  obtain a certificate -- if the hostname changed, check that DNS points at
  this VM, since Let's Encrypt validates over HTTP-01 on port 80."
