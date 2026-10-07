#!/bin/sh
set -eu

cert=/run/secrets/DF_FIREWALL_CA
if [ ! -s "$cert" ]; then
  exit 0
fi

expected=${1:-}
if command -v sha256sum >/dev/null 2>&1; then
  actual=$(sha256sum "$cert")
  actual=${actual%% *}
elif command -v openssl >/dev/null 2>&1; then
  actual=$(openssl dgst -sha256 "$cert")
  actual=${actual##* }
else
  echo "depthfirst: cannot verify DF_FIREWALL_CA because neither sha256sum nor openssl is installed" >&2
  exit 1
fi
if [ "$actual" != "$expected" ]; then
  echo "depthfirst: DF_FIREWALL_CA does not match DF_FIREWALL_CA_SHA256" >&2
  exit 1
fi

if [ "$(id -u)" != 0 ]; then
  echo "depthfirst: CA installation requires the current Dockerfile stage to run as root" >&2
  exit 1
fi

install_cert() {
  mkdir -p "$1"
  cp "$cert" "$1/depthfirst-firewall.crt"
  chmod 0644 "$1/depthfirst-firewall.crt"
}

if command -v update-ca-certificates >/dev/null 2>&1; then
  install_cert /usr/local/share/ca-certificates
  update-ca-certificates >/dev/null
elif command -v update-ca-trust >/dev/null 2>&1; then
  install_cert /etc/pki/ca-trust/source/anchors
  update-ca-trust extract >/dev/null
else
  # Slim images such as node:*-slim ship without ca-certificates. Node still
  # finds the CA here through NODE_EXTRA_CA_CERTS, and update-ca-certificates
  # adds it to the system bundle if a later RUN installs ca-certificates.
  install_cert /usr/local/share/ca-certificates
  if [ -f /etc/ssl/certs/ca-certificates.crt ]; then
    cat "$cert" >> /etc/ssl/certs/ca-certificates.crt
  elif [ -f /etc/ssl/cert.pem ]; then
    cat "$cert" >> /etc/ssl/cert.pem
  fi
fi
