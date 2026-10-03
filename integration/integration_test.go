//go:build integration

// Package integration builds real Dockerfiles through the frontend image and
// checks the result inside real base images. It needs a Docker daemon and
// network access to pull base images.
//
//	go test -tags integration -count=1 ./integration
//
// DF_INTEGRATION_FRONTEND names an already-built frontend image. Without it the
// suite builds one from the repository root.
package integration

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/sha256"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/hex"
	"encoding/pem"
	"fmt"
	"math/big"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
	"time"
)

const localFrontend = "depthfirst-buildkit:integration"

var (
	frontend string
	certs    testCerts
)

func TestMain(m *testing.M) {
	if err := setup(); err != nil {
		fmt.Fprintf(os.Stderr, "integration setup: %v\n", err)
		os.Exit(1)
	}
	os.Exit(m.Run())
}

func setup() error {
	if output, err := exec.Command("docker", "version").CombinedOutput(); err != nil {
		return fmt.Errorf("docker is not available: %v\n%s", err, output)
	}
	var err error
	if certs, err = newTestCerts(); err != nil {
		return err
	}
	frontend = os.Getenv("DF_INTEGRATION_FRONTEND")
	if frontend != "" {
		return nil
	}
	frontend = localFrontend
	cmd := exec.Command("docker", "build", "-q", "-t", frontend, "..")
	if output, err := cmd.CombinedOutput(); err != nil {
		return fmt.Errorf("building frontend image: %v\n%s", err, output)
	}
	return nil
}

// Local Device Firewall mode: the CA secret and fingerprint, no API key.
func TestLocalCAInstall(t *testing.T) {
	tests := []struct {
		name       string
		dockerfile string
		markers    []string
	}{
		{
			name: "node slim without ca-certificates",
			dockerfile: `FROM node:24-slim
COPY fixture /fixture
RUN node /fixture/check-node-tls.js
RUN test ! -e /etc/ssl/certs/ca-certificates.crt && test -z "${SSL_CERT_FILE:-}" && echo no-system-store`,
			markers: []string{"node-tls-ok", "no-system-store"},
		},
		{
			name: "node slim installing ca-certificates later",
			dockerfile: `FROM node:24-slim
COPY fixture /fixture
RUN apt-get update -qq && apt-get install -y -qq --no-install-recommends ca-certificates >/dev/null
RUN sh /fixture/check-system-ca.sh && node /fixture/check-node-tls.js`,
			markers: []string{"system-ca-ok", "node-tls-ok"},
		},
		{
			name: "debian",
			dockerfile: `FROM node:24
COPY fixture /fixture
RUN sh /fixture/check-system-ca.sh && node /fixture/check-node-tls.js`,
			markers: []string{"system-ca-ok", "node-tls-ok"},
		},
		{
			name: "alpine",
			dockerfile: `FROM node:24-alpine
COPY fixture /fixture
RUN sh /fixture/check-system-ca.sh && node /fixture/check-node-tls.js`,
			markers: []string{"system-ca-ok", "node-tls-ok"},
		},
		{
			name: "ubuntu without ca-certificates",
			dockerfile: `FROM ubuntu:24.04
RUN test -s /usr/local/share/ca-certificates/depthfirst-firewall.crt && echo ca-file-ok`,
			markers: []string{"ca-file-ok"},
		},
		{
			name: "rhel",
			dockerfile: `FROM registry.access.redhat.com/ubi9/ubi
COPY fixture /fixture
RUN sh /fixture/check-system-ca.sh`,
			markers: []string{"system-ca-ok"},
		},
		{
			name: "python",
			dockerfile: `FROM python:3.13-slim
COPY fixture /fixture
RUN sh /fixture/check-system-ca.sh && test "$PIP_CERT" = "$SSL_CERT_FILE" && test "$REQUESTS_CA_BUNDLE" = "$SSL_CERT_FILE" && python3 /fixture/check-python-tls.py`,
			markers: []string{"system-ca-ok", "python-tls-ok"},
		},
		{
			name: "multi-stage with JSON-form RUN",
			dockerfile: `FROM node:24-alpine AS deps
COPY fixture /fixture
RUN ["node", "/fixture/check-node-tls.js"]
FROM deps
RUN node /fixture/check-node-tls.js`,
			markers: []string{"node-tls-ok"},
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			output, err := dockerBuild(t, buildSpec{dockerfile: test.dockerfile, ca: true})
			if err != nil {
				t.Fatalf("build failed: %v\n%s", err, output)
			}
			requireMarkers(t, output, test.markers...)
		})
	}
}

