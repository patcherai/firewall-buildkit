package dockerfile

import (
	"context"
	"crypto/rand"
	"crypto/rsa"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"fmt"
	"math/big"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
)

func TestAdditionalClientsPreserveUserConfiguration(t *testing.T) {
	temp := t.TempDir()
	writeFirewallNpmrc(t, temp)
	home := filepath.Join(temp, "original-gradle")
	for _, dir := range []string{"init.d", "caches"} {
		if err := os.MkdirAll(filepath.Join(home, dir), 0700); err != nil {
			t.Fatal(err)
		}
	}
	for _, name := range []string{"gradle.properties", "init.d/custom.gradle", "caches/marker"} {
		if err := os.WriteFile(filepath.Join(home, name), []byte("original"), 0600); err != nil {
			t.Fatal(err)
		}
	}
	prefix := strings.ReplaceAll(apiRunPrefix, "/run/depthfirst", temp)
	cmd := exec.Command("/bin/sh", "-c", prefix+`printf '%s\n' "$PIPENV_PYPI_MIRROR" "$MAVEN_ARGS"; cat "$GRADLE_USER_HOME/gradle.properties" "$GRADLE_USER_HOME/init.d/custom.gradle" "$GRADLE_USER_HOME/caches/marker"`)
	cmd.Env = []string{"HOME=" + temp, "PATH=/usr/bin:/bin", "DF_FIREWALL_API_KEY=test-token", "GRADLE_USER_HOME=" + home, "MAVEN_ARGS=-B -Dcustom=true"}
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("%v: %s", err, out)
	}
	for _, want := range []string{"https://__token__:test-token@firewall.depthfirst.com/pypi/simple/", "-B -Dcustom=true", "originaloriginaloriginal"} {
		if !strings.Contains(string(out), want) {
			t.Fatalf("missing %q in %s", want, out)
		}
	}
	if _, err := os.Stat(filepath.Join(home, "init.d/depthfirst.init.gradle")); !os.IsNotExist(err) {
		t.Fatal("original Gradle home was modified")
	}
	data, err := os.ReadFile(filepath.Join(temp, "clients/maven-settings.xml"))
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(data), "test-token") || !strings.Contains(string(data), "${env.DF_FIREWALL_API_KEY}") {
		t.Fatal("Maven settings must reference the environment, not embed credentials")
	}
}

func TestJavaTrustStore(t *testing.T) {
	javaHome := os.Getenv("DEPTHFIRST_TEST_JAVA_HOME")
	if javaHome == "" {
		t.Skip("set DEPTHFIRST_TEST_JAVA_HOME to test a real JDK")
	}
	temp := t.TempDir()
	writeFirewallNpmrc(t, temp)
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatal(err)
	}
	cert := &x509.Certificate{SerialNumber: big.NewInt(1), Subject: pkix.Name{CommonName: "Test firewall CA"}, IsCA: true, BasicConstraintsValid: true, KeyUsage: x509.KeyUsageCertSign, NotBefore: time.Now().Add(-time.Hour), NotAfter: time.Now().Add(time.Hour)}
	der, err := x509.CreateCertificate(rand.Reader, cert, cert, &key.PublicKey, key)
	if err != nil {
		t.Fatal(err)
	}
	ca := filepath.Join(temp, "ca.pem")
	if err := os.WriteFile(ca, pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der}), 0600); err != nil {
		t.Fatal(err)
	}
	original := filepath.Join(javaHome, "lib/security/cacerts")
	before, err := os.ReadFile(original)
	if err != nil {
		t.Fatal(err)
	}
	cmd := exec.Command("/bin/sh", "-c", `. "$SCRIPT"; "$JAVA_HOME/bin/keytool" -list -alias depthfirst-firewall -keystore "$STORE" -storepass changeit`)
	cmd.Env = []string{"PATH=/usr/bin:/bin", "JAVA_HOME=" + javaHome, "_df_node_ca=" + ca, "SCRIPT=" + filepath.Join(temp, "clients/java-ca.sh"), "STORE=" + filepath.Join(temp, "java-cacerts")}
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("%v: %s", err, out)
	}
	after, err := os.ReadFile(original)
	if err != nil {
		t.Fatal(err)
	}
	if string(before) != string(after) {
		t.Fatal("base JDK trust store changed")
	}
	if !strings.Contains(string(out), "trustedCertEntry") {
		t.Fatalf("CA was not trusted: %s", out)
	}
	// A conflicting custom trust store must not be silently discarded.
	cmd = exec.Command("/bin/sh", "-c", `. "$SCRIPT"`)
	cmd.Env = []string{"PATH=/usr/bin:/bin", "JAVA_HOME=" + javaHome, "SCRIPT=" + filepath.Join(temp, "clients/java-ca.sh"), "JAVA_TOOL_OPTIONS=-Djavax.net.ssl.trustStore=/custom/store"}
	if err := cmd.Run(); err == nil {
		t.Fatal("custom trust store was silently replaced")
	}
}

