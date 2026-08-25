package dockerfile

import (
	_ "embed"
	"strings"
)

//go:embed assets/install-ca.sh
var installCAScript string

//go:embed assets/npmrc
var npmrc string

//go:embed assets/api-run-prefix.sh
var apiRunPrefixFile string

//go:embed assets/ca-run-prefix.sh
var caRunPrefixFile string

//go:embed assets/ca-install-command
var caInstallCommandFile string

//go:embed assets/run-mount-prefix
var runMountPrefixFile string

func oneLinePrefix(s string) string {
	var parts []string
	for _, line := range strings.Split(s, "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		parts = append(parts, line)
	}
	if len(parts) == 0 {
		return ""
	}
	var b strings.Builder
	for i, line := range parts {
		if i > 0 {
			if continuesCompound(parts[i-1]) {
				b.WriteByte(' ')
			} else {
				b.WriteString("; ")
			}
		}
		b.WriteString(line)
	}
	return b.String() + "; "
}

func continuesCompound(line string) bool {
	if line == ";;" || strings.HasSuffix(line, " ;;") {
		return true
	}
	for _, kw := range []string{"then", "else", "elif", "in", "do"} {
		if line == kw || strings.HasSuffix(line, " "+kw) {
			return true
		}
	}
	return false
}

var (
	apiRunPrefix     = oneLinePrefix(apiRunPrefixFile)
	caRunPrefix      = oneLinePrefix(caRunPrefixFile)
	caInstallCommand = strings.TrimSuffix(caInstallCommandFile, "\n")
	runMountPrefix   = strings.TrimSuffix(runMountPrefixFile, "\n")
	assetStage       = "FROM scratch AS " + reservedStage + "\n" +
		"COPY <<'DEPTHFIRST_CA_INSTALLER' /install-ca\n" +
		strings.TrimSuffix(installCAScript, "\n") + "\n" +
		"DEPTHFIRST_CA_INSTALLER\n" +
		"COPY <<'DEPTHFIRST_NPMRC' /npmrc\n" +
		strings.TrimSuffix(npmrc, "\n") + "\n" +
		"DEPTHFIRST_NPMRC\n"
)
