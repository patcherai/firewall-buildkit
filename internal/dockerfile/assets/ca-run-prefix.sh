if [ -f /usr/local/share/ca-certificates/depthfirst-firewall.crt ]; then
  _df_node_ca=/usr/local/share/ca-certificates/depthfirst-firewall.crt
elif [ -f /etc/pki/ca-trust/source/anchors/depthfirst-firewall.crt ]; then
  _df_node_ca=/etc/pki/ca-trust/source/anchors/depthfirst-firewall.crt
else
  _df_node_ca=
fi
if [ -n "$_df_node_ca" ]; then
  if [ -n "${NODE_EXTRA_CA_CERTS:-}" ] && [ -f "$NODE_EXTRA_CA_CERTS" ] && [ "$NODE_EXTRA_CA_CERTS" != "$_df_node_ca" ]; then
    cat "$NODE_EXTRA_CA_CERTS" "$_df_node_ca" > /run/depthfirst/node-extra-ca.crt || exit $?
    export NODE_EXTRA_CA_CERTS=/run/depthfirst/node-extra-ca.crt
  else
    export NODE_EXTRA_CA_CERTS="$_df_node_ca"
  fi
fi
unset _df_node_ca
if [ -f /etc/ssl/certs/ca-certificates.crt ]; then
  _df_ca_bundle=/etc/ssl/certs/ca-certificates.crt
elif [ -f /etc/ssl/cert.pem ]; then
  _df_ca_bundle=/etc/ssl/cert.pem
elif [ -f /etc/pki/ca-trust/extracted/pem/tls-ca-bundle.pem ]; then
  _df_ca_bundle=/etc/pki/ca-trust/extracted/pem/tls-ca-bundle.pem
elif [ -f /etc/pki/tls/certs/ca-bundle.crt ]; then
  _df_ca_bundle=/etc/pki/tls/certs/ca-bundle.crt
else
  _df_ca_bundle=
fi
if [ -n "$_df_ca_bundle" ]; then
  if [ -n "${SSL_CERT_FILE:-}" ] && [ -f "$SSL_CERT_FILE" ] && [ "$SSL_CERT_FILE" != "$_df_ca_bundle" ]; then
    cat "$SSL_CERT_FILE" "$_df_ca_bundle" > /run/depthfirst/tls-ca-bundle.pem || exit $?
    _df_ca_bundle=/run/depthfirst/tls-ca-bundle.pem
  fi
  export SSL_CERT_FILE="$_df_ca_bundle"
  export REQUESTS_CA_BUNDLE="$_df_ca_bundle"
  export PIP_CERT="$_df_ca_bundle"
  export CURL_CA_BUNDLE="$_df_ca_bundle"
  export BUNDLE_SSL_CA_CERT="$_df_ca_bundle"
  export GIT_SSL_CAINFO="$_df_ca_bundle"
fi
unset _df_ca_bundle
