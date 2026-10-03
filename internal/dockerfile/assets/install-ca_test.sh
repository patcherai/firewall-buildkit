#!/bin/sh
set -eu

assets=$(CDPATH= cd -- "$(dirname -- "$0")" && pwd)
scratch=$(mktemp -d)
trap 'rm -rf "$scratch"' EXIT
openssl req -x509 -newkey rsa:2048 -nodes -days 1 -subj /CN=firewall-test \
  -keyout "$scratch/key.pem" -out "$scratch/ca.crt" >/dev/null 2>&1

docker run --rm --network none \
  --mount "type=bind,src=$assets/install-ca.sh,dst=/install-ca.sh,readonly" \
  --mount "type=bind,src=$scratch/ca.crt,dst=/run/secrets/DF_FIREWALL_CA,readonly" \
  node:22-bookworm-slim sh -eu -c '
    bundle=/etc/ssl/certs/ca-certificates.crt
    cert=/run/secrets/DF_FIREWALL_CA
    ! command -v update-ca-certificates
    test ! -e "$bundle"

    if sh /install-ca.sh wrong-fingerprint 2>/tmp/ca-error; then exit 1; fi
    grep -q "does not match" /tmp/ca-error
    test ! -e "$bundle"

    expected=$(sha256sum "$cert")
    sh /install-ca.sh "${expected%% *}"
    cmp "$cert" "$bundle"
    cmp "$cert" /usr/local/share/ca-certificates/depthfirst-firewall.crt
    test "$(stat -c %a "$bundle")" = 644

    printf "EXISTING_CA\n" > "$bundle"
    sh /install-ca.sh "${expected%% *}"
    { printf "EXISTING_CA\n"; cat "$cert"; } > /tmp/expected-bundle
    cmp /tmp/expected-bundle "$bundle"

    rm "$bundle" /etc/debian_version
    if sh /install-ca.sh "${expected%% *}" 2>/tmp/ca-error; then exit 1; fi
    grep -q "unsupported CA store" /tmp/ca-error
    test ! -e "$bundle"
    printf "PASS: slim CA bootstrap, fingerprint validation, and existing trust preservation\n"
  '
