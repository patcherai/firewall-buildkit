if [ -n "${DF_FIREWALL_API_KEY:-}" ]; then
  case "$DF_FIREWALL_API_KEY" in
    *[!A-Za-z0-9._~-]*) echo "depthfirst: DF_FIREWALL_API_KEY contains characters that cannot be safely placed in package registry URLs" >&2; exit 1 ;;
  esac
  _df_npmrc=/run/depthfirst/npmrc
  : > "$_df_npmrc"
  _df_userconfig=${NPM_CONFIG_USERCONFIG:-${npm_config_userconfig:-}}
  if [ -n "$_df_userconfig" ] && [ -f "$_df_userconfig" ] && [ "$_df_userconfig" != /run/depthfirst/firewall.npmrc ] && [ "$_df_userconfig" != "$_df_npmrc" ]; then
    cat "$_df_userconfig" >> "$_df_npmrc" || exit $?
    printf '\n' >> "$_df_npmrc"
  elif [ -f "${HOME:-}/.npmrc" ]; then
    cat "${HOME}/.npmrc" >> "$_df_npmrc" || exit $?
    printf '\n' >> "$_df_npmrc"
  fi
  cat /run/depthfirst/firewall.npmrc >> "$_df_npmrc" || exit $?
  export NPM_CONFIG_REGISTRY=https://firewall.depthfirst.com/npm/
  export NPM_CONFIG_ALWAYS_AUTH=true
  export NPM_CONFIG_USERCONFIG="$_df_npmrc"
  export YARN_NPM_REGISTRY_SERVER=https://firewall.depthfirst.com/npm/
  export YARN_NPM_AUTH_TOKEN="$DF_FIREWALL_API_KEY"
  export PIP_INDEX_URL="https://__token__:${DF_FIREWALL_API_KEY}@firewall.depthfirst.com/pypi/simple/"
  export UV_DEFAULT_INDEX="https://__token__:${DF_FIREWALL_API_KEY}@firewall.depthfirst.com/pypi/simple/"
  export UV_INDEX_URL="https://__token__:${DF_FIREWALL_API_KEY}@firewall.depthfirst.com/pypi/simple/"
  export POETRY_HTTP_BASIC_FIREWALL_USERNAME=__token__
  export POETRY_HTTP_BASIC_FIREWALL_PASSWORD="$DF_FIREWALL_API_KEY"
  export GOPROXY="https://__token__:${DF_FIREWALL_API_KEY}@firewall.depthfirst.com/go"
  export GOSUMDB=off
  printf '%s\n' ':sources:' "- https://__token__:${DF_FIREWALL_API_KEY}@firewall.depthfirst.com/rubygems" > /run/depthfirst/gemrc
  export GEMRC=/run/depthfirst/gemrc
  _df_bundle=/run/depthfirst/bundle-config
  _df_existing_bundle=${BUNDLE_USER_CONFIG:-}
  if [ -z "$_df_existing_bundle" ] && [ -f "${HOME:-}/.bundle/config" ]; then
    _df_existing_bundle=${HOME}/.bundle/config
  fi
  if [ -n "$_df_existing_bundle" ] && [ -f "$_df_existing_bundle" ] && [ "$_df_existing_bundle" != "$_df_bundle" ]; then
    cp "$_df_existing_bundle" "$_df_bundle" || exit $?
  else
    : > "$_df_bundle"
  fi
  export BUNDLE_USER_CONFIG="$_df_bundle"
  export BUNDLE_FIREWALL__DEPTHFIRST__COM="__token__:${DF_FIREWALL_API_KEY}"
  if command -v bundle >/dev/null 2>&1; then
    bundle config set --global mirror.https://rubygems.org https://firewall.depthfirst.com/rubygems >/dev/null || exit $?
  fi
  unset _df_npmrc _df_userconfig _df_bundle _df_existing_bundle
fi
