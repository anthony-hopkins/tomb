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

# Serialise against the other script that rewrites /opt/tomb.
#
# startup.sh runs on every boot; deploy.sh runs over SSH. They never used to
# overlap, because the VM was always already up when a deploy arrived. Then
# develop started stopping itself overnight, so a deploy now STARTS the VM and
# SSHes in immediately -- and the boot-time script is still running when it
# does. Both rewrite /opt/tomb/{compose.yaml,Caddyfile,deploy.sh,configure.sh}
# with docker cp, which replaces each file rather than editing it in place, so
# there is a window where it does not exist. A deploy landed in that window:
#
#   bash: /opt/tomb/configure.sh: No such file or directory
#
# One lock, taken by whichever arrives first; the other waits. Held on fd 9 for
# the life of the script, so children inherit it, and skipped when the process
# already holds it -- re-locking the same file from the same process would
# block on itself forever, and deploy.sh re-execs itself on self-update.
if [ "${TOMB_STACK_LOCKED:-}" != "1" ]; then
  exec 9>/var/lock/tomb-stack.lock
  if ! flock --timeout 900 9; then
    echo "ERROR: timed out waiting for the stack lock." >&2
    echo "Another process -- almost certainly the boot-time startup script -- has held it" >&2
    echo "for fifteen minutes. Check 'journalctl -u google-startup-scripts' on the VM." >&2
    exit 1
  fi
  export TOMB_STACK_LOCKED=1
fi

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

# The uploads directory is a bind mount on the data disk (compose.yaml), and
# the app writes to it as uid 65532. startup.sh creates it with that owner at
# boot -- but only at boot. Production was already up when that step was
# added, so the step never ran there: Docker created the host directory for
# the bind mount itself, as root, and every upload failed with "permission
# denied" on the first piece (2026-09-18). Set it right on every deploy;
# idempotent, and harmless when startup.sh already did it.
UPLOADS_DIR=/mnt/tomb-data/uploads
mkdir -p "$UPLOADS_DIR"
chown 65532:65532 "$UPLOADS_DIR"
chmod 700 "$UPLOADS_DIR"

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

# Before waiting on TLS, check the hostname resolves at all.
#
# Caddy validates over ACME HTTP-01, which requires Let's Encrypt to resolve the
# name and reach this VM. If the name does not resolve there is no certificate
# coming, and waiting three minutes to discover that is the least of it: every
# attempt is a FAILED VALIDATION against Let's Encrypt, which allows five per
# hostname per hour. The first deploy of dev.tombguild.com burned fourteen in
# one run, because the workflow retried the whole deploy ten times and each
# retry recreated Caddy into another doomed ACME order. That left the hostname
# rate-limited, so the certificate could not be issued even once DNS was fixed.
#
# So: resolve first, and if the name is not there, stop immediately with exit 3
# rather than generating more failures. The workflow treats 3 as "the deploy
# ran, the stack is up, DNS is the missing piece" and does not retry it.
if ! getent hosts "$TOMB_DOMAIN" >/dev/null 2>&1; then
  echo "ERROR: $TOMB_DOMAIN does not resolve, so Let's Encrypt cannot validate it." >&2
  echo "" >&2
  echo "The stack is up and the app is healthy; only TLS is missing. Point an A" >&2
  echo "record at this VM's reserved address and deploy again:" >&2
  echo "" >&2
  echo "    $TOMB_DOMAIN.  A  $(curl -fsS -H 'Metadata-Flavor: Google'     'http://metadata.google.internal/computeMetadata/v1/instance/network-interfaces/0/access-configs/0/external-ip' 2>/dev/null || echo '<see the run summary>')" >&2
  echo "" >&2
  echo "That address is reserved and survives VM recreation. Avoid re-running the" >&2
  echo "deploy until DNS resolves: each attempt spends part of an hourly Let's" >&2
  echo "Encrypt budget of five failed validations for this hostname." >&2
  exit 3
fi

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

# The name resolves but nothing is serving. Separate the two causes, because
# they need opposite responses: a dead Caddy is a real failure worth retrying
# and dumping logs for, whereas a live Caddy still working through an ACME order
# just needs DNS to have propagated and the retry to stop.
if dc ps --status running --services 2>/dev/null | grep -qx caddy; then
  echo "ERROR: caddy is running but has no usable certificate for $TOMB_DOMAIN yet." >&2
  echo "" >&2
  echo "$TOMB_DOMAIN resolves to $(getent hosts "$TOMB_DOMAIN" | awk '{print $1}' | head -1)." >&2
  echo "Check that address is this VM, and that port 80 is reachable from the" >&2
  echo "internet -- Let's Encrypt validates over HTTP-01 there, not on 443." >&2
  echo "" >&2
  echo "Recent caddy output:" >&2
  dc logs --tail 30 caddy >&2 || true
  echo "" >&2
  echo "Not retrying: repeated attempts spend an hourly Let's Encrypt budget of" >&2
  echo "five failed validations per hostname, and being rate-limited turns a DNS" >&2
  echo "fix into an hour of waiting." >&2
  exit 3
fi

fail "caddy is not running, so nothing is listening on 443.
  This is a dead proxy rather than a certificate problem -- Caddy exited during
  config load."
