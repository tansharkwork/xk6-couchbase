#!/usr/bin/env bash
# Run the Enterprise certificate acceptance matrix against an already provisioned server.
set -uo pipefail

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd -P)" || exit 1
BIN="${BIN:-$ROOT/xk6-couchbase}"
EX_DIR="${EX_DIR:-$ROOT/examples}"
STATE="$ROOT/.certs/enterprise"
CONNECTION_ENV="$STATE/connection.env"

if [ -L "$ROOT/.certs" ] || [ -L "$STATE" ] || [ -L "$CONNECTION_ENV" ]; then
  echo "ERROR: Certificate state paths must not be symlinks." >&2
  exit 1
fi
if [ ! -f "$CONNECTION_ENV" ] || [ ! -r "$CONNECTION_ENV" ]; then
  echo "ERROR: Missing readable $CONNECTION_ENV. Provision Enterprise certificate fixtures first." >&2
  exit 1
fi
# Do not allow ambient variables to fill gaps in the provisioned fixture manifest.
unset CB_HOST CB_PASSWORD_HOST CB_USER CB_PASS CB_BUCKET CB_CA_CERT CB_CLIENT_CERT CB_CLIENT_KEY \
  CB_UNMAPPED_CERT CB_UNMAPPED_KEY CB_EXPIRED_CERT CB_EXPIRED_KEY \
  CB_UNTRUSTED_CERT CB_UNTRUSTED_KEY CB_RESTRICTED_CERT CB_RESTRICTED_KEY
if ! source "$CONNECTION_ENV"; then
  echo "ERROR: Could not load $CONNECTION_ENV." >&2
  exit 1
fi

if [ ! -f "$BIN" ] || [ ! -x "$BIN" ]; then
  echo "ERROR: $BIN not found or not executable. Run 'make build' first." >&2
  exit 1
fi
EXAMPLE="$EX_DIR/test-client-cert.js"
if [ ! -f "$EXAMPLE" ] || [ ! -r "$EXAMPLE" ]; then
  echo "ERROR: Missing readable example: $EXAMPLE" >&2
  exit 1
fi
for name in CB_HOST CB_PASSWORD_HOST CB_USER CB_PASS CB_BUCKET; do
  if [ -z "${!name:-}" ]; then
    echo "ERROR: $name is missing from $CONNECTION_ENV." >&2
    exit 1
  fi
done
for name in CB_CA_CERT CB_CLIENT_CERT CB_CLIENT_KEY \
  CB_UNMAPPED_CERT CB_UNMAPPED_KEY CB_EXPIRED_CERT CB_EXPIRED_KEY \
  CB_UNTRUSTED_CERT CB_UNTRUSTED_KEY CB_RESTRICTED_CERT CB_RESTRICTED_KEY; do
  path="${!name:-}"
  if [ -z "$path" ] || [ ! -f "$path" ] || [ ! -r "$path" ]; then
    echo "ERROR: $name must name a readable fixture file: $path" >&2
    exit 1
  fi
done

# k6 exit codes: 0 success, 99 thresholds failed (connected, but an operation
# was denied), 107 script exception (here: bucket readiness failed in init).
# Requiring the exact code keeps a broken fixture from passing a negative case.
EXIT_OK=0
EXIT_DENIED=99
EXIT_REJECTED=107

passed=0
failed=0
report() {
  local mode="$1" label="$2" status="$3" expected="$4" out="$5" ok="$6"
  if [ "$ok" = yes ]; then
    printf 'PASS  %-6s %-18s (exit %s)\n' "$mode" "$label" "$status"
    passed=$((passed + 1))
  else
    printf 'FAIL  %-6s %-18s (exit %s; expected %s)\n' "$mode" "$label" "$status" "$expected" >&2
    if [ -n "$out" ]; then
      printf '%s\n' "$out" >&2
    else
      echo '        k6 produced no diagnostics.' >&2
    fi
    failed=$((failed + 1))
  fi
}

run_cert_case() {
  local mode="$1" label="$2" cert="$3" key="$4" expected="$5"
  local out status ok=no
  # A clean child environment keeps passwords, admin credentials, ambient k6
  # options, and scope/collection overrides out of every acceptance execution.
  out=$(env -i PATH="$PATH" HOME="${HOME:-}" TMPDIR="${TMPDIR:-/tmp}" \
    "$BIN" run --quiet --include-system-env-vars=false --vus 2 --iterations 4 \
    -e "CB_HOST=$CB_HOST" -e "CB_BUCKET=$CB_BUCKET" -e "CB_CA_CERT=$CB_CA_CERT" \
    -e "CB_CLIENT_CERT=$cert" -e "CB_CLIENT_KEY=$key" \
    -e "CB_CONNECTION_MODE=$mode" "$EXAMPLE" 2>&1)
  status=$?
  [ "$status" -ne "$expected" ] || ok=yes
  report "$mode" "$label" "$status" "$expected" "$out" "$ok"
}

# Password authentication must keep working when certificate handling is
# "enable". These examples have no thresholds, so also reject logged errors.
# With a CA bundle, the example connects over TLS and verifies the server.
run_password_case() {
  local mode="$1" example="$2" label="$3" host="$4" ca="${5:-}"
  local out status ok=no
  out=$(env -i PATH="$PATH" HOME="${HOME:-}" TMPDIR="${TMPDIR:-/tmp}" \
    "$BIN" run --quiet --include-system-env-vars=false --vus 2 --iterations 4 \
    -e "CB_HOST=$host" -e "CB_USER=$CB_USER" -e "CB_PASS=$CB_PASS" \
    -e "CB_BUCKET=$CB_BUCKET" -e "CB_CA_CERT=$ca" "$EX_DIR/$example" 2>&1)
  status=$?
  if [ "$status" -eq "$EXIT_OK" ] && ! printf '%s\n' "$out" | grep -qiE 'level=error|GoError|panic'; then
    ok=yes
  fi
  report "$mode" "$label" "$status" "$EXIT_OK without errors" "$out" "$ok"
}

for mode in shared per-vu; do
  run_cert_case "$mode" mapped "$CB_CLIENT_CERT" "$CB_CLIENT_KEY" "$EXIT_OK"
  run_cert_case "$mode" unmapped "$CB_UNMAPPED_CERT" "$CB_UNMAPPED_KEY" "$EXIT_REJECTED"
  run_cert_case "$mode" expired "$CB_EXPIRED_CERT" "$CB_EXPIRED_KEY" "$EXIT_REJECTED"
  run_cert_case "$mode" untrusted "$CB_UNTRUSTED_CERT" "$CB_UNTRUSTED_KEY" "$EXIT_REJECTED"
  run_cert_case "$mode" "restricted (write)" "$CB_RESTRICTED_CERT" "$CB_RESTRICTED_KEY" "$EXIT_DENIED"
done
run_password_case shared test-new-with-shared-conn.js password "$CB_PASSWORD_HOST"
run_password_case per-vu test-new-with-conn-per-vu.js password "$CB_PASSWORD_HOST"
run_password_case shared test-new-with-shared-conn.js "password (TLS)" "$CB_HOST" "$CB_CA_CERT"
run_password_case per-vu test-new-with-conn-per-vu.js "password (TLS)" "$CB_HOST" "$CB_CA_CERT"
total=$((passed + failed))
printf 'Certificate acceptance: %s passed, %s failed (%s cases).\n' "$passed" "$failed" "$total"
if [ "$failed" -ne 0 ]; then
  exit 1
fi
