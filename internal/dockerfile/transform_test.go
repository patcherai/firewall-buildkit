package dockerfile

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/moby/buildkit/frontend/dockerfile/parser"
)

func TestTransformProtectsEveryStageAndRun(t *testing.T) {
	source := []byte("ARG BASE=alpine\nFROM ${BASE} AS build\nRUN apk add nodejs\nFROM build\nRUN [\"npm\",\"ci\"]\n")
	fingerprint := strings.Repeat("a", 64)
	got, err := Transform(source, "Dockerfile", Options{CAFingerprint: fingerprint})
	if err != nil {
		t.Fatal(err)
	}
	text := string(got)
	if _, err := parser.Parse(bytes.NewReader(got)); err != nil {
		t.Fatalf("generated Dockerfile does not parse: %v\n%s", err, got)
	}
	if strings.Index(text, "ARG BASE=alpine") > strings.Index(text, "FROM scratch AS "+reservedStage) {
		t.Fatal("global ARG moved after the injected stage")
	}
	if count := strings.Count(text, caInstallCommand); count != 2 {
		t.Fatalf("CA install count = %d, want 2\n%s", count, text)
	}
	if count := strings.Count(text, "id=DF_FIREWALL_API_KEY"); count != 2 {
		t.Fatalf("API key mount count = %d, want 2\n%s", count, text)
	}
	if !strings.Contains(text, `exec \"$@\"","depthfirst","npm","ci"]`) {
		t.Fatalf("JSON RUN was not wrapped safely\n%s", text)
	}
}

func TestTransformInstallsCAOnlyInStagesWithProtectedRun(t *testing.T) {
	source := []byte(strings.Join([]string{
		"FROM golang AS build",
		"USER builder",
		"RUN go build -o /app .",
		"FROM alpine AS offline",
		"RUN --network=none true",
		"FROM scratch",
		"COPY --from=build /app /app",
		"",
	}, "\n"))
	got, err := Transform(source, "Dockerfile", Options{CAFingerprint: strings.Repeat("c", 64)})
	if err != nil {
		t.Fatal(err)
	}
	text := string(got)
	if count := strings.Count(text, caInstallCommand); count != 1 {
		t.Fatalf("CA install count = %d, want 1\n%s", count, text)
	}
	if !strings.Contains(text, "FROM golang AS build\n"+caInstallCommand) {
		t.Fatalf("CA install must directly follow FROM, before USER\n%s", text)
	}
}

func TestTransformRewritesSyntaxDirective(t *testing.T) {
	source := []byte("# syntax=depthfirst-buildkit:dev\n# escape=`\nFROM alpine\nRUN echo hi\n")
	got, err := Transform(source, "Dockerfile", Options{})
	if err != nil {
		t.Fatal(err)
	}
	text := string(got)
	if strings.Contains(text, "depthfirst-buildkit") {
		t.Fatalf("original syntax directive was left in place\n%s", text)
	}
	if !strings.HasPrefix(text, "# syntax="+DelegatedSyntax+"\n") {
		t.Fatalf("delegated syntax was not written\n%s", text)
	}
	if !strings.Contains(text, "# escape=`") {
		t.Fatalf("escape directive was dropped\n%s", text)
	}
	if strings.Index(text, "# syntax=") > strings.Index(text, "# escape=`") {
		t.Fatalf("syntax directive is not first\n%s", text)
	}
}

func TestTransformPreservesRunOptions(t *testing.T) {
	source := []byte("FROM alpine\nRUN --mount=type=cache,target=/root/.npm --network=host npm ci\nRUN --network=none echo offline\n")
	got, err := Transform(source, "Dockerfile", Options{})
	if err != nil {
		t.Fatal(err)
	}
	text := string(got)
	if !strings.Contains(text, "RUN --mount=type=cache,target=/root/.npm --network=host "+runMountPrefix+"true ") {
		t.Fatalf("RUN options were not preserved\n%s", text)
	}
	if !strings.Contains(text, "RUN --network=none echo offline") {
		t.Fatalf("network-none RUN was changed\n%s", text)
	}
}

