# JAVA_HOME identifies the JDK used by Maven/Gradle. Keep its public roots and
# add the Device Firewall CA to a per-RUN copy, never mutate the base JDK.
case "${JAVA_TOOL_OPTIONS:-} ${JDK_JAVA_OPTIONS:-} ${_JAVA_OPTIONS:-} ${MAVEN_OPTS:-} ${GRADLE_OPTS:-}" in
  *javax.net.ssl.trustStore*)
    echo "depthfirst: custom Java trustStore options require manually adding the Device Firewall CA; automatic trust-store merging is unavailable" >&2
    exit 1
    ;;
esac
if [ -f "$JAVA_HOME/lib/security/cacerts" ]; then
  _df_java_store="$JAVA_HOME/lib/security/cacerts"
elif [ -f "$JAVA_HOME/jre/lib/security/cacerts" ]; then
  _df_java_store="$JAVA_HOME/jre/lib/security/cacerts"
else
  echo "depthfirst: cannot find JAVA_HOME's default CA store" >&2
  exit 1
fi
cp "$_df_java_store" /run/depthfirst/java-cacerts || exit $?
chmod 600 /run/depthfirst/java-cacerts || exit $?
"$JAVA_HOME/bin/keytool" -delete -alias depthfirst-firewall -keystore /run/depthfirst/java-cacerts -storepass changeit >/dev/null 2>&1 || :
"$JAVA_HOME/bin/keytool" -importcert -noprompt -alias depthfirst-firewall -file "$_df_node_ca" -keystore /run/depthfirst/java-cacerts -storepass changeit >/dev/null 2>&1 || {
  echo "depthfirst: cannot add the firewall CA to the Java trust store (expected default password changeit)" >&2
  exit 1
}
export JAVA_TOOL_OPTIONS="${JAVA_TOOL_OPTIONS:-} -Djavax.net.ssl.trustStore=/run/depthfirst/java-cacerts -Djavax.net.ssl.trustStorePassword=changeit"
unset _df_java_store
