package main

import (
	"strings"
	"testing"
)

func TestReadCAFingerprint(t *testing.T) {
	upper := strings.Repeat("AB", 32)
	got, err := readCAFingerprint(map[string]string{"build-arg:DF_FIREWALL_CA_SHA256": upper})
	if err != nil {
		t.Fatal(err)
	}
	if got != strings.ToLower(upper) {
		t.Fatalf("fingerprint = %q", got)
	}
	if _, err := readCAFingerprint(map[string]string{"build-arg:DF_FIREWALL_CA_SHA256": "not-a-digest"}); err == nil {
		t.Fatal("invalid fingerprint was accepted")
	}
}

func TestRemoteContext(t *testing.T) {
	for _, value := range []string{
		"https://github.com/depthfirst/example.git#main",
		"git://github.com/depthfirst/example.git",
		"https://example.com/context.tar.gz",
	} {
		if !remoteContext(value) {
			t.Errorf("remoteContext(%q) = false", value)
		}
	}
	if remoteContext("") {
		t.Fatal("empty local context was classified as remote")
	}
}