func TestLocalCAWithScratchFinalStage(t *testing.T) {
	t.Parallel()
	output, err := dockerBuild(t, buildSpec{ca: true, dockerfile: `FROM alpine AS build
RUN echo built > /built
FROM scratch
COPY --from=build /built /built`})
	if err != nil {
		t.Fatalf("build failed: %v\n%s", err, output)
	}
}

func TestLocalCARejectsWrongFingerprint(t *testing.T) {
	t.Parallel()
	output, err := dockerBuild(t, buildSpec{
		ca:          true,
		fingerprint: strings.Repeat("0", 64),
		dockerfile:  "FROM alpine\nRUN true",
	})
	if err == nil {
		t.Fatalf("build with a mismatched CA fingerprint succeeded\n%s", output)
	}
	requireMarkers(t, output, "depthfirst: DF_FIREWALL_CA does not match DF_FIREWALL_CA_SHA256")
}

// CI mode: the API key secret, no CA.
func TestCIConfiguresPackageManagers(t *testing.T) {
	t.Parallel()
	output, err := dockerBuild(t, buildSpec{apiKey: true, dockerfile: `FROM node:24-alpine
RUN test "$(npm config get registry)" = https://firewall.depthfirst.com/npm/ && echo npm-registry-ok
RUN grep -q '^//firewall.depthfirst.com/npm/:_authToken=' "$NPM_CONFIG_USERCONFIG" && echo npm-auth-ok
RUN case "$PIP_INDEX_URL" in "https://__token__:$DF_FIREWALL_API_KEY@firewall.depthfirst.com/pypi/simple/") echo pip-ok ;; *) exit 1 ;; esac
RUN ["sh", "-c", "test -n \"$GOPROXY\" && echo json-run-ok"]
RUN --network=none test -z "${DF_FIREWALL_API_KEY:-}" && test -z "${NPM_CONFIG_REGISTRY:-}" && echo network-none-untouched`})
	if err != nil {
		t.Fatalf("build failed: %v\n%s", err, output)
	}
	requireMarkers(t, output, "npm-registry-ok", "npm-auth-ok", "pip-ok", "json-run-ok", "network-none-untouched")
}

