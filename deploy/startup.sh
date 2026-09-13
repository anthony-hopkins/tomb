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

APP_IMAGE="$(meta tomb-image)"
if [ -z "$APP_IMAGE" ]; then
  log "ERROR: instance metadata has no tomb-image; cannot start the stack"
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
