#!/usr/bin/env bash
# VM startup script. Runs on every boot: installs Docker if absent, prepares the
# data disk, fetches secrets, writes the Compose environment, and starts the
# stack.
#
# Idempotent by design -- a reboot must converge on a running site, not on a
# half-configured one.

set -euo pipefail

log() { echo "[tomb-startup] $*"; }

APP_DIR=/opt/tomb
DATA_MOUNT=/mnt/tomb-data
DATA_DEVICE=/dev/disk/by-id/google-tomb-data

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

meta() {
  curl -fsS -H "Metadata-Flavor: Google" \
    "http://metadata.google.internal/computeMetadata/v1/instance/attributes/$1" 2>/dev/null || true
}

# ---------------------------------------------------------------------------
# Docker
# ---------------------------------------------------------------------------
if ! command -v docker >/dev/null 2>&1; then
  log "installing Docker"
  export DEBIAN_FRONTEND=noninteractive
  apt-get update -qq
  apt-get install -y -qq ca-certificates curl gnupg python3 >/dev/null

  install -m 0755 -d /etc/apt/keyrings
  curl -fsSL https://download.docker.com/linux/ubuntu/gpg |
    gpg --dearmor -o /etc/apt/keyrings/docker.gpg
  chmod a+r /etc/apt/keyrings/docker.gpg

  codename="$(. /etc/os-release && echo "$VERSION_CODENAME")"
  arch="$(dpkg --print-architecture)"
  echo "deb [arch=$arch signed-by=/etc/apt/keyrings/docker.gpg] https://download.docker.com/linux/ubuntu $codename stable" \
    >/etc/apt/sources.list.d/docker.list

  apt-get update -qq
  apt-get install -y -qq docker-ce docker-ce-cli containerd.io \
    docker-buildx-plugin docker-compose-plugin >/dev/null
  systemctl enable --now docker
else
  log "Docker already present"
fi

# ---------------------------------------------------------------------------
# Data disk. Formatted only when it has no filesystem, so member records
# survive every reboot and every VM reimage.
# ---------------------------------------------------------------------------
mkdir -p "$DATA_MOUNT"

if [ -b "$DATA_DEVICE" ]; then
  if ! blkid "$DATA_DEVICE" >/dev/null 2>&1; then
    log "data disk is blank; creating ext4"
    mkfs.ext4 -m 0 -E lazy_itable_init=0,lazy_journal_init=0,discard "$DATA_DEVICE"
  fi

  if ! mountpoint -q "$DATA_MOUNT"; then
    log "mounting data disk at $DATA_MOUNT"
    mount -o discard,defaults "$DATA_DEVICE" "$DATA_MOUNT"
  fi

  if ! grep -q "google-tomb-data" /etc/fstab; then
    echo "$DATA_DEVICE $DATA_MOUNT ext4 discard,defaults,nofail 0 2" >>/etc/fstab
  fi
else
  log "WARNING: $DATA_DEVICE not found; Postgres will write to the boot disk"
fi

# Postgres in the official image runs as uid 999.
mkdir -p "$DATA_MOUNT/postgres"
chown -R 999:999 "$DATA_MOUNT/postgres"

# ---------------------------------------------------------------------------
# Configuration from instance metadata and Secret Manager
# ---------------------------------------------------------------------------
mkdir -p "$APP_DIR"

# Which image to boot. The .env written by the last deploy wins over instance
# metadata, and that order matters more than it looks.
#
# compute.tf carries `ignore_changes = [metadata["tomb-image"]]`, so that
# metadata key is frozen at whatever the FIRST apply set and is never updated
# again -- production's still reads 46cf70b1eeb5 while the VM has long been
# running a much later build. Booting from metadata therefore rolls the
# application back to the first image ever deployed, silently, and the deploy
# that follows reports success because the app is healthy; it is just old.
#
# Rebooting was rare enough for that to hide. A develop environment that stops
# every night would hit it every morning.
#
# /opt/tomb/.env is written by configure.sh on every deploy, so APP_IMAGE there
# is the image actually running. Metadata is the fallback for a genuinely first
# boot, when no .env exists yet.
APP_IMAGE=""
if [ -f "$APP_DIR/.env" ]; then
  APP_IMAGE="$(sed -n 's/^APP_IMAGE=//p' "$APP_DIR/.env" | head -1)"
  [ -n "$APP_IMAGE" ] && log "resuming the last deployed image from .env: $APP_IMAGE"
fi

if [ -z "$APP_IMAGE" ]; then
  APP_IMAGE="$(meta tomb-image)"
  [ -n "$APP_IMAGE" ] && log "first boot; taking the image from instance metadata: $APP_IMAGE"
fi

if [ -z "$APP_IMAGE" ]; then
  log "ERROR: no image in $APP_DIR/.env or instance metadata; cannot start the stack"
  exit 1
fi

# Only needed to authenticate the registry pull below. configure.sh mints its
# own token for Secret Manager.
TOKEN="$(curl -fsS -H 'Metadata-Flavor: Google' \
  'http://metadata.google.internal/computeMetadata/v1/instance/service-accounts/default/token' |
  python3 -c 'import json,sys; print(json.load(sys.stdin)["access_token"])')"


# ---------------------------------------------------------------------------
# Compose project, extracted from the app image so the VM needs no checkout of
# the repository.
# ---------------------------------------------------------------------------
REGISTRY_HOST="${APP_IMAGE%%/*}"
log "authenticating Docker to $REGISTRY_HOST"
docker login -u oauth2accesstoken -p "$TOKEN" "https://$REGISTRY_HOST" >/dev/null 2>&1 ||
  log "WARNING: docker login failed; the image pull will probably fail too"

log "extracting the Compose project from $APP_IMAGE"
docker pull -q "$APP_IMAGE"
cid="$(docker create "$APP_IMAGE")"
docker cp "$cid:/deploy/compose.yaml" "$APP_DIR/compose.yaml"
docker cp "$cid:/deploy/Caddyfile" "$APP_DIR/Caddyfile"
docker cp "$cid:/deploy/deploy.sh" "$APP_DIR/deploy.sh"
docker cp "$cid:/deploy/configure.sh" "$APP_DIR/configure.sh"
docker rm -f "$cid" >/dev/null
chmod +x "$APP_DIR/deploy.sh" "$APP_DIR/configure.sh"

# Shared with deploy.sh, so a metadata change takes effect on either path.
"$APP_DIR/configure.sh" "$APP_IMAGE"

log "starting the stack"
cd "$APP_DIR"
docker compose --env-file "$APP_DIR/.env" up -d --remove-orphans

log "done; the site should answer on https://$(meta tomb-domain)"