func TestCIKeepsAPIKeyOutOfImage(t *testing.T) {
	t.Parallel()
	out := t.TempDir()
	output, err := dockerBuild(t, buildSpec{apiKey: true, output: out, dockerfile: `FROM node:24-alpine
WORKDIR /app
RUN npm config get registry > registry && echo '{"name":"app","private":true}' > package.json`})
	if err != nil {
		t.Fatalf("build failed: %v\n%s", err, output)
	}
	found := 0
	err = filepath.Walk(out, func(path string, info os.FileInfo, err error) error {
		if err != nil || !info.Mode().IsRegular() {
			return err
		}
		data, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		found++
		if strings.Contains(string(data), testAPIKey) {
			t.Errorf("API key found in image file %s", strings.TrimPrefix(path, out))
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if found == 0 {
		t.Fatal("exported image has no files; the scan checked nothing")
	}
}

func TestCIRequiresAPIKeySecret(t *testing.T) {
	t.Parallel()
	output, err := dockerBuild(t, buildSpec{dockerfile: "FROM alpine\nRUN true"})
	if err == nil {
		t.Fatalf("build without DF_FIREWALL_API_KEY succeeded\n%s", output)
	}
	requireOutput(t, output, "secret DF_FIREWALL_API_KEY: not found")
}

func TestRejectsRunHeredoc(t *testing.T) {
	t.Parallel()
	output, err := dockerBuild(t, buildSpec{apiKey: true, dockerfile: "FROM alpine\nRUN <<EOF\necho hi\nEOF"})
	if err == nil {
		t.Fatalf("RUN heredoc was accepted\n%s", output)
	}
	requireOutput(t, output, "RUN heredocs are not supported")
}

const testAPIKey = "itest_key-5f3a9c"

type buildSpec struct {
	dockerfile  string
	ca          bool   // pass the test CA and its fingerprint
	fingerprint string // overrides the real fingerprint when set
	apiKey      bool   // pass DF_FIREWALL_API_KEY as a secret
	output      string // export the final stage to this directory
}

func dockerBuild(t *testing.T, spec buildSpec) (string, error) {
	t.Helper()
	dir := t.TempDir()
	dockerfile := "# syntax=" + frontend + "\n" + spec.dockerfile + "\n"
	writeFile(t, filepath.Join(dir, "Dockerfile"), dockerfile)
	certs.writeFixture(t, filepath.Join(dir, "fixture"))

	args := []string{"build", "--no-cache", "--progress=plain"}
	if spec.ca {
		fingerprint := spec.fingerprint
		if fingerprint == "" {
			fingerprint = certs.caFingerprint
		}
		args = append(args,
			"--secret", "id=DF_FIREWALL_CA,src="+filepath.Join(dir, "fixture", "ca.crt"),
			"--build-arg", "DF_FIREWALL_CA_SHA256="+fingerprint)
	}
	if spec.apiKey {
		args = append(args, "--secret", "id=DF_FIREWALL_API_KEY,env=DF_FIREWALL_API_KEY")
	}
	if spec.output != "" {
		args = append(args, "--output", "type=local,dest="+spec.output)
	}
	args = append(args, "-f", filepath.Join(dir, "Dockerfile"), dir)

	cmd := exec.Command("docker", args...)
	cmd.Env = withoutEnv(os.Environ(), "DF_FIREWALL_API_KEY")
	if spec.apiKey {
		cmd.Env = append(cmd.Env, "DF_FIREWALL_API_KEY="+testAPIKey)
	}
	output, err := cmd.CombinedOutput()
	return string(output), err
}

// requireMarkers checks lines printed by RUN processes. Plain progress also
// echoes each RUN's command text, so a substring match would find the echo.
func requireMarkers(t *testing.T, output string, markers ...string) {
	t.Helper()
	for _, marker := range markers {
		if !regexp.MustCompile(`(?m)^#\d+ \d+\.\d+ ` + regexp.QuoteMeta(marker) + `\r?$`).MatchString(output) {
			t.Errorf("no RUN printed %q\n%s", marker, output)
		}
	}
}

func requireOutput(t *testing.T, output, want string) {
	t.Helper()
	if !strings.Contains(output, want) {
		t.Errorf("build output does not contain %q\n%s", want, output)
	}
}

func withoutEnv(env []string, name string) []string {
	out := make([]string, 0, len(env))
	for _, entry := range env {
		if !strings.HasPrefix(entry, name+"=") {
			out = append(out, entry)
		}
	}
	return out
}

func writeFile(t *testing.T, path, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

// testCerts is a throwaway CA and a 127.0.0.1 leaf it signed. Checks start a
// TLS server with the leaf inside the build container and connect with the
// client's default trust, so passing proves the frontend installed the CA.
type testCerts struct {
	caPEM, leafPEM, leafKeyPEM string
	caFingerprint              string
}

func newTestCerts() (testCerts, error) {
	caKey, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		return testCerts{}, err
	}
	caTemplate := &x509.Certificate{
		SerialNumber:          randomSerial(),
		Subject:               pkix.Name{CommonName: "DepthFirst Integration Test CA"},
		NotBefore:             time.Now().Add(-time.Hour),
		NotAfter:              time.Now().Add(24 * time.Hour),
		IsCA:                  true,
		BasicConstraintsValid: true,
		KeyUsage:              x509.KeyUsageCertSign | x509.KeyUsageCRLSign,
	}
	caDER, err := x509.CreateCertificate(rand.Reader, caTemplate, caTemplate, &caKey.PublicKey, caKey)
	if err != nil {
		return testCerts{}, err
	}
	ca, err := x509.ParseCertificate(caDER)
	if err != nil {
		return testCerts{}, err
	}

	leafKey, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		return testCerts{}, err
	}
	leafTemplate := &x509.Certificate{
		SerialNumber: randomSerial(),
		Subject:      pkix.Name{CommonName: "127.0.0.1"},
		NotBefore:    time.Now().Add(-time.Hour),
		NotAfter:     time.Now().Add(24 * time.Hour),
		KeyUsage:     x509.KeyUsageDigitalSignature,
		ExtKeyUsage:  []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
		IPAddresses:  []net.IP{net.ParseIP("127.0.0.1")},
	}
	leafDER, err := x509.CreateCertificate(rand.Reader, leafTemplate, ca, &leafKey.PublicKey, caKey)
	if err != nil {
		return testCerts{}, err
	}
	leafKeyDER, err := x509.MarshalPKCS8PrivateKey(leafKey)
	if err != nil {
		return testCerts{}, err
	}

	caPEM := string(pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: caDER}))
	sum := sha256.Sum256([]byte(caPEM))
	return testCerts{
		caPEM:         caPEM,
		leafPEM:       string(pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: leafDER})),
		leafKeyPEM:    string(pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: leafKeyDER})),
		caFingerprint: hex.EncodeToString(sum[:]),
	}, nil
}

