#!/usr/bin/env bash
# Provision a private, repeatable Enterprise certificate-authentication fixture.
# Requires Docker, curl, and OpenSSL; Couchbase CLI runs inside the container.
set -euo pipefail
umask 077

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd -P)"
CERT_ROOT="$ROOT/.certs"
STATE="$CERT_ROOT/enterprise"
CONTAINER=xk6-couchbase-enterprise-cert
LABEL=com.xk6-couchbase.acceptance
OWNER=enterprise-cert
IMAGE=couchbase:enterprise-8.0.0
MGMT=http://127.0.0.1:28091
QUERY=https://127.0.0.1:38093
CB_BUCKET=tls-auth-test
CB_ADMIN_USER=Administrator
CB_USER=xk6-auth-user
RESTRICTED_USER=xk6-readonly-user
HTTP_CODE=
RESPONSE=

fail() { printf 'ERROR: %s\n' "$*" >&2; exit 1; }
require() { command -v "$1" >/dev/null 2>&1 || fail "Required command not found: $1"; }
# Hide OpenSSL's progress chatter; show its output only when it fails.
quiet() {
  local out
  out="$("$@" 2>&1)" || { printf '%s\n' "$out" >&2; return 1; }
}

# Refuse symlinks before reading secrets, writing fixtures, or removing state.
check_state_paths() {
  [ ! -L "$CERT_ROOT" ] || fail "Refusing symlink: $CERT_ROOT"
  [ ! -L "$STATE" ] || fail "Refusing symlink: $STATE"
  if [ -e "$CERT_ROOT" ]; then
    [ -d "$CERT_ROOT" ] || fail "$CERT_ROOT is not a directory"
  fi
  if [ -e "$STATE" ]; then
    [ -d "$STATE" ] || fail "$STATE is not a directory"
    local path
    for path in "$STATE"/* "$STATE"/.[!.]* "$STATE"/..?*; do
      [ ! -L "$path" ] || fail "Refusing symlink in generated state: $path"
    done
    [ -f "$STATE/.owner" ] || fail "Refusing unowned state directory: $STATE"
    [ "$(cat "$STATE/.owner")" = "$OWNER" ] || fail "State ownership marker does not match"
  fi
}

ensure_state() {
  check_state_paths
  mkdir -p "$CERT_ROOT"
  chmod 700 "$CERT_ROOT"
  if [ ! -d "$STATE" ]; then
    mkdir "$STATE"
    printf '%s\n' "$OWNER" > "$STATE/.owner"
  fi
  chmod 700 "$STATE"
  local path
  for path in "$STATE"/* "$STATE"/.[!.]* "$STATE"/..?*; do
    if [ -f "$path" ]; then chmod 600 "$path"; fi
  done
}

# Listing succeeds only when the daemon is reachable. Inspection alone must not
# turn a Docker-daemon error into "container absent".
container_exists() {
  local names name
  names="$(docker ps -a --format '{{.Names}}')" || fail "Cannot contact Docker"
  while IFS= read -r name; do
    [ "$name" != "$CONTAINER" ] || return 0
  done <<< "$names"
  return 1
}

check_container_owner() {
  local owner
  owner="$(docker inspect --format '{{index .Config.Labels "com.xk6-couchbase.acceptance"}}' "$CONTAINER")"
  [ "$owner" = "$OWNER" ] || fail "Refusing to adopt or delete unowned container '$CONTAINER'"
}

ensure_container() {
  if container_exists; then
    check_container_owner
    # A container without its persisted secrets cannot safely be reinitialized.
    [ -s "$STATE/admin-password" ] || fail "Owned container exists but its saved credentials are missing; run down first"
    if [ "$(docker inspect --format '{{.State.Running}}' "$CONTAINER")" != true ]; then
      docker start "$CONTAINER" >/dev/null
    fi
  else
    # Fail clearly instead of with Docker's bind error when another instance
    # (such as a manually created Enterprise container) holds these ports.
    local port
    for port in 28091 28093 21210 21207 38091 38093; do
      if (exec 3<>"/dev/tcp/127.0.0.1/$port") 2>/dev/null; then
        fail "127.0.0.1:$port is already in use; stop the other Couchbase instance first"
      fi
    done
    docker run -d --name "$CONTAINER" --label "$LABEL=$OWNER" \
      --memory 4g \
      -p 127.0.0.1:28091:8091 -p 127.0.0.1:28093:8093 \
      -p 127.0.0.1:21210:11210 -p 127.0.0.1:21207:11207 \
      -p 127.0.0.1:38091:18091 -p 127.0.0.1:38093:18093 \
      "$IMAGE" >/dev/null
  fi
}

save_password() {
  local file="$1"
  if [ ! -e "$file" ]; then
    openssl rand -hex 24 > "$file.tmp"
    mv "$file.tmp" "$file"
  fi
  [ -s "$file" ] || fail "Saved password is empty: $file"
  chmod 600 "$file"
}

# Keep response bodies private and do not echo them: errors can contain secrets.
request() {
  local method="$1" url="$2"
  shift 2
  HTTP_CODE="$(curl --silent --show-error --connect-timeout 3 --max-time 15 \
    --output "$STATE/http-response" --write-out '%{http_code}' \
    --request "$method" "$url" "$@")" || return 1
  RESPONSE="$(cat "$STATE/http-response")"
}

api() {
  local method="$1" endpoint="$2"
  shift 2
  request "$method" "$MGMT$endpoint" --user "$CB_ADMIN_USER:$CB_ADMIN_PASS" "$@" \
    || fail "Transport failure: $method $endpoint"
  case "$HTTP_CODE" in
    2??) ;;
    *) fail "$method $endpoint returned HTTP $HTTP_CODE (private response: $STATE/http-response)" ;;
  esac
}

wait_management() {
  local attempt=0
  while [ "$attempt" -lt 90 ]; do
    if request GET "$MGMT/pools" --user "$CB_ADMIN_USER:$CB_ADMIN_PASS" 2>/dev/null && [ "$HTTP_CODE" = 200 ]; then return; fi
    attempt=$((attempt + 1))
    sleep 1
  done
  fail "Couchbase management did not become ready within 90 attempts"
}

initialize_cluster() {
  local empty_pools='"pools"[[:space:]]*:[[:space:]]*\[[[:space:]]*\]'
  api GET /pools
  if [[ "$RESPONSE" =~ $empty_pools ]]; then
    api POST /pools/default --data memoryQuota=512 --data indexMemoryQuota=512
    # A previous up may have stopped after setting services but before credentials.
    api GET /nodes/self
    local services_pattern='"services"[[:space:]]*:[[:space:]]*\[([^]]*)\]'
    [[ "$RESPONSE" =~ $services_pattern ]] || fail "Cannot read node services"
    local services="${BASH_REMATCH[1]}"
    if [[ "$services" != *'"n1ql"'* ]] || [[ "$services" != *'"index"'* ]]; then
      api POST /node/controller/setupServices --data services=kv,n1ql,index
    fi
    api POST /settings/web --data port=8091 \
      --data-urlencode "username=$CB_ADMIN_USER" --data-urlencode "password=$CB_ADMIN_PASS"
  fi
  # Authenticate even on an already initialized cluster. Never ignore init errors.
  api GET /pools/default
  api POST /pools/default --data memoryQuota=512 --data indexMemoryQuota=512
  api GET /nodes/self
  local hostname_pattern='"hostname"[[:space:]]*:[[:space:]]*"([^"\\]+)"'
  [[ "$RESPONSE" =~ $hostname_pattern ]] || fail "Cannot read actual Couchbase node hostname"
  NODE="${BASH_REMATCH[1]}"
  NODE_HOST="${NODE%:8091}"
  local services_pattern='"services"[[:space:]]*:[[:space:]]*\[([^]]*)\]'
  [[ "$RESPONSE" =~ $services_pattern ]] || fail "Cannot read configured node services"
  local services="${BASH_REMATCH[1]}"
  [[ "$services" == *'"kv"'* && "$services" == *'"n1ql"'* && "$services" == *'"index"'* ]] \
    || fail "Owned container does not have KV, Query, and Index services"
  docker exec "$CONTAINER" /opt/couchbase/bin/couchbase-cli setting-alternate-address \
    --cluster http://127.0.0.1:8091 --username "$CB_ADMIN_USER" --password "$CB_ADMIN_PASS" \
    --set --node "$NODE" --hostname 127.0.0.1 \
    --ports mgmt=28091,mgmtSSL=38091,kv=21210,kvSSL=21207,n1ql=28093,n1qlSSL=38093 >/dev/null
}

ensure_bucket() {
  request GET "$MGMT/pools/default/buckets/$CB_BUCKET" --user "$CB_ADMIN_USER:$CB_ADMIN_PASS" \
    || fail "Cannot check acceptance bucket"
  case "$HTTP_CODE" in
    200) api POST "/pools/default/buckets/$CB_BUCKET" --data ramQuota=256 --data replicaNumber=0 ;;
    404) api POST /pools/default/buckets --data "name=$CB_BUCKET" \
      --data bucketType=couchbase --data ramQuota=256 --data replicaNumber=0 ;;
    *) fail "Checking acceptance bucket returned HTTP $HTTP_CODE" ;;
  esac
  local healthy='"status"[[:space:]]*:[[:space:]]*"healthy"' attempt=0
  while [ "$attempt" -lt 90 ]; do
    if request GET "$MGMT/pools/default/buckets/$CB_BUCKET" --user "$CB_ADMIN_USER:$CB_ADMIN_PASS" 2>/dev/null \
      && [ "$HTTP_CODE" = 200 ] && [[ "$RESPONSE" =~ $healthy ]]; then return; fi
    attempt=$((attempt + 1))
    sleep 1
  done
  fail "Acceptance bucket did not become healthy within 90 attempts"
}

ensure_ca() {
  local stem="$1" cn="$2"
  if [ -f "$STATE/$stem.pem" ]; then
    [ -s "$STATE/$stem.key" ] || fail "CA private key is missing: $stem"
    return
  fi
  if [ ! -s "$STATE/$stem.key" ]; then
    quiet openssl genrsa -out "$STATE/$stem.key.tmp" 2048 >/dev/null
    mv "$STATE/$stem.key.tmp" "$STATE/$stem.key"
  fi
  quiet openssl req -new -x509 -sha256 -days 3650 -key "$STATE/$stem.key" \
    -subj "/CN=$cn" -config "$STATE/ca.conf" -extensions ca \
    -out "$STATE/$stem.pem.tmp" >/dev/null
  mv "$STATE/$stem.pem.tmp" "$STATE/$stem.pem"
}

ensure_leaf() {
  local stem="$1" cn="$2" ca="$3" ext="$4" expired="${5:-no}"
  if [ -f "$STATE/$stem.pem" ]; then
    [ -s "$STATE/$stem.key" ] || fail "Certificate private key is missing: $stem"
    return
  fi
  if [ ! -s "$STATE/$stem.key" ]; then
    quiet openssl genrsa -out "$STATE/$stem.key.tmp" 2048 >/dev/null
    mv "$STATE/$stem.key.tmp" "$STATE/$stem.key"
  fi
  quiet openssl req -new -sha256 -key "$STATE/$stem.key" -subj "/CN=$cn" \
    -out "$STATE/$stem.csr" >/dev/null
  if [ "$expired" = yes ]; then
    # Explicit historic dates, not a zero-day certificate or clock-dependent hack.
    # This private CA database issues only this fixture. Reset failed issuance
    # attempts so its generated archive has one fixed, safely removable name.
    : > "$STATE/expired-index"
    printf '1000\n' > "$STATE/expired-serial"
    quiet openssl ca -batch -notext -config "$STATE/expired-ca.conf" \
      -startdate 20200101000000Z -enddate 20200102000000Z \
      -in "$STATE/$stem.csr" -out "$STATE/$stem.pem.tmp" >/dev/null
  else
    quiet openssl x509 -req -sha256 -days 825 -in "$STATE/$stem.csr" \
      -CA "$STATE/$ca.pem" -CAkey "$STATE/$ca.key" -CAcreateserial \
      -extfile "$STATE/$ext" -out "$STATE/$stem.pem.tmp" >/dev/null
  fi
  mv "$STATE/$stem.pem.tmp" "$STATE/$stem.pem"
}

ensure_certificates() {
  cat > "$STATE/ca.conf" <<'EOF'
[req]
distinguished_name = dn
[dn]
[ca]
basicConstraints = critical,CA:TRUE
keyUsage = critical,keyCertSign,cRLSign
subjectKeyIdentifier = hash
authorityKeyIdentifier = keyid:always
EOF
  cat > "$STATE/client.ext" <<'EOF'
basicConstraints = critical,CA:FALSE
keyUsage = critical,digitalSignature,keyEncipherment
extendedKeyUsage = clientAuth
subjectKeyIdentifier = hash
authorityKeyIdentifier = keyid,issuer
EOF
  local ip hostname
  ip="$(docker inspect --format '{{range .NetworkSettings.Networks}}{{.IPAddress}}{{end}}' "$CONTAINER")"
  hostname="$(docker exec "$CONTAINER" hostname)"
  [[ "$ip" =~ ^[0-9]+\.[0-9]+\.[0-9]+\.[0-9]+$ ]] || fail "Cannot determine container IPv4 address"
  [[ "$hostname" =~ ^[a-zA-Z0-9.-]+$ ]] || fail "Unexpected container hostname"
  [[ "$NODE_HOST" =~ ^[a-zA-Z0-9.-]+$ ]] || fail "Unexpected Couchbase node hostname"
  local san="IP:127.0.0.1,IP:$ip,DNS:localhost,DNS:$hostname"
  if [[ "$NODE_HOST" =~ ^[0-9]+\.[0-9]+\.[0-9]+\.[0-9]+$ ]]; then
    san="$san,IP:$NODE_HOST"
  else
    san="$san,DNS:$NODE_HOST"
  fi
  cat > "$STATE/server.ext" <<EOF
basicConstraints = critical,CA:FALSE
keyUsage = critical,digitalSignature,keyEncipherment
extendedKeyUsage = serverAuth,clientAuth
subjectKeyIdentifier = hash
authorityKeyIdentifier = keyid,issuer
subjectAltName = $san
EOF
  ensure_ca ca xk6-enterprise-acceptance-ca
  ensure_ca untrusted-ca xk6-untrusted-acceptance-ca
  [ -e "$STATE/expired-index" ] || : > "$STATE/expired-index"
  [ -e "$STATE/expired-serial" ] || printf '1000\n' > "$STATE/expired-serial"
  cat > "$STATE/expired-ca.conf" <<EOF
[ca]
default_ca = signer
[signer]
database = "$STATE/expired-index"
serial = "$STATE/expired-serial"
new_certs_dir = "$STATE"
certificate = "$STATE/ca.pem"
private_key = "$STATE/ca.key"
default_md = sha256
default_days = 1
unique_subject = no
policy = policy
x509_extensions = client
[policy]
commonName = supplied
[client]
basicConstraints = critical,CA:FALSE
keyUsage = critical,digitalSignature,keyEncipherment
extendedKeyUsage = clientAuth
subjectKeyIdentifier = hash
authorityKeyIdentifier = keyid,issuer
EOF
  ensure_leaf server localhost ca server.ext
  ensure_leaf client "$CB_USER" ca client.ext
  ensure_leaf unmapped xk6-unmapped-user ca client.ext
  ensure_leaf expired "$CB_USER" ca client.ext yes
  ensure_leaf untrusted "$CB_USER" untrusted-ca client.ext
  ensure_leaf restricted "$RESTRICTED_USER" ca client.ext
  cat "$STATE/server.pem" "$STATE/ca.pem" > "$STATE/chain.pem"
  local path
  for path in "$STATE"/*; do [ ! -f "$path" ] || chmod 600 "$path"; done
}

install_certificates() {
  docker exec --user root "$CONTAINER" sh -c \
    'mkdir -p /opt/couchbase/var/lib/couchbase/inbox/CA'
  docker cp "$STATE/ca.pem" "$CONTAINER:/opt/couchbase/var/lib/couchbase/inbox/CA/ca.pem" >/dev/null
  docker cp "$STATE/chain.pem" "$CONTAINER:/opt/couchbase/var/lib/couchbase/inbox/chain.pem" >/dev/null
  docker cp "$STATE/server.key" "$CONTAINER:/opt/couchbase/var/lib/couchbase/inbox/pkey.key" >/dev/null
  docker exec --user root "$CONTAINER" sh -c \
    'chown -R couchbase:couchbase /opt/couchbase/var/lib/couchbase/inbox && chmod 700 /opt/couchbase/var/lib/couchbase/inbox /opt/couchbase/var/lib/couchbase/inbox/CA && chmod 600 /opt/couchbase/var/lib/couchbase/inbox/CA/ca.pem /opt/couchbase/var/lib/couchbase/inbox/chain.pem /opt/couchbase/var/lib/couchbase/inbox/pkey.key'
  api POST /node/controller/loadTrustedCAs
  api POST /node/controller/reloadCertificate
  api PUT "/settings/rbac/users/local/$CB_USER" \
    --data-urlencode "password=$CB_PASS" --data-urlencode "name=Certificate acceptance user" \
    --data-urlencode "roles=data_reader[$CB_BUCKET],data_writer[$CB_BUCKET],query_select[$CB_BUCKET]"
  api PUT "/settings/rbac/users/local/$RESTRICTED_USER" \
    --data-urlencode "password=$RESTRICTED_PASS" --data-urlencode "name=Read-only certificate acceptance user" \
    --data-urlencode "roles=data_reader[$CB_BUCKET],query_select[$CB_BUCKET]"
  api POST /settings/clientCertAuth --header 'Content-Type: application/json' \
    --data-binary '{"state":"enable","prefixes":[{"path":"subject.cn","prefix":"","delimiter":""}]}'
}

wait_query() {
  local attempt=0 success='"status"[[:space:]]*:[[:space:]]*"success"'
  local query_ssl='"n1qlSSL"[[:space:]]*:[[:space:]]*18093'
  local statement="SELECT RAW d FROM \`$CB_BUCKET\`._default._default AS d USE KEYS \"__xk6_acceptance_readiness__\""
  while [ "$attempt" -lt 60 ]; do
    # Plaintext Query readiness is insufficient: TLS must be advertised to SDKs
    # and the mapped certificate must be able to query the actual test keyspace.
    if request GET "$MGMT/pools/default/nodeServices" --user "$CB_ADMIN_USER:$CB_ADMIN_PASS" 2>/dev/null \
      && [ "$HTTP_CODE" = 200 ] && [[ "$RESPONSE" =~ $query_ssl ]] \
      && request POST "$QUERY/query/service" --cacert "$STATE/ca.pem" \
        --cert "$STATE/client.pem" --key "$STATE/client.key" \
        --data-urlencode "statement=$statement" 2>/dev/null \
      && [ "$HTTP_CODE" = 200 ] && [[ "$RESPONSE" =~ $success ]]; then return; fi
    attempt=$((attempt + 1))
    sleep 1
  done
  fail "Certificate-authenticated TLS Query did not become ready within 60 attempts"
}

write_connection_env() {
  # Bash printf %q works on macOS Bash 3.2 and safely quotes absolute paths.
  {
    printf 'export %s=%q\n' CB_HOST 'couchbases://127.0.0.1:21207?network=external'
    printf 'export %s=%q\n' CB_PASSWORD_HOST 'couchbase://127.0.0.1:21210?network=external'
    printf 'export %s=%q\n' CB_BUCKET "$CB_BUCKET"
    printf 'export %s=%q\n' CB_CA_CERT "$STATE/ca.pem"
    printf 'export %s=%q\n' CB_CLIENT_CERT "$STATE/client.pem"
    printf 'export %s=%q\n' CB_CLIENT_KEY "$STATE/client.key"
    printf 'export %s=%q\n' CB_UNMAPPED_CERT "$STATE/unmapped.pem"
    printf 'export %s=%q\n' CB_UNMAPPED_KEY "$STATE/unmapped.key"
    printf 'export %s=%q\n' CB_EXPIRED_CERT "$STATE/expired.pem"
    printf 'export %s=%q\n' CB_EXPIRED_KEY "$STATE/expired.key"
    printf 'export %s=%q\n' CB_UNTRUSTED_CERT "$STATE/untrusted.pem"
    printf 'export %s=%q\n' CB_UNTRUSTED_KEY "$STATE/untrusted.key"
    printf 'export %s=%q\n' CB_RESTRICTED_CERT "$STATE/restricted.pem"
    printf 'export %s=%q\n' CB_RESTRICTED_KEY "$STATE/restricted.key"
    printf 'export %s=%q\n' CB_ADMIN_USER "$CB_ADMIN_USER"
    printf 'export %s=%q\n' CB_ADMIN_PASS "$CB_ADMIN_PASS"
    printf 'export %s=%q\n' CB_USER "$CB_USER"
    printf 'export %s=%q\n' CB_PASS "$CB_PASS"
  } > "$STATE/connection.env.tmp"
  chmod 600 "$STATE/connection.env.tmp"
  mv "$STATE/connection.env.tmp" "$STATE/connection.env"
}

cmd_up() {
  require docker; require curl; require openssl
  ensure_state
  # Check ownership before creating credentials or starting anything.
  if container_exists; then
    check_container_owner
    [ -s "$STATE/admin-password" ] || fail "Owned container has no saved administrator password; run down first"
  fi
  save_password "$STATE/admin-password"
  save_password "$STATE/user-password"
  save_password "$STATE/restricted-password"
  CB_ADMIN_PASS="$(cat "$STATE/admin-password")"
  CB_PASS="$(cat "$STATE/user-password")"
  RESTRICTED_PASS="$(cat "$STATE/restricted-password")"
  ensure_container
  wait_management
  initialize_cluster
  ensure_bucket
  ensure_certificates
  install_certificates
  wait_query
  write_connection_env
  printf 'Enterprise certificate fixture ready. Private connection settings: %s\n' "$STATE/connection.env"
}

cmd_down() {
  require docker
  check_state_paths
  if container_exists; then
    check_container_owner
    docker rm -f "$CONTAINER" >/dev/null
  fi
  if [ -d "$STATE" ]; then
    # Delete only our named artifacts, never recursively remove a user directory.
    local stem suffix file
    for stem in ca untrusted-ca server client unmapped expired untrusted restricted; do
      for suffix in key key.tmp pem pem.tmp csr srl; do
        rm -f "$STATE/$stem.$suffix"
      done
    done
    for file in admin-password user-password restricted-password \
      admin-password.tmp user-password.tmp restricted-password.tmp \
      ca.conf client.ext server.ext expired-ca.conf chain.pem 1000.pem \
      expired-index expired-index.attr expired-index.old expired-index.attr.old \
      expired-index.new expired-index.attr.new expired-serial.new \
      expired-serial expired-serial.old http-response connection.env connection.env.tmp; do
      rm -f "$STATE/$file"
    done
    # Leave unrelated contents, and their ownership marker, alone.
    local remaining=no
    for file in "$STATE"/* "$STATE"/.[!.]* "$STATE"/..?*; do
      [ -e "$file" ] || continue
      [ "$file" = "$STATE/.owner" ] || remaining=yes
    done
    if [ "$remaining" = no ]; then
      rm -f "$STATE/.owner"
      rmdir "$STATE"
    fi
  fi
  printf 'Enterprise certificate fixture removed (unrelated .certs contents preserved).\n'
}

case "${1:-}" in
  up) cmd_up ;;
  down) cmd_down ;;
  *) printf 'usage: %s {up|down}\n' "$0" >&2; exit 2 ;;
esac
