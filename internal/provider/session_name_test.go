package provider

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/trentkm/stormlight/internal/agent"
)

func TestCodexSessionNameUsesAppServerProtocol(t *testing.T) {
	capture := filepath.Join(t.TempDir(), "request.json")
	binary := fakeAppServer(t, `
IFS= read -r initialize
printf '{"id":1,"result":{}}\n'
IFS= read -r initialized
IFS= read -r rename
printf '%s\n' "$rename" > "$CAPTURE"
printf '{"id":2,"result":{}}\n'
while :; do sleep 1; done
`)
	t.Setenv("CAPTURE", capture)
	registry := NewRegistryWithSpecs([]Spec{{
		ID:     agent.ProviderCodex,
		Binary: binary,
	}})

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	supported, err := registry.SetSessionName(
		ctx,
		agent.ProviderCodex,
		"3308ff3d-2cbc-47ab-81b1-a8fa28940a14",
		`focused "fixer"`,
	)
	if err != nil {
		t.Fatal(err)
	}
	if !supported {
		t.Fatal("Codex session naming reported unsupported")
	}
	encoded, err := os.ReadFile(capture)
	if err != nil {
		t.Fatal(err)
	}
	var request struct {
		Method string `json:"method"`
		ID     int    `json:"id"`
		Params struct {
			ThreadID string `json:"threadId"`
			Name     string `json:"name"`
		} `json:"params"`
	}
	if err := json.Unmarshal(encoded, &request); err != nil {
		t.Fatalf("decode request %q: %v", encoded, err)
	}
	if request.Method != "thread/name/set" || request.ID != 2 ||
		request.Params.ThreadID != "3308ff3d-2cbc-47ab-81b1-a8fa28940a14" ||
		request.Params.Name != `focused "fixer"` {
		t.Fatalf("request = %#v", request)
	}
}

func TestCodexLiveSessionNameUsesRenameCommand(t *testing.T) {
	registry := NewRegistryWithSpecs([]Spec{{
		ID:     agent.ProviderCodex,
		Binary: "echo",
	}})

	command, supported, err := registry.SessionNameCommand(
		agent.ProviderCodex,
		"  focused fixer  ",
	)
	if err != nil {
		t.Fatal(err)
	}
	if !supported || command != "/rename focused fixer" {
		t.Fatalf("command = %q, supported = %t", command, supported)
	}
}

func TestCodexSessionNameReportsAppServerErrors(t *testing.T) {
	binary := fakeAppServer(t, `
IFS= read -r initialize
printf '{"id":1,"result":{}}\n'
IFS= read -r initialized
IFS= read -r rename
printf '{"id":2,"error":{"code":-32602,"message":"thread not found"}}\n'
`)
	registry := NewRegistryWithSpecs([]Spec{{
		ID:     agent.ProviderCodex,
		Binary: binary,
	}})

	supported, err := registry.SetSessionName(
		context.Background(),
		agent.ProviderCodex,
		"3308ff3d-2cbc-47ab-81b1-a8fa28940a14",
		"focused fixer",
	)
	if !supported {
		t.Fatal("Codex session naming reported unsupported")
	}
	if err == nil || !strings.Contains(err.Error(), "thread not found") {
		t.Fatalf("error = %v", err)
	}
}

func TestCustomProvidersDoNotClaimSessionNaming(t *testing.T) {
	registry := NewRegistryWithSpecs([]Spec{{
		ID:     agent.Provider("other"),
		Binary: "echo",
	}})
	supported, err := registry.SetSessionName(
		context.Background(),
		agent.Provider("other"),
		"session",
		"name",
	)
	if err != nil || supported {
		t.Fatalf("supported=%v error=%v", supported, err)
	}
}

func fakeAppServer(t *testing.T, body string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "codex")
	script := "#!/bin/sh\nset -eu\n" + strings.TrimSpace(body) + "\n"
	if err := os.WriteFile(path, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	return path
}