func TestTransformUsesParsedNetworkFlag(t *testing.T) {
	for _, test := range []struct {
		name    string
		flags   string
		offline bool
	}{
		{"unquoted none", "--network=none", true},
		{"double-quoted none", `--network="none"`, true},
		{"single-quoted none", "--network='none'", true},
		{"cache ID containing network option", "--mount=type=cache,id=cache--network=none,target=/cache", false},
	} {
		t.Run(test.name, func(t *testing.T) {
			line := "RUN " + test.flags + " echo hello"
			got, err := Transform([]byte("FROM alpine\n"+line+"\n"), "Dockerfile", Options{CAFingerprint: strings.Repeat("d", 64)})
			if err != nil {
				t.Fatal(err)
			}
			text := string(got)
			if test.offline {
				if !strings.Contains(text, line+"\n") || strings.Contains(text, caInstallCommand) || strings.Contains(text, "id=DF_FIREWALL_API_KEY") {
					t.Fatalf("offline RUN must remain unchanged and need no CA installation or API key mount\n%s", text)
				}
			} else if !strings.Contains(text, caInstallCommand) || !strings.Contains(text, "RUN "+test.flags+" "+runMountPrefix) {
				t.Fatalf("network-enabled RUN must keep its flags and receive CA installation and setup\n%s", text)
			}
		})
	}
}

func TestTransformRejectsUnsupportedInput(t *testing.T) {
	tests := []struct {
		name   string
		source string
		want   string
	}{
		{"heredoc", "FROM alpine\nRUN <<EOF\necho hello\nEOF\n", "RUN heredocs"},
		{"reserved stage", "FROM alpine AS depthfirst_assets\n", "is reserved"},
		{"no stage", "RUN echo hello\n", "no FROM"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			_, err := Transform([]byte(test.source), "Dockerfile", Options{})
			if err == nil || !strings.Contains(err.Error(), test.want) {
				t.Fatalf("error = %v, want containing %q", err, test.want)
			}
		})
	}
}

func TestTransformSupportsLineContinuations(t *testing.T) {
	source := []byte(strings.Join([]string{
		"FROM alpine",
		"RUN npm install \\",
		"  left-pad \\",
		"  is-number",
		"",
	}, "\n"))
	got, err := Transform(source, "Dockerfile", Options{})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(got), apiRunPrefix+"npm install   left-pad   is-number") {
		t.Fatalf("continued RUN was not transformed\n%s", got)
	}
}

func TestTransformConfiguresSupportedPackageManagersEphemerally(t *testing.T) {
	got, err := Transform([]byte("FROM alpine\nRUN install-dependencies\n"), "Dockerfile", Options{})
	if err != nil {
		t.Fatal(err)
	}
	text := string(got)
	for _, want := range []string{
		"target=/run/depthfirst",
		"target=/run/depthfirst/firewall.npmrc",
		"NPM_CONFIG_REGISTRY=https://firewall.depthfirst.com/npm/",
		"NPM_CONFIG_ALWAYS_AUTH=true",
		`PIP_INDEX_URL="https://__token__:${DF_FIREWALL_API_KEY}@firewall.depthfirst.com/pypi/simple/"`,
		`UV_DEFAULT_INDEX="https://__token__:${DF_FIREWALL_API_KEY}@firewall.depthfirst.com/pypi/simple/"`,
		`POETRY_HTTP_BASIC_FIREWALL_PASSWORD="$DF_FIREWALL_API_KEY"`,
		`GOPROXY="https://__token__:${DF_FIREWALL_API_KEY}@firewall.depthfirst.com/go"`,
		"GOSUMDB=off",
		"GEMRC=/run/depthfirst/gemrc",
		`BUNDLE_FIREWALL__DEPTHFIRST__COM="__token__:${DF_FIREWALL_API_KEY}"`,
		"mirror.https://rubygems.org https://firewall.depthfirst.com/rubygems",
	} {
		if !strings.Contains(text, want) {
			t.Errorf("generated Dockerfile does not contain %q\n%s", want, text)
		}
	}
	if strings.Contains(assetStage, "DF_FIREWALL_API_KEY=") {
		t.Fatal("asset stage must not contain an API key value")
	}
}

func TestTransformExposesInstalledCABundleToLanguageClients(t *testing.T) {
	fingerprint := strings.Repeat("b", 64)
	got, err := Transform([]byte("FROM alpine\nRUN install-dependencies\n"), "Dockerfile", Options{CAFingerprint: fingerprint})
	if err != nil {
		t.Fatal(err)
	}
	text := string(got)
	for _, want := range []string{
		"SSL_CERT_FILE=",
		"PIP_CERT=",
		"BUNDLE_SSL_CA_CERT=",
		"NODE_EXTRA_CA_CERTS=",
	} {
		if !strings.Contains(text, want) {
			t.Errorf("generated Dockerfile does not expose CA through %q\n%s", want, text)
		}
	}
}

