package main

import (
	"path/filepath"
	"strings"
	"testing"
)

func envFrom(values map[string]string) func(string) string {
	return func(key string) string { return values[key] }
}

const goodSecret = "0123456789abcdef0123456789abcdef"

func TestEntraStaysOffWhenNothingIsConfigured(t *testing.T) {
	_, enabled, err := readEntraEnv(envFrom(nil), "data/uade.db")
	if enabled || err != nil {
		t.Fatalf("enabled=%v err=%v, want off and silent", enabled, err)
	}
}

// Half a configuration is reported, never guessed at: it is almost always a
// typo in the variable name, and silently disabling would hide it.
func TestEntraRejectsAPartialConfiguration(t *testing.T) {
	_, enabled, err := readEntraEnv(envFrom(map[string]string{"ENTRA_TENANT_ID": "t"}), "data/uade.db")
	if enabled || err == nil {
		t.Fatalf("enabled=%v err=%v, want an error naming the missing variables", enabled, err)
	}
}

func TestEntraRejectsAShortSecret(t *testing.T) {
	_, enabled, err := readEntraEnv(envFrom(map[string]string{
		"ENTRA_TENANT_ID": "t", "ENTRA_RESOURCE": "https://graph.microsoft.com", "ENTRA_TOKEN_SECRET": "short",
	}), "data/uade.db")
	if enabled || err == nil || !strings.Contains(err.Error(), "32") {
		t.Fatalf("enabled=%v err=%v", enabled, err)
	}
}

// Sessions live next to the database, on the same persistent volume, and the
// probe defaults to a real authenticated endpoint of the resource.
func TestEntraDefaultsDirectoryAndProbe(t *testing.T) {
	env, enabled, err := readEntraEnv(envFrom(map[string]string{
		"ENTRA_TENANT_ID": "t", "ENTRA_RESOURCE": "https://graph.microsoft.com", "ENTRA_TOKEN_SECRET": goodSecret,
	}), filepath.Join("var", "lib", "uade.db"))
	if !enabled || err != nil {
		t.Fatalf("enabled=%v err=%v", enabled, err)
	}
	if env.Dir != filepath.Join("var", "lib", "entra") {
		t.Fatalf("Dir = %q", env.Dir)
	}
	if env.ProbeURL != "https://graph.microsoft.com/v1.0/me" {
		t.Fatalf("ProbeURL = %q", env.ProbeURL)
	}
}

func TestClientLabelNeverPrintsTheIdentifierItself(t *testing.T) {
	for _, id := range []string{"", "d3590ed6-52b3-4102-aeff-aad2292ab01c", "own-app-11111111"} {
		if label := clientLabel(id); id != "" && strings.Contains(label, id) {
			t.Fatalf("label %q exposes %q", label, id)
		}
	}
}