func TestPackageManagerSetupWithoutAPIKey(t *testing.T) {
	temp := t.TempDir()
	prefix := strings.ReplaceAll(apiRunPrefix, "/run/depthfirst", temp)
	cmd := exec.Command("/bin/sh", "-c", prefix+`printf '%s\n' "$PATH" "${BUN_CONFIG_REGISTRY-unset}" "${CARGO_REGISTRIES_DEPTHFIRST_TOKEN-unset}"`)
	cmd.Env = []string{"HOME=" + temp, "PATH=/usr/bin:/bin"}
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("%v: %s", err, out)
	}
	if string(out) != "/usr/bin:/bin\nunset\nunset\n" {
		t.Fatalf("unexpected local-mode configuration: %s", out)
	}
	entries, err := os.ReadDir(temp)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 0 {
		t.Fatal("local mode created API client configuration")
	}
}

func TestAdditionalClientsUseMergedCABundle(t *testing.T) {
	temp := t.TempDir()
	bundle := filepath.Join(temp, "system.pem")
	existing := filepath.Join(temp, "existing.pem")
	if err := os.WriteFile(bundle, []byte("SYSTEM_ROOTS\nFIREWALL_CA\n"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(existing, []byte("CUSTOM_ROOTS\n"), 0600); err != nil {
		t.Fatal(err)
	}
	prefix := strings.ReplaceAll(caRunPrefix, "/run/depthfirst", temp)
	prefix = strings.ReplaceAll(prefix, "/etc/ssl/certs/ca-certificates.crt", bundle)
	cmd := exec.Command("/bin/sh", "-c", prefix+`test "$COMPOSER_CAFILE" = "$SSL_CERT_FILE" && test "$CONDA_SSL_VERIFY" = "$SSL_CERT_FILE" && test "$CARGO_HTTP_CAINFO" = "$SSL_CERT_FILE" && test "$CARGO_HTTP_PROXY_CAINFO" = "$SSL_CERT_FILE" && cat "$SSL_CERT_FILE"`)
	cmd.Env = []string{"PATH=/usr/bin:/bin", "SSL_CERT_FILE=" + existing}
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("%v: %s", err, out)
	}
	if string(out) != "CUSTOM_ROOTS\nSYSTEM_ROOTS\nFIREWALL_CA\n" {
		t.Fatalf("CA roots were lost: %s", out)
	}
}

func TestCargoWrapperPreservesArgumentsAndHome(t *testing.T) {
	for _, args := range []string{`build --locked`, `+stable run -- "two words" --flag`} {
		t.Run(args, func(t *testing.T) {
			temp := t.TempDir()
			writeFirewallNpmrc(t, temp)
			bin := filepath.Join(temp, "original-bin")
			if err := os.Mkdir(bin, 0700); err != nil {
				t.Fatal(err)
			}
			fake := "#!/bin/sh\nprintf '%s\\n' \"$CARGO_HOME\" \"$CARGO_REGISTRIES_DEPTHFIRST_TOKEN\" \"$@\"\n"
			if err := os.WriteFile(filepath.Join(bin, "cargo"), []byte(fake), 0700); err != nil {
				t.Fatal(err)
			}
			prefix := strings.ReplaceAll(apiRunPrefix, "/run/depthfirst", temp)
			cmd := exec.Command("/bin/sh", "-c", prefix+"cargo "+args)
			cmd.Env = []string{"HOME=" + temp, "PATH=" + bin + ":/usr/bin:/bin", "CARGO_HOME=/existing/cache", "DF_FIREWALL_API_KEY=test-token"}
			out, err := cmd.CombinedOutput()
			if err != nil {
				t.Fatalf("%v: %s", err, out)
			}
			want := "/existing/cache\ntest-token\n--config\n" + temp + "/cargo.toml\nbuild\n--locked\n"
			if strings.HasPrefix(args, "+") {
				want = "/existing/cache\ntest-token\n+stable\n--config\n" + temp + "/cargo.toml\nrun\n--\ntwo words\n--flag\n"
			}
			if string(out) != want {
				t.Fatalf("got %q, want %q", out, want)
			}
			config, err := os.ReadFile(filepath.Join(temp, "cargo.toml"))
			if err != nil {
				t.Fatal(err)
			}
			if strings.Contains(string(config), "test-token") {
				t.Fatal("token written to Cargo config")
			}
		})
	}
}

// Opt-in real-client checks against a local registry. No external downloads or
// real credentials are needed; a blocked package must reach our registry with auth.
func TestRealPackageManagersUseAuthenticatedRegistry(t *testing.T) {
	for _, manager := range []string{"bun", "cargo", "mvn", "gradle", "pipenv"} {
		t.Run(manager, func(t *testing.T) {
			binary := os.Getenv("DEPTHFIRST_TEST_" + strings.ToUpper(manager))
			if binary == "" {
				t.Skip("set DEPTHFIRST_TEST_" + strings.ToUpper(manager) + " to test a real client")
			}
			var mu sync.Mutex
			authenticated := false
			var server *httptest.Server
			server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.URL.Path == "/cratesio/config.json" {
					w.Header().Set("Content-Type", "application/json")
					fmt.Fprintf(w, `{"dl":%q,"auth-required":true}`, server.URL+"/cratesio/crates")
					return
				}
				user, password, basic := r.BasicAuth()
				valid := r.Header.Get("Authorization") == "test-token" || r.Header.Get("Authorization") == "Bearer test-token" || (basic && user == "__token__" && password == "test-token")
				mu.Lock()
				authenticated = authenticated || valid
				mu.Unlock()
				if !valid {
					w.Header().Set("WWW-Authenticate", `Basic realm="registry"`)
					w.WriteHeader(http.StatusUnauthorized)
					return
				}
				w.WriteHeader(http.StatusForbidden)
			}))
			defer server.Close()
			temp := t.TempDir()
			run := filepath.Join(temp, "run")
			if err := os.Mkdir(run, 0700); err != nil {
				t.Fatal(err)
			}
			writeFirewallNpmrc(t, run)
			entries, _ := os.ReadDir(filepath.Join(run, "clients"))
			for _, entry := range entries {
				path := filepath.Join(run, "clients", entry.Name())
				data, err := os.ReadFile(path)
				if err != nil {
					t.Fatal(err)
				}
				data = []byte(strings.ReplaceAll(string(data), "https://firewall.depthfirst.com", server.URL))
				// Gradle requires explicit opt-in for the test registry's HTTP transport.
				if entry.Name() == "gradle.init.gradle" {
					data = []byte(strings.ReplaceAll(string(data), "repo.credentials {", "repo.allowInsecureProtocol = true\n    repo.credentials {"))
				}
				if err := os.WriteFile(path, data, 0600); err != nil {
					t.Fatal(err)
				}
			}
			bin := filepath.Join(temp, "original-bin")
			if err := os.Mkdir(bin, 0700); err != nil {
				t.Fatal(err)
			}
			if err := os.Symlink(binary, filepath.Join(bin, manager)); err != nil {
				t.Fatal(err)
			}
			files := map[string]string{"package.json": `{"name":"probe","dependencies":{"depthfirst-probe":"1.0.0"}}`, "Cargo.toml": "[package]\nname = \"probe\"\nversion = \"0.1.0\"\n[dependencies]\ndepthfirst-probe = \"1.0.0\"\n", "src/lib.rs": "",
				"pom.xml":           `<project><modelVersion>4.0.0</modelVersion><groupId>test</groupId><artifactId>probe</artifactId><version>1.0</version></project>`,
				"settings.gradle":   `rootProject.name = 'probe'`,
				"gradle.properties": "org.gradle.configuration-cache=true\n",
				"build.gradle":      gradleRegistryProbe,
				"Pipfile":           "[[source]]\nurl = \"https://pypi.org/simple\"\nverify_ssl = true\nname = \"pypi\"\n[packages]\ndepthfirst-probe = \"==1.0.0\"\n",
			}
			if err := os.Mkdir(filepath.Join(temp, "src"), 0700); err != nil {
				t.Fatal(err)
			}
			for name, body := range files {
				if err := os.WriteFile(filepath.Join(temp, name), []byte(body), 0600); err != nil {
					t.Fatal(err)
				}
			}
			prefix := strings.ReplaceAll(apiRunPrefix, "/run/depthfirst", run)
			prefix = strings.ReplaceAll(prefix, "firewall.depthfirst.com", strings.TrimPrefix(server.URL, "http://"))
			prefix = strings.ReplaceAll(prefix, "https://", "http://")
			command := "bun install --ignore-scripts"
			switch manager {
			case "cargo":
				command = "cargo fetch"
			case "mvn":
				command = "mvn -B -ntp package"
			case "gradle":
				command = "gradle --no-daemon --console=plain resolveProbe"
			case "pipenv":
				command = "pipenv lock"
			}
			ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
			defer cancel()
			cmd := exec.CommandContext(ctx, "/bin/sh", "-c", prefix+command)
			cmd.Dir = temp
			rustupHome := os.Getenv("RUSTUP_HOME")
			if rustupHome == "" {
				rustupHome = filepath.Join(os.Getenv("HOME"), ".rustup")
			}
			cmd.Env = []string{"HOME=" + temp, "CARGO_HOME=" + temp + "/cargo-home", "RUSTUP_HOME=" + rustupHome, "PATH=" + bin + ":" + filepath.Dir(binary) + ":/usr/bin:/bin", "DF_FIREWALL_API_KEY=test-token", "CARGO_NET_RETRY=0", "JAVA_HOME=" + os.Getenv("JAVA_HOME"), "DEPTHFIRST_TEST_REGISTRY=" + server.URL, "PIPENV_NOSPIN=1", "PIPENV_VENV_IN_PROJECT=1"}
			out, err := cmd.CombinedOutput()
			if ctx.Err() != nil {
				t.Fatalf("client timed out: %s", out)
			}
			if err == nil {
				t.Fatal("blocked install unexpectedly succeeded")
			}
			mu.Lock()
			gotAuth := authenticated
			mu.Unlock()
			if manager == "gradle" {
				if _, err := os.Stat(filepath.Join(temp, ".gradle/configuration-cache")); !os.IsNotExist(err) {
					t.Fatal("Gradle configuration cache was not disabled")
				}
			}
			if !gotAuth {
				t.Fatalf("no authenticated package request reached registry: %s", out)
			}
		})
	}
}