func TestAPIRunPrefixCreatesOnlyEphemeralRubyConfiguration(t *testing.T) {
	temp := t.TempDir()
	writeFirewallNpmrc(t, temp)
	bin := filepath.Join(temp, "bin")
	if err := os.Mkdir(bin, 0o700); err != nil {
		t.Fatal(err)
	}
	bundleLog := filepath.Join(temp, "bundle.log")
	fakeBundle := []byte("#!/bin/sh\nprintf '%s\\n' \"$*\" > \"$DF_BUNDLE_LOG\"\n")
	if err := os.WriteFile(filepath.Join(bin, "bundle"), fakeBundle, 0o700); err != nil {
		t.Fatal(err)
	}

	prefix := strings.ReplaceAll(apiRunPrefix, "/run/depthfirst", temp)
	command := prefix + `printf '%s\n' "$NPM_CONFIG_REGISTRY" "$PIP_INDEX_URL" "$UV_DEFAULT_INDEX" "$POETRY_HTTP_BASIC_FIREWALL_USERNAME" "$POETRY_HTTP_BASIC_FIREWALL_PASSWORD" "$GOPROXY" "$GOSUMDB" "$GEMRC" "$BUNDLE_USER_CONFIG" "$BUNDLE_FIREWALL__DEPTHFIRST__COM"`
	cmd := exec.Command("/bin/sh", "-c", command)
	cmd.Env = []string{
		"DF_FIREWALL_API_KEY=test_token-123",
		"DF_BUNDLE_LOG=" + bundleLog,
		"HOME=" + temp,
		"PATH=" + bin + ":/usr/bin:/bin",
	}
	output, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("run prefix failed: %v\n%s", err, output)
	}
	for _, want := range []string{
		"https://firewall.depthfirst.com/npm/",
		"https://__token__:test_token-123@firewall.depthfirst.com/pypi/simple/",
		"https://__token__:test_token-123@firewall.depthfirst.com/go",
		"__token__:test_token-123",
	} {
		if !strings.Contains(string(output), want) {
			t.Errorf("configured environment does not contain %q\n%s", want, output)
		}
	}
	gemrc, err := os.ReadFile(filepath.Join(temp, "gemrc"))
	if err != nil {
		t.Fatal(err)
	}
	if want := "https://__token__:test_token-123@firewall.depthfirst.com/rubygems"; !strings.Contains(string(gemrc), want) {
		t.Fatalf("gemrc does not contain %q\n%s", want, gemrc)
	}
	bundleArgs, err := os.ReadFile(bundleLog)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(bundleArgs), "mirror.https://rubygems.org https://firewall.depthfirst.com/rubygems") {
		t.Fatalf("Bundler mirror was not configured\n%s", bundleArgs)
	}
	if strings.Contains(string(bundleArgs), "test_token-123") {
		t.Fatalf("API key was passed in Bundler command arguments\n%s", bundleArgs)
	}
}

func TestAPIRunPrefixRejectsUnsafeURLCredentials(t *testing.T) {
	temp := t.TempDir()
	prefix := strings.ReplaceAll(apiRunPrefix, "/run/depthfirst", temp)
	cmd := exec.Command("/bin/sh", "-c", prefix+"exit 0")
	cmd.Env = []string{"DF_FIREWALL_API_KEY=unsafe:token", "PATH=/usr/bin:/bin"}
	output, err := cmd.CombinedOutput()
	if err == nil {
		t.Fatalf("unsafe API key was accepted\n%s", output)
	}
	if !strings.Contains(string(output), "cannot be safely placed") {
		t.Fatalf("unexpected error for unsafe API key\n%s", output)
	}
}

