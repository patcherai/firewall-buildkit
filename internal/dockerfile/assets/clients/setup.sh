# Sourced inside an authenticated RUN; all generated files live on tmpfs.
export PIPENV_PYPI_MIRROR="$PIP_INDEX_URL"
export MAVEN_ARGS="-gs /run/depthfirst/clients/maven-settings.xml ${MAVEN_ARGS:-}"
# Repository credentials must not be serialized into the project's configuration
# cache, which is outside the tmpfs mount. Keep ordinary artifact caches enabled.
export GRADLE_OPTS="${GRADLE_OPTS:-} -Dorg.gradle.configuration-cache=false"

# Maven 3.9+ (including mvnw) consumes MAVEN_ARGS. The PATH wrapper also
# supports older Maven launchers without rewriting user settings.xml.
if command -v mvn >/dev/null 2>&1; then
  export DEPTHFIRST_MVN="$(command -v mvn)"
  mkdir -p /run/depthfirst/bin || exit $?
  printf '%s\n' '#!/bin/sh' 'exec "$DEPTHFIRST_MVN" -gs /run/depthfirst/clients/maven-settings.xml "$@"' > /run/depthfirst/bin/mvn || exit $?
  chmod 755 /run/depthfirst/bin/mvn || exit $?
  export PATH="/run/depthfirst/bin:$PATH"
fi

# GRADLE_USER_HOME also covers ./gradlew. Keep caches and user configuration
# reachable while adding our init script only to the ephemeral home.
_df_gradle_home=${GRADLE_USER_HOME:-${HOME:-/root}/.gradle}
case "$_df_gradle_home" in
  /*) ;;
  *) _df_gradle_home="$PWD/$_df_gradle_home" ;;
esac
mkdir -p /run/depthfirst/gradle/init.d || exit $?
if [ -d "$_df_gradle_home" ]; then
  for _df_entry in "$_df_gradle_home"/* "$_df_gradle_home"/.[!.]* "$_df_gradle_home"/..?*; do
    [ -e "$_df_entry" ] || continue
    [ "${_df_entry##*/}" = init.d ] && continue
    ln -s "$_df_entry" "/run/depthfirst/gradle/${_df_entry##*/}" || exit $?
  done
  if [ -d "$_df_gradle_home/init.d" ]; then
    cp -R "$_df_gradle_home/init.d/." /run/depthfirst/gradle/init.d/ || exit $?
  fi
fi
cp /run/depthfirst/clients/gradle.init.gradle /run/depthfirst/gradle/init.d/depthfirst.init.gradle || exit $?
export GRADLE_USER_HOME=/run/depthfirst/gradle
unset _df_gradle_home _df_entry
