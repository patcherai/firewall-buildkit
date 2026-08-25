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

if command -v update-ca-certificates >/dev/null 2>&1; then
  mkdir -p /usr/local/share/ca-certificates
  cp "$cert" /usr/local/share/ca-certificates/depthfirst-firewall.crt
  chmod 0644 /usr/local/share/ca-certificates/depthfirst-firewall.crt
  update-ca-certificates >/dev/null
elif command -v update-ca-trust >/dev/null 2>&1; then
  mkdir -p /etc/pki/ca-trust/source/anchors
  cp "$cert" /etc/pki/ca-trust/source/anchors/depthfirst-firewall.crt
  chmod 0644 /etc/pki/ca-trust/source/anchors/depthfirst-firewall.crt
  update-ca-trust extract >/dev/null
elif [ -f /etc/ssl/certs/ca-certificates.crt ]; then
  mkdir -p /usr/local/share/ca-certificates
  cp "$cert" /usr/local/share/ca-certificates/depthfirst-firewall.crt
  chmod 0644 /usr/local/share/ca-certificates/depthfirst-firewall.crt
  cat "$cert" >> /etc/ssl/certs/ca-certificates.crt
elif [ -f /etc/ssl/cert.pem ]; then
  mkdir -p /usr/local/share/ca-certificates
  cp "$cert" /usr/local/share/ca-certificates/depthfirst-firewall.crt
  chmod 0644 /usr/local/share/ca-certificates/depthfirst-firewall.crt
  cat "$cert" >> /etc/ssl/cert.pem
else
  echo "depthfirst: unsupported CA store; install ca-certificates in the base image" >&2
  exit 1
fi