func TestAPIRunPrefixMergesExistingNpmrc(t *testing.T) {
	temp := t.TempDir()
	writeFirewallNpmrc(t, temp)
	home := filepath.Join(temp, "home")
	if err := os.Mkdir(home, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(home, ".npmrc"), []byte("legacy-peer-deps=true\nfund=false\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	prefix := strings.ReplaceAll(apiRunPrefix, "/run/depthfirst", temp)
	cmd := exec.Command("/bin/sh", "-c", prefix+`printf '%s\n' "$NPM_CONFIG_USERCONFIG"`)
	cmd.Env = []string{
		"DF_FIREWALL_API_KEY=test_token-123",
		"HOME=" + home,
		"PATH=/usr/bin:/bin",
	}
	output, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("run prefix failed: %v\n%s", err, output)
	}
	mergedPath := strings.TrimSpace(string(output))
	if mergedPath != filepath.Join(temp, "npmrc") {
		t.Fatalf("NPM_CONFIG_USERCONFIG = %q, want merged tmpfs npmrc", mergedPath)
	}
	merged, err := os.ReadFile(mergedPath)
	if err != nil {
		t.Fatal(err)
	}
	text := string(merged)
	for _, want := range []string{
		"legacy-peer-deps=true",
		"fund=false",
		"registry=https://firewall.depthfirst.com/npm/",
		"always-auth=true",
	} {
		if !strings.Contains(text, want) {
			t.Errorf("merged npmrc does not contain %q\n%s", want, text)
		}
	}
	if idxUser, idxFirewall := strings.Index(text, "legacy-peer-deps=true"), strings.Index(text, "registry=https://firewall.depthfirst.com/npm/"); idxUser < 0 || idxFirewall < 0 || idxUser > idxFirewall {
		t.Fatalf("firewall npmrc must be appended after existing userconfig\n%s", text)
	}
}

func TestAPIRunPrefixPrefersExistingUserconfigOverHomeNpmrc(t *testing.T) {
	temp := t.TempDir()
	writeFirewallNpmrc(t, temp)
	home := filepath.Join(temp, "home")
	if err := os.Mkdir(home, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(home, ".npmrc"), []byte("fund=true\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	userconfig := filepath.Join(temp, "user.npmrc")
	if err := os.WriteFile(userconfig, []byte("legacy-peer-deps=true\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	prefix := strings.ReplaceAll(apiRunPrefix, "/run/depthfirst", temp)
	cmd := exec.Command("/bin/sh", "-c", prefix+`cat "$NPM_CONFIG_USERCONFIG"`)
	cmd.Env = []string{
		"DF_FIREWALL_API_KEY=test_token-123",
		"HOME=" + home,
		"NPM_CONFIG_USERCONFIG=" + userconfig,
		"PATH=/usr/bin:/bin",
	}
	output, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("run prefix failed: %v\n%s", err, output)
	}
	text := string(output)
	if !strings.Contains(text, "legacy-peer-deps=true") {
		t.Fatalf("existing NPM_CONFIG_USERCONFIG was not merged\n%s", text)
	}
	if strings.Contains(text, "fund=true") {
		t.Fatalf("HOME .npmrc was merged even though NPM_CONFIG_USERCONFIG was set\n%s", text)
	}
	if !strings.Contains(text, "registry=https://firewall.depthfirst.com/npm/") {
		t.Fatalf("firewall npmrc was not appended\n%s", text)
	}
}

func TestAPIRunPrefixIgnoresMissingUserconfig(t *testing.T) {
	temp := t.TempDir()
	writeFirewallNpmrc(t, temp)
	prefix := strings.ReplaceAll(apiRunPrefix, "/run/depthfirst", temp)
	cmd := exec.Command("/bin/sh", "-c", prefix+`cat "$NPM_CONFIG_USERCONFIG"`)
	cmd.Env = []string{
		"DF_FIREWALL_API_KEY=test_token-123",
		"HOME=" + filepath.Join(temp, "missing-home"),
		"NPM_CONFIG_USERCONFIG=" + filepath.Join(temp, "missing.npmrc"),
		"PATH=/usr/bin:/bin",
	}
	output, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("missing userconfig should be ignored: %v\n%s", err, output)
	}
	if !strings.Contains(string(output), "registry=https://firewall.depthfirst.com/npm/") {
		t.Fatalf("firewall npmrc was not used when userconfig was missing\n%s", output)
	}
}

func TestAPIRunPrefixKeepsExistingBundleUserConfig(t *testing.T) {
	temp := t.TempDir()
	writeFirewallNpmrc(t, temp)
	existing := filepath.Join(temp, "existing-bundle-config")
	if err := os.WriteFile(existing, []byte("BUNDLE_JOBS: \"4\"\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	prefix := strings.ReplaceAll(apiRunPrefix, "/run/depthfirst", temp)
	cmd := exec.Command("/bin/sh", "-c", prefix+`cat "$BUNDLE_USER_CONFIG"`)
	cmd.Env = []string{
		"DF_FIREWALL_API_KEY=test_token-123",
		"BUNDLE_USER_CONFIG=" + existing,
		"HOME=" + temp,
		"PATH=/usr/bin:/bin",
	}
	output, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("run prefix failed: %v\n%s", err, output)
	}
	if !strings.Contains(string(output), `BUNDLE_JOBS: "4"`) {
		t.Fatalf("existing BUNDLE_USER_CONFIG was not preserved\n%s", output)
	}
}

func TestCARunPrefixKeepsExistingNodeExtraCAs(t *testing.T) {
	temp := t.TempDir()
	existing := filepath.Join(temp, "existing.pem")
	firewall := filepath.Join(temp, "depthfirst-firewall.crt")
	if err := os.WriteFile(existing, []byte("EXISTING_CA\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(firewall, []byte("FIREWALL_CA\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	prefix := strings.ReplaceAll(caRunPrefix, "/usr/local/share/ca-certificates/depthfirst-firewall.crt", firewall)
	prefix = strings.ReplaceAll(prefix, "/run/depthfirst", temp)
	cmd := exec.Command("/bin/sh", "-c", prefix+`cat "$NODE_EXTRA_CA_CERTS"`)
	cmd.Env = []string{
		"NODE_EXTRA_CA_CERTS=" + existing,
		"PATH=/usr/bin:/bin",
	}
	output, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("run prefix failed: %v\n%s", err, output)
	}
	text := string(output)
	if !strings.Contains(text, "EXISTING_CA") || !strings.Contains(text, "FIREWALL_CA") {
		t.Fatalf("NODE_EXTRA_CA_CERTS lost an existing CA\n%s", text)
	}
}

func TestInstallCAWithoutTrustStoreKeepsCAForNode(t *testing.T) {
	temp := t.TempDir()
	cert := filepath.Join(temp, "ca.crt")
	if err := os.WriteFile(cert, []byte("FIREWALL_CA\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	bin := filepath.Join(temp, "bin")
	if err := os.Mkdir(bin, 0o700); err != nil {
		t.Fatal(err)
	}
	// Only the tools a slim image has: no update-ca-certificates, no update-ca-trust.
	for _, tool := range []string{"cat", "chmod", "cp", "mkdir", "sha256sum"} {
		target, err := exec.LookPath(tool)
		if err != nil {
			t.Skipf("%s is not installed", tool)
		}
		if err := os.Symlink(target, filepath.Join(bin, tool)); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.WriteFile(filepath.Join(bin, "id"), []byte("#!/bin/sh\necho 0\n"), 0o700); err != nil {
		t.Fatal(err)
	}
	digest, err := exec.Command("sha256sum", cert).Output()
	if err != nil {
		t.Fatal(err)
	}

	script := strings.ReplaceAll(installCAScript, "/run/secrets/DF_FIREWALL_CA", cert)
	script = strings.ReplaceAll(script, "/usr/local/share", filepath.Join(temp, "usr-local-share"))
	script = strings.ReplaceAll(script, "/etc/", filepath.Join(temp, "etc")+"/")
	cmd := exec.Command("/bin/sh", "-c", script, "install-ca", strings.Fields(string(digest))[0])
	cmd.Env = []string{"PATH=" + bin}
	if output, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("install-ca failed without a trust store: %v\n%s", err, output)
	}
	installed, err := os.ReadFile(filepath.Join(temp, "usr-local-share", "ca-certificates", "depthfirst-firewall.crt"))
	if err != nil {
		t.Fatalf("CA was not installed where the RUN prefix exposes it to Node: %v", err)
	}
	if string(installed) != "FIREWALL_CA\n" {
		t.Fatalf("installed CA = %q", installed)
	}
}

func TestRunPrefixesAreValidShell(t *testing.T) {
	for _, prefix := range []string{apiRunPrefix, caRunPrefix} {
		cmd := exec.Command("/bin/sh", "-n", "-c", prefix)
		if output, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("prefix is not valid shell: %v\n%s\n%s", err, output, prefix)
		}
	}
}

func writeFirewallNpmrc(t *testing.T, dir string) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(dir, "firewall.npmrc"), []byte(npmrc), 0o600); err != nil {
		t.Fatal(err)
	}
}
