// Package dockerfile transforms a stable Dockerfile into one that installs the
// Device Firewall CA and gives package-manager RUN steps ephemeral CI auth.
package dockerfile

import (
	"bytes"
	"encoding/json"
	"fmt"
	"regexp"
	"strings"

	"github.com/moby/buildkit/frontend/dockerfile/parser"
)

const (
	defaultFilename = "Dockerfile"
	reservedStage   = "depthfirst_assets"

	// DelegatedSyntax is the digest-pinned Dockerfile frontend that finishes
	// the build. Transform rewrites `# syntax=` to this image so the official
	// frontend does not re-enter this frontend and reject the injected stage.
	DelegatedSyntax = "docker.io/docker/dockerfile:1.24.0@sha256:87999aa3d42bdc6bea60565083ee17e86d1f3339802f543c0d03998580f9cb89"
)

// Options controls host-provided, non-secret build settings.
type Options struct {
	// CAFingerprint is the lowercase SHA-256 fingerprint of DF_FIREWALL_CA.
	// Its presence enables CA installation and makes CA rotation change the
	// generated RUN cache key.
	CAFingerprint string
}

var (
	runPattern      = regexp.MustCompile(`(?i)^(\s*RUN\s+)((?:--(?:mount|network|security|device)=(?:"[^"]*"|'[^']*'|\S+)\s+)*)(.*)$`)
	reservedUse     = regexp.MustCompile(`(?i)(^|[^a-z0-9_.-])depthfirst_assets([^a-z0-9_.-]|$)`)
	syntaxDirective = regexp.MustCompile(`(?i)^#\s*syntax\s*=`)
)

// Transform adds a private asset stage, CA bootstrap steps, and ephemeral
// package-manager configuration to every network-enabled RUN instruction. Heredoc RUN steps
// are rejected because safely injecting shell setup into an arbitrary heredoc
// requires syntax-specific rewriting.
func Transform(source []byte, filename string, options Options) ([]byte, error) {
	if filename == "" {
		filename = defaultFilename
	}
	if bytes.Contains(source, []byte("\x00")) {
		return nil, fmt.Errorf("%s: NUL byte is not valid in a Dockerfile", filename)
	}

	parsed, err := parser.Parse(bytes.NewReader(source))
	if err != nil {
		return nil, fmt.Errorf("%s: %w", filename, err)
	}
	lines := strings.Split(strings.TrimSuffix(string(source), "\n"), "\n")
	firstFromLine, err := validate(parsed.AST.Children, filename)
	if err != nil {
		return nil, err
	}
	if firstFromLine < 0 {
		return nil, fmt.Errorf("%s: no FROM instruction", filename)
	}

	out := make([]string, 0, len(lines)+20)
	out = append(out, rewritePreamble(lines[:firstFromLine])...)
	out = append(out, strings.TrimSuffix(assetStage, "\n"))

	needsCA := stagesWithProtectedRun(parsed.AST.Children)
	lineIndex := firstFromLine
	for _, node := range parsed.AST.Children {
		start := node.StartLine - 1
		end := node.EndLine
		if start < lineIndex {
			continue
		}
		out = append(out, lines[lineIndex:start]...)
		switch strings.ToUpper(node.Value) {
		case "FROM":
			out = append(out, lines[start:end]...)
			if options.CAFingerprint != "" && needsCA[node] {
				out = append(out, caInstallCommand+" "+options.CAFingerprint)
			}
		case "RUN":
			transformed, err := transformRun(node, filename, options)
			if err != nil {
				return nil, err
			}
			out = append(out, transformed)
		default:
			out = append(out, lines[start:end]...)
		}
		lineIndex = end
	}
	out = append(out, lines[lineIndex:]...)

	return []byte(strings.Join(out, "\n") + "\n"), nil
}

func validate(nodes []*parser.Node, filename string) (int, error) {
	firstFromLine := -1
	for _, node := range nodes {
		if reservedUse.MatchString(node.Original) {
			return -1, fmt.Errorf("%s:%d: name %q is reserved", filename, node.StartLine, reservedStage)
		}
		switch strings.ToUpper(node.Value) {
		case "RUN":
			if len(node.Heredocs) > 0 {
				return -1, fmt.Errorf("%s:%d: RUN heredocs are not supported yet", filename, node.StartLine)
			}
		case "FROM":
			if firstFromLine < 0 {
				firstFromLine = node.StartLine - 1
			}
		}
	}
	return firstFromLine, nil
}

// stagesWithProtectedRun returns the FROM instructions whose stage has a
// network-enabled RUN. Only those stages get the CA, so scratch and distroless
// stages that only COPY never need a shell. The CA step stays directly after
// FROM so it runs as the base image user, before any USER instruction.
func stagesWithProtectedRun(nodes []*parser.Node) map[*parser.Node]bool {
	stages := make(map[*parser.Node]bool)
	var stage *parser.Node
	for _, node := range nodes {
		switch strings.ToUpper(node.Value) {
		case "FROM":
			stage = node
		case "RUN":
			if stage != nil && !disablesNetwork(node) {
				stages[stage] = true
			}
		}
	}
	return stages
}

func disablesNetwork(node *parser.Node) bool {
	for _, flag := range node.Flags {
		if flag == "--network=none" {
			return true
		}
	}
	return false
}

func rewritePreamble(lines []string) []string {
	out := make([]string, 0, len(lines)+1)
	out = append(out, "# syntax="+DelegatedSyntax)
	for _, line := range lines {
		if syntaxDirective.MatchString(line) {
			continue
		}
		out = append(out, line)
	}
	return out
}

func transformRun(node *parser.Node, filename string, options Options) (string, error) {
	line := node.Original
	match := runPattern.FindStringSubmatch(line)
	if match == nil {
		return "", fmt.Errorf("%s: unsupported RUN instruction %q", filename, line)
	}
	if disablesNetwork(node) {
		return line, nil
	}

	command := strings.TrimSpace(match[3])
	mounts := runMountPrefix + "true "
	prefix := apiRunPrefix
	if options.CAFingerprint != "" {
		mounts = runMountPrefix + "false "
		prefix = caRunPrefix + prefix
	}
	if strings.HasPrefix(command, "[") {
		var argv []string
		if err := json.Unmarshal([]byte(command), &argv); err != nil || len(argv) == 0 {
			return "", fmt.Errorf("%s: invalid JSON-form RUN instruction", filename)
		}
		wrapped := append([]string{"/bin/sh", "-c", prefix + `exec "$@"`, "depthfirst"}, argv...)
		encoded, err := json.Marshal(wrapped)
		if err != nil {
			return "", fmt.Errorf("%s: encoding JSON-form RUN: %w", filename, err)
		}
		return match[1] + match[2] + mounts + string(encoded), nil
	}
	if command == "" {
		return "", fmt.Errorf("%s: empty RUN instruction", filename)
	}
	return match[1] + match[2] + mounts + prefix + command, nil
}