func randomSerial() *big.Int {
	serial, err := rand.Int(rand.Reader, new(big.Int).Lsh(big.NewInt(1), 128))
	if err != nil {
		panic(err)
	}
	return serial
}

func (c testCerts) writeFixture(t *testing.T, dir string) {
	t.Helper()
	writeFile(t, filepath.Join(dir, "ca.crt"), c.caPEM)
	writeFile(t, filepath.Join(dir, "leaf.crt"), c.leafPEM)
	writeFile(t, filepath.Join(dir, "leaf.key"), c.leafKeyPEM)
	writeFile(t, filepath.Join(dir, "check-node-tls.js"), checkNodeTLS)
	writeFile(t, filepath.Join(dir, "check-python-tls.py"), checkPythonTLS)
	writeFile(t, filepath.Join(dir, "check-system-ca.sh"), checkSystemCA)
}

const checkNodeTLS = `const fs = require('fs');
const https = require('https');
const server = https.createServer({
  key: fs.readFileSync('/fixture/leaf.key'),
  cert: fs.readFileSync('/fixture/leaf.crt'),
}, (req, res) => res.end('ok'));
server.listen(0, '127.0.0.1', () => {
  https.get({ host: '127.0.0.1', port: server.address().port, agent: false }, (res) => {
    res.resume();
    res.on('end', () => {
      console.log('node-tls-ok');
      server.close();
    });
  }).on('error', (err) => {
    console.error('node-tls-failed: ' + err.code);
    process.exit(1);
  });
});
`

const checkPythonTLS = `import http.server, ssl, threading, urllib.request

class Handler(http.server.BaseHTTPRequestHandler):
    def do_GET(self):
        self.send_response(200)
        self.end_headers()
        self.wfile.write(b"ok")

server = http.server.HTTPServer(("127.0.0.1", 0), Handler)
context = ssl.SSLContext(ssl.PROTOCOL_TLS_SERVER)
context.load_cert_chain("/fixture/leaf.crt", "/fixture/leaf.key")
server.socket = context.wrap_socket(server.socket, server_side=True)
threading.Thread(target=server.serve_forever, daemon=True).start()
urllib.request.urlopen("https://127.0.0.1:%d/" % server.server_port).read()
print("python-tls-ok")
`

// The second PEM line holds the random serial, so finding it in the bundle
// means this CA, not just any CA, is trusted.
const checkSystemCA = `set -eu
bundle=${SSL_CERT_FILE:?SSL_CERT_FILE is not set}
grep -qF "$(sed -n 2p /fixture/ca.crt)" "$bundle" || { echo "test CA is missing from $bundle" >&2; exit 1; }
if command -v openssl >/dev/null 2>&1; then
  openssl verify -CAfile "$bundle" /fixture/leaf.crt >/dev/null
fi
echo system-ca-ok
`
