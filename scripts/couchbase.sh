#!/usr/bin/env bash
#
# Manage a local Couchbase for running the examples.
#
#   ./scripts/couchbase.sh up     # reuse a reachable Couchbase, else start a container
#   ./scripts/couchbase.sh down   # remove the container this script created
#
# If a Couchbase is already reachable at $CB_HOST:8091 (your own install, an
# existing container, etc.) this script will NOT create or modify a container —
# it only ensures the target bucket exists. Containers it creates carry a label,
# and it refuses to start or remove a container without that label, even one
# named $CB_CONTAINER. Override connection details via env:
#
#   CB_HOST (localhost)  CB_USER (Administrator)  CB_PASS (password)
#   CB_BUCKET (test)     CB_CONTAINER (couchbase) CB_IMAGE (couchbase:community)
#
set -euo pipefail

CB_HOST="${CB_HOST:-localhost}"
CB_USER="${CB_USER:-Administrator}"
CB_PASS="${CB_PASS:-password}"
CB_BUCKET="${CB_BUCKET:-test}"
CB_CONTAINER="${CB_CONTAINER:-couchbase}"
CB_IMAGE="${CB_IMAGE:-couchbase:community}"

MGMT="http://${CB_HOST}:8091"
QUERY="http://${CB_HOST}:8093"
LABEL=com.xk6-couchbase.acceptance
OWNER=community
RESPONSE=

# Prints the HTTP status of a GET, or 000 if the server cannot be reached.
http_code() { curl -s -o /dev/null -w '%{http_code}' "$@" 2>/dev/null || true; }

reachable() { [ "$(http_code "$MGMT/ui/index.html")" = "200" ]; }

fail() { echo "ERROR: $*" >&2; exit 1; }

# api METHOD PATH [curl args...] calls the management API, keeps the response
# body in RESPONSE, and fails the script on anything but a 2xx status.
api() {
  local method="$1" path="$2" out code
  shift 2
  out="$(curl -s -w '\n%{http_code}' -X "$method" "$MGMT$path" "$@")" \
    || fail "$method $path: cannot reach $MGMT"
  code="${out##*$'\n'}"
  RESPONSE="${out%$'\n'*}"
  case "$code" in
    2??) ;;
    *) fail "$method $path returned HTTP $code: $RESPONSE" ;;
  esac
}

# Listing fails when the Docker daemon is unreachable, which must not be
# mistaken for "no such container".
container_exists() {
  local names
  names="$(docker ps -a --format '{{.Names}}')" || fail "cannot contact Docker"
  printf '%s\n' "$names" | grep -qxF "$CB_CONTAINER"
}

