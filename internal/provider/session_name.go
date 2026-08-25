package provider

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os/exec"
	"strings"
	"time"

	"github.com/trentkm/stormlight/internal/agent"
)

// SetSessionName writes a human-facing name through the provider's supported
// out-of-band API. The bool reports capability: custom providers and providers
// named entirely through launch arguments return false.
func (r *Registry) SetSessionName(
	ctx context.Context,
	id agent.Provider,
	sessionID string,
	name string,
) (bool, error) {
	if id != agent.ProviderCodex {
		return false, nil
	}
	adapter, ok := r.adapters[id]
	if !ok {
		return false, nil
	}
	sessionID = strings.TrimSpace(sessionID)
	name = strings.TrimSpace(name)
	if sessionID == "" {
		return true, fmt.Errorf("session id cannot be empty")
	}
	if name == "" {
		return true, fmt.Errorf("session name cannot be empty")
	}
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	path, err := exec.LookPath(adapter.Binary())
	if err != nil {
		return true, fmt.Errorf("%s is not installed or not on PATH", adapter.Binary())
	}
	if err := setCodexSessionName(ctx, path, sessionID, name); err != nil {
		return true, fmt.Errorf("name Codex session: %w", err)
	}
	return true, nil
}

// SessionNameCommand returns the provider-native command that names the
// currently running session. Codex only updates its TUI's in-memory name
// through /rename; an app-server write from another process updates the index
// but leaves exit and resume hints using the UUID.
func (r *Registry) SessionNameCommand(
	id agent.Provider,
	name string,
) (string, bool, error) {
	if id != agent.ProviderCodex {
		return "", false, nil
	}
	if _, ok := r.adapters[id]; !ok {
		return "", false, nil
	}
	name = strings.TrimSpace(name)
	if name == "" {
		return "", true, fmt.Errorf("session name cannot be empty")
	}
	if strings.ContainsAny(name, "\r\n") {
		return "", true, fmt.Errorf("session name must be one line")
	}
	return "/rename " + name, true, nil
}

type rpcResponse struct {
	ID    json.RawMessage `json:"id"`
	Error *struct {
		Code    int    `json:"code"`
		Message string `json:"message"`
	} `json:"error,omitempty"`
}

func setCodexSessionName(
	ctx context.Context,
	binary string,
	sessionID string,
	name string,
) error {
	command := exec.CommandContext(ctx, binary, "app-server", "--stdio")
	stdin, err := command.StdinPipe()
	if err != nil {
		return err
	}
	stdout, err := command.StdoutPipe()
	if err != nil {
		return err
	}
	var stderr bytes.Buffer
	command.Stderr = &stderr
	if err := command.Start(); err != nil {
		return err
	}
	waited := false
	stop := func() {
		if waited {
			return
		}
		_ = stdin.Close()
		_ = command.Process.Kill()
		_ = command.Wait()
		waited = true
	}
	defer stop()

	encoder := json.NewEncoder(stdin)
	decoder := json.NewDecoder(bufio.NewReader(stdout))
	if err := encoder.Encode(map[string]any{
		"method": "initialize",
		"id":     1,
		"params": map[string]any{
			"clientInfo": map[string]string{
				"name":    "stormlight",
				"title":   "Stormlight",
				"version": "unknown",
			},
		},
	}); err != nil {
		return err
	}
	if err := readRPCResponse(decoder, 1); err != nil {
		stop()
		return withCommandError(err, stderr.String())
	}
	if err := encoder.Encode(map[string]any{
		"method": "initialized",
	}); err != nil {
		return err
	}
	if err := encoder.Encode(map[string]any{
		"method": "thread/name/set",
		"id":     2,
		"params": map[string]string{
			"threadId": sessionID,
			"name":     name,
		},
	}); err != nil {
		return err
	}
	if err := readRPCResponse(decoder, 2); err != nil {
		stop()
		return withCommandError(err, stderr.String())
	}
	// app-server is a service, not a one-shot command: after initialization
	// it can remain alive even when stdin closes. The successful response is
	// the transaction boundary, so terminate the helper and reap it here.
	stop()
	return nil
}

func readRPCResponse(decoder *json.Decoder, id int) error {
	for {
		var response rpcResponse
		if err := decoder.Decode(&response); err != nil {
			if err == io.EOF {
				return fmt.Errorf("app server closed before response %d", id)
			}
			return fmt.Errorf("decode app-server response: %w", err)
		}
		if string(response.ID) != fmt.Sprint(id) {
			continue
		}
		if response.Error != nil {
			return fmt.Errorf(
				"app-server error %d: %s",
				response.Error.Code,
				response.Error.Message,
			)
		}
		return nil
	}
}

func withCommandError(err error, stderr string) error {
	stderr = strings.TrimSpace(stderr)
	if stderr == "" {
		return err
	}
	return fmt.Errorf("%w: %s", err, stderr)
}
