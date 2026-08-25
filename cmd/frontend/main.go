package main

import (
	"context"
	"fmt"
	"maps"
	"os"
	"path"
	"regexp"
	"strings"

	"github.com/patcherai/firewall-buildkit/internal/dockerfile"
	"github.com/moby/buildkit/client/llb"
	"github.com/moby/buildkit/frontend/dockerui"
	"github.com/moby/buildkit/frontend/gateway/client"
	"github.com/moby/buildkit/frontend/gateway/grpcclient"
	"github.com/moby/buildkit/solver/pb"
	"github.com/moby/buildkit/util/appcontext"
	_ "github.com/moby/buildkit/util/grpcutil/encoding/proto"
)

var caFingerprintPattern = regexp.MustCompile(`^[0-9a-fA-F]{64}$`)

func main() {
	if err := grpcclient.RunFromEnvironment(appcontext.Context(), build); err != nil {
		fmt.Fprintf(os.Stderr, "depthfirst frontend: %v\n", err)
		os.Exit(1)
	}
}

func build(ctx context.Context, gateway client.Client) (*client.Result, error) {
	ui, err := dockerui.NewClient(gateway)
	if err != nil {
		return nil, fmt.Errorf("initializing Dockerfile input: %w", err)
	}
	source, err := ui.ReadEntrypoint(ctx, "Dockerfile")
	if err != nil {
		return nil, fmt.Errorf("reading Dockerfile: %w", err)
	}

	caFingerprint, err := readCAFingerprint(gateway.BuildOpts().Opts)
	if err != nil {
		return nil, err
	}
	transformed, err := dockerfile.Transform(source.Data, source.Filename, dockerfile.Options{
		CAFingerprint: caFingerprint,
	})
	if err != nil {
		return nil, err
	}
	if source.State == nil {
		return nil, fmt.Errorf("Dockerfile input has no source state")
	}

	transformedState := *source.State
	parent := path.Dir(source.Filename)
	if parent != "." && parent != "/" {
		transformedState = transformedState.File(llb.Mkdir(parent, 0o755, llb.WithParents(true)))
	}
	transformedState = transformedState.File(llb.Mkfile(source.Filename, 0o644, transformed))

	inputs, err := gateway.Inputs(ctx)
	if err != nil {
		return nil, fmt.Errorf("reading frontend inputs: %w", err)
	}
	inputs[dockerui.DefaultLocalNameDockerfile] = transformedState

	opts := maps.Clone(gateway.BuildOpts().Opts)
	if opts == nil {
		opts = make(map[string]string)
	}
	if remoteContext(opts["context"]) {
		// ReadEntrypoint has already resolved and, for HTTP archives, unpacked the
		// remote context. Forward that state so the delegated frontend does not
		// download it a second time or replace our transformed Dockerfile.
		inputs[dockerui.DefaultLocalNameContext] = *source.State
		delete(opts, "context")
		delete(opts, "contextsubdir")
	}

	frontendInputs := make(map[string]*pb.Definition, len(inputs))
	for name, state := range inputs {
		definition, err := state.Marshal(ctx)
		if err != nil {
			return nil, fmt.Errorf("marshalling %q input: %w", name, err)
		}
		frontendInputs[name] = definition.ToPB()
	}

	opts["source"] = dockerfile.DelegatedSyntax
	delete(opts, "dockerfilekey")
	delete(opts, "cmdline")
	delete(opts, "build-arg:BUILDKIT_SYNTAX")
	delete(opts, "build-arg:DF_FIREWALL_CA_SHA256")

	result, err := gateway.Solve(ctx, client.SolveRequest{
		Frontend:       "gateway.v0",
		FrontendOpt:    opts,
		FrontendInputs: frontendInputs,
	})
	if err != nil {
		return nil, fmt.Errorf("building transformed Dockerfile: %w", err)
	}
	return result, nil
}

func readCAFingerprint(opts map[string]string) (string, error) {
	value := opts["build-arg:DF_FIREWALL_CA_SHA256"]
	if value == "" {
		return "", nil
	}
	if !caFingerprintPattern.MatchString(value) {
		return "", fmt.Errorf("DF_FIREWALL_CA_SHA256 must be exactly 64 hexadecimal characters")
	}
	return strings.ToLower(value), nil
}

func remoteContext(value string) bool {
	if value == "" {
		return false
	}
	if _, _, ok := dockerui.DetectHTTPContext(value); ok {
		return true
	}
	_, ok, _ := dockerui.DetectGitContext(value, nil)
	return ok || strings.HasPrefix(value, "git://")
}
