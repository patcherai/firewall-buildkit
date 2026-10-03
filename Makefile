GO ?= go
GOCACHE ?= /private/tmp/depthfirst-buildkit-gocache
GOMODCACHE ?= /private/tmp/depthfirst-buildkit-gomod

.PHONY: build test integration

test:
	GOCACHE=$(GOCACHE) GOMODCACHE=$(GOMODCACHE) $(GO) test ./...

integration:
	GOCACHE=$(GOCACHE) GOMODCACHE=$(GOMODCACHE) $(GO) test -tags integration -count=1 -timeout 30m ./integration

build:
	mkdir -p bin
	GOCACHE=$(GOCACHE) GOMODCACHE=$(GOMODCACHE) CGO_ENABLED=0 GOOS=linux GOARCH=amd64 $(GO) build -trimpath -o bin/depthfirst-frontend-linux-amd64 ./cmd/frontend