// Exercise actual Gradle repository objects before any dependency resolution so
// a routing regression fails locally instead of contacting the public registry.
const gradleRegistryProbe = `
plugins { id 'java' }
repositories { maven { url = 'https://repo.maven.apache.org:443/maven2' } }
dependencies { implementation 'test:depthfirst-probe:1.0' }
def changedRepo = repositories.mavenCentral()
changedRepo.url = 'https://private.invalid/maven'
changedRepo.content { excludeGroupByRegex '.*' }

def probes = []
[
    ['repo.maven.apache.org', '/maven2', 'mavenCentral'],
    ['repo1.maven.org', '/maven2', 'mavenCentral'],
    ['jitpack.io', '', 'jitpack']
].each { host, path, route ->
    ['http', 'https'].each { scheme ->
        def defaultPort = scheme == 'https' ? 443 : 80
        [-1, defaultPort, 8443, scheme == 'https' ? 80 : 443].each { port ->
            def original = "${scheme}://${host}${port == -1 ? '' : ':' + port}${path}/".toString()
            def expected = port == -1 || port == defaultPort ?
                System.getenv('DEPTHFIRST_TEST_REGISTRY') + '/' + route : original
            def repo = repositories.maven {
                url = original
                content { excludeGroupByRegex '.*' }
            }
            probes << [repo, expected, expected != original]
        }
    }
}
['https://private.invalid:443/maven2', 'https://repo.maven.apache.org:443/private'].each { original ->
    def repo = repositories.maven {
        url = original
        content { excludeGroupByRegex '.*' }
    }
    probes << [repo, original, false]
}
tasks.register('resolveProbe') {
    doLast {
        assert changedRepo.credentials.password != System.getenv('DF_FIREWALL_API_KEY')
        probes.each { repo, expected, protectedRepo ->
            assert repo.url.toString() == expected : "Unexpected route for ${repo.name}: ${repo.url}, expected ${expected}"
            if (protectedRepo) {
                assert repo.credentials.username == '__token__'
                assert repo.credentials.password == System.getenv('DF_FIREWALL_API_KEY')
            } else {
                assert repo.credentials.password != System.getenv('DF_FIREWALL_API_KEY')
            }
            // These repositories test configuration only. Keep resolution on
            // the primary explicit-default-port repository and local server.
            repositories.remove(repo)
        }
        configurations.runtimeClasspath.files
    }
}
`