check_container_owner() {
  local owner
  owner="$(docker inspect --format "{{index .Config.Labels \"$LABEL\"}}" "$CB_CONTAINER")"
  if [ "$owner" != "$OWNER" ]; then
    echo "ERROR: container '$CB_CONTAINER' was not created by this script; refusing to $1 it." >&2
    echo "       Set CB_CONTAINER to use another name, or manage it with docker directly." >&2
    echo "       (Containers created before this check have no label; remove one with" >&2
    echo "       'docker rm -f $CB_CONTAINER' if you no longer need its data.)" >&2
    exit 1
  fi
}

wait_reachable() {
  echo "waiting for Couchbase management UI at $MGMT ..."
  for _ in $(seq 1 90); do reachable && return 0; sleep 1; done
  echo "ERROR: Couchbase did not become reachable at $MGMT" >&2
  return 1
}

ensure_container() {
  if ! command -v docker >/dev/null 2>&1; then
    echo "ERROR: docker not found and no Couchbase reachable at $MGMT" >&2
    echo "       Install Docker, or point CB_HOST at an existing cluster." >&2
    exit 1
  fi
  if container_exists; then
    check_container_owner start
    if [ "$(docker inspect --format '{{.State.Running}}' "$CB_CONTAINER")" = true ]; then
      echo "container '$CB_CONTAINER' already running"
    else
      echo "starting existing container '$CB_CONTAINER'"
      docker start "$CB_CONTAINER" >/dev/null
    fi
  else
    echo "creating container '$CB_CONTAINER' from $CB_IMAGE"
    docker run -d --name "$CB_CONTAINER" --label "$LABEL=$OWNER" \
      -p 8091-8096:8091-8096 -p 11210-11211:11210-11211 "$CB_IMAGE" >/dev/null
  fi
  wait_reachable
}

# Initializes the cluster only if it has not been set up yet, so re-running up
# against our own configured container is a no-op. An unconfigured cluster
# lists no pools.
init_cluster() {
  api GET /pools
  if ! printf '%s' "$RESPONSE" | grep -qE '"pools":[[:space:]]*\[[[:space:]]*\]'; then
    return 0
  fi
  echo "initializing cluster"
  api POST /pools/default -d memoryQuota=512 -d indexMemoryQuota=512
  api POST /node/controller/setupServices --data-urlencode services=kv,n1ql,index
  api POST /settings/web -d port=8091 \
    --data-urlencode "username=$CB_USER" --data-urlencode "password=$CB_PASS"
}

ensure_bucket() {
  local code
  code="$(http_code -u "$CB_USER:$CB_PASS" "$MGMT/pools/default/buckets/$CB_BUCKET")"
  case "$code" in
    200) echo "bucket '$CB_BUCKET' already exists" ;;
    404)
      echo "creating bucket '$CB_BUCKET'"
      # Zero replicas: a single node cannot hold a replica, so majority-durable
      # writes (removeWithDurability) would otherwise fail as "durability impossible".
      api POST /pools/default/buckets -u "$CB_USER:$CB_PASS" \
        --data-urlencode "name=$CB_BUCKET" -d bucketType=couchbase -d ramQuota=256 \
        -d flushEnabled=1 -d replicaNumber=0
      ;;
    401) fail "Couchbase at $MGMT rejected user '$CB_USER'; check CB_USER and CB_PASS" ;;
    *) fail "checking bucket '$CB_BUCKET' returned HTTP $code" ;;
  esac
  echo "waiting for bucket '$CB_BUCKET' to be healthy ..."
  for _ in $(seq 1 60); do
    if curl -s -u "$CB_USER:$CB_PASS" "$MGMT/pools/default/buckets/$CB_BUCKET" \
         | grep -q '"status":"healthy"'; then return 0; fi
    sleep 1
  done
  fail "bucket '$CB_BUCKET' was not reported healthy within 60s"
}

# A primary index is optional (examples use USE KEYS), but handy for ad-hoc N1QL.
ensure_primary_index() {
  for _ in $(seq 1 30); do [ "$(http_code "$QUERY/admin/ping")" = "200" ] && break; sleep 1; done
  curl -s -u "$CB_USER:$CB_PASS" "$QUERY/query/service" \
    --data-urlencode "statement=CREATE PRIMARY INDEX IF NOT EXISTS ON \`$CB_BUCKET\`._default._default" \
    >/dev/null 2>&1 || true
}

cmd_up() {
  if reachable; then
    echo "Couchbase already reachable at $MGMT — reusing it (not touching Docker)"
  else
    ensure_container
    init_cluster
  fi
  ensure_bucket
  ensure_primary_index
  echo "Couchbase ready at $MGMT (user: $CB_USER, bucket: $CB_BUCKET)"
}

cmd_down() {
  if command -v docker >/dev/null 2>&1 && container_exists; then
    check_container_owner remove
    docker rm -f "$CB_CONTAINER" >/dev/null
    echo "removed container '$CB_CONTAINER'"
  else
    echo "no container '$CB_CONTAINER' to remove"
  fi
}

case "${1:-}" in
  up)   cmd_up ;;
  down) cmd_down ;;
  *)    echo "usage: $0 {up|down}" >&2; exit 2 ;;
esac
