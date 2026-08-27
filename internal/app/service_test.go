package app

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"slices"
	"testing"

	"github.com/trentkm/stormlight/internal/agent"
	"github.com/trentkm/stormlight/internal/history"
	"github.com/trentkm/stormlight/internal/provider"
	"github.com/trentkm/stormlight/internal/pty"
	"github.com/trentkm/stormlight/internal/session"
	"github.com/trentkm/stormlight/internal/workspace"
)

type recordingRuntime struct {
	session.Runtime
	agents      []agent.Agent
	workspaceID string
	updates     []session.Update
	commands    []string
	terminal    pty.Transport
}

type rootsResolver struct {
	root  workspace.Context
	roots []workspace.Context
	err   error
}

func (r rootsResolver) Name() string {
	return "roots"
}

func (r rootsResolver) Resolve(
	context.Context,
	string,
) (workspace.Context, bool, error) {
	return r.root, true, nil
}

func (r rootsResolver) ExecutionRoots(
	context.Context,
	workspace.Context,
) ([]workspace.Context, bool, error) {
	return r.roots, true, r.err
}

func (r *recordingRuntime) Update(
	_ context.Context,
	id string,
	update session.Update,
) error {
	r.updates = append(r.updates, update)
	for index := range r.agents {
		if r.agents[index].ID != id {
			continue
		}
		if update.Activity != "" {
			r.agents[index].Activity = update.Activity
		}
		if update.SessionName != "" {
			r.agents[index].SessionName = update.SessionName
		}
	}
	return nil
}

func (r *recordingRuntime) SendCommand(
	_ context.Context,
	id string,
	command string,
) error {
	r.commands = append(r.commands, id+":"+command)
	return nil
}

func (r *recordingRuntime) ListAgents(
	context.Context,
) ([]agent.Agent, error) {
	return r.agents, nil
}

func (r *recordingRuntime) SetWorkspace(
	_ context.Context,
	id string,
	_ workspace.Context,
) error {
	r.workspaceID = id
	return nil
}

func (r *recordingRuntime) AttachTerminal(
	context.Context,
	string,
	int,
	int,
) (session.TerminalStream, error) {
	return r.terminal, nil
}

type recordingTransport struct {
	writes [][]byte
	err    error
}

func (t *recordingTransport) Seed() pty.Message {
	return pty.Message{}
}

func (t *recordingTransport) Output() <-chan pty.Message {
	return make(chan pty.Message)
}

func (t *recordingTransport) Write(data []byte) error {
	t.writes = append(t.writes, slices.Clone(data))
	return t.err
}

func (t *recordingTransport) Resize(context.Context, int, int) error {
	return nil
}

func (t *recordingTransport) Close() {}

func TestCodexEscapeSettlesWorkingActivity(t *testing.T) {
	terminal := &recordingTransport{}
	current := &recordingRuntime{
		agents: []agent.Agent{{
			ID:          "agent-one",
			Provider:    agent.ProviderCodex,
			Activity:    agent.ActivityWorking,
			ProcessLive: true,
		}},
		terminal: terminal,
	}
	service := NewService(current, provider.NewRegistry(), workspace.NewRegistry())
	attached, err := service.AttachTerminal(
		context.Background(), "agent-one", 80, 24)
	if err != nil {
		t.Fatal(err)
	}

	if err := attached.Write([]byte{0x1b}); err != nil {
		t.Fatal(err)
	}
	if len(terminal.writes) != 1 || !slices.Equal(terminal.writes[0], []byte{0x1b}) {
		t.Fatalf("terminal writes = %#v", terminal.writes)
	}
	if len(current.updates) != 1 ||
		current.updates[0].Activity != agent.ActivityIdle {
		t.Fatalf("updates = %#v, want one idle update", current.updates)
	}
	if current.agents[0].Activity != agent.ActivityIdle {
		t.Fatalf("activity = %q, want idle", current.agents[0].Activity)
	}
}

func TestTerminalInputDoesNotInventCodexInterrupts(t *testing.T) {
	tests := []struct {
		name      string
		provider  agent.Provider
		activity  agent.Activity
		live      bool
		input     []byte
		writeErr  error
		wantError bool
	}{
		{
			name:     "escape sequence",
			provider: agent.ProviderCodex,
			activity: agent.ActivityWorking,
			live:     true,
			input:    []byte("\x1b[A"),
		},
		{
			name:     "other provider",
			provider: agent.ProviderClaude,
			activity: agent.ActivityWorking,
			live:     true,
			input:    []byte{0x1b},
		},
		{
			name:     "idle codex",
			provider: agent.ProviderCodex,
			activity: agent.ActivityIdle,
			live:     true,
			input:    []byte{0x1b},
		},
		{
			name:     "exited codex",
			provider: agent.ProviderCodex,
			activity: agent.ActivityWorking,
			input:    []byte{0x1b},
		},
		{
			name:      "failed write",
			provider:  agent.ProviderCodex,
			activity:  agent.ActivityWorking,
			live:      true,
			input:     []byte{0x1b},
			writeErr:  errors.New("write failed"),
			wantError: true,
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			terminal := &recordingTransport{err: test.writeErr}
			current := &recordingRuntime{
				agents: []agent.Agent{{
					ID:          "agent-one",
					Provider:    test.provider,
					Activity:    test.activity,
					ProcessLive: test.live,
				}},
				terminal: terminal,
			}
			service := NewService(
				current, provider.NewRegistry(), workspace.NewRegistry())
			attached, err := service.AttachTerminal(
				context.Background(), "agent-one", 80, 24)
			if err != nil {
				t.Fatal(err)
			}

			err = attached.Write(test.input)
			if (err != nil) != test.wantError {
				t.Fatalf("Write error = %v, want error %v", err, test.wantError)
			}
			if len(current.updates) != 0 {
				t.Fatalf("updates = %#v, want none", current.updates)
			}
		})
	}
}

func TestWorkspaceBackfillUsesRuntimeNeutralAgentID(t *testing.T) {
	current := &recordingRuntime{
		agents: []agent.Agent{{
			ID:       "agent-one",
			WindowID: "@17",
			Cwd:      t.TempDir(),
		}},
	}
	service := NewService(
		current,
		provider.NewRegistry(),
		workspace.NewRegistry(),
	)

	agents, err := service.ListAgents(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(agents) != 1 || !agents[0].Workspace.IsComplete() {
		t.Fatalf("workspace was not resolved: %#v", agents)
	}
	if current.workspaceID != "agent-one" {
		t.Fatalf(
			"workspace persisted with runtime handle %q, want agent ID",
			current.workspaceID,
		)
	}
}

func TestUpdateRecordsSessionHistory(t *testing.T) {
	current := &recordingRuntime{
		agents: []agent.Agent{{
			ID:        "agent-one",
			Provider:  agent.ProviderClaude,
			Name:      "cl-tests",
			Task:      "Run the tests",
			Cwd:       t.TempDir(),
			SessionID: "3308ff3d-2cbc-47ab-81b1-a8fa28940a14",
			Workspace: workspace.DirectoryContext("/workspace/project"),
		}},
	}
	log := history.NewLogAt(filepath.Join(t.TempDir(), "sessions.jsonl"))
	service := NewServiceWithCatalog(
		current,
		provider.NewRegistry(),
		workspace.NewRegistry(),
		workspace.NewCatalogAt(filepath.Join(t.TempDir(), "workspaces.json")),
		log,
	)

	if err := service.Update(
		context.Background(),
		"agent-one",
		session.Update{Activity: agent.ActivityWorking},
	); err != nil {
		t.Fatal(err)
	}

	records, err := log.Records()
	if err != nil {
		t.Fatal(err)
	}
	if len(records) != 1 ||
		records[0].SessionID != "3308ff3d-2cbc-47ab-81b1-a8fa28940a14" ||
		records[0].Task != "Run the tests" {
		t.Fatalf("records = %#v", records)
	}

	// While the window is alive its session is not history yet.
	past, err := service.SessionHistory(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(past) != 0 {
		t.Fatalf("live session listed as history: %#v", past)
	}

	// The window going away is what hands the session to the log.
	current.agents = nil
	past, err = service.SessionHistory(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(past) != 1 || past[0].SessionID != records[0].SessionID {
		t.Fatalf("past = %#v", past)
	}
}

func TestSessionNameSyncUsesLiveCodexCommand(t *testing.T) {
	current := &recordingRuntime{
		agents: []agent.Agent{{
			ID:          "agent-one",
			Provider:    agent.ProviderCodex,
			Name:        "focused fixer",
			SessionID:   "3308ff3d-2cbc-47ab-81b1-a8fa28940a14",
			ProcessLive: true,
		}},
	}
	registry := provider.NewRegistryWithSpecs([]provider.Spec{{
		ID:     agent.ProviderCodex,
		Binary: filepath.Join(t.TempDir(), "missing-codex"),
	}})
	service := NewService(current, registry, workspace.NewRegistry())

	if err := service.SyncSessionName(context.Background(), "agent-one"); err != nil {
		t.Fatal(err)
	}
	if err := service.SyncSessionName(context.Background(), "agent-one"); err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(
		current.commands,
		[]string{"agent-one:/rename focused fixer"},
	) {
		t.Fatalf("commands = %#v", current.commands)
	}
	if current.agents[0].SessionName != "focused fixer" ||
		len(current.updates) != 1 {
		t.Fatalf("agent = %#v, updates = %#v", current.agents[0], current.updates)
	}
}

func TestCompletedSessionNameSyncWritesCodexOnce(t *testing.T) {
	capture := filepath.Join(t.TempDir(), "calls")
	binary := filepath.Join(t.TempDir(), "codex")
	script := `#!/bin/sh
set -eu
IFS= read -r initialize
printf '{"id":1,"result":{}}\n'
IFS= read -r initialized
IFS= read -r rename
printf 'called\n' >> "$CAPTURE"
printf '{"id":2,"result":{}}\n'
while :; do sleep 1; done
`
	if err := os.WriteFile(binary, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("CAPTURE", capture)
	current := &recordingRuntime{
		agents: []agent.Agent{{
			ID:        "agent-one",
			Provider:  agent.ProviderCodex,
			Name:      "focused fixer",
			SessionID: "3308ff3d-2cbc-47ab-81b1-a8fa28940a14",
		}},
	}
	registry := provider.NewRegistryWithSpecs([]provider.Spec{{
		ID:     agent.ProviderCodex,
		Binary: binary,
	}})
	service := NewService(current, registry, workspace.NewRegistry())

	if err := service.SyncSessionName(context.Background(), "agent-one"); err != nil {
		t.Fatal(err)
	}
	if err := service.SyncSessionName(context.Background(), "agent-one"); err != nil {
		t.Fatal(err)
	}
	calls, err := os.ReadFile(capture)
	if err != nil {
		t.Fatal(err)
	}
	if string(calls) != "called\n" {
		t.Fatalf("provider calls = %q", calls)
	}
	if current.agents[0].SessionName != "focused fixer" ||
		len(current.updates) != 1 {
		t.Fatalf("agent = %#v, updates = %#v", current.agents[0], current.updates)
	}
}

func TestListWorkspaceRootsExpandsCatalogWorkspaces(t *testing.T) {
	root, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	worktreeBase, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	worktree := filepath.Join(worktreeBase, "feature")
	if err := os.MkdirAll(worktree, 0o755); err != nil {
		t.Fatal(err)
	}
	value := workspace.Context{
		ID:            "example:" + root,
		Kind:          "example",
		Name:          "example",
		Root:          root,
		ExecutionRoot: root,
	}
	linked := value
	linked.ExecutionRoot = worktree
	catalog := workspace.NewCatalogAt(filepath.Join(t.TempDir(), "workspaces.json"))
	if err := catalog.Add(workspace.Entry{Path: root}); err != nil {
		t.Fatal(err)
	}
	service := NewServiceWithCatalog(
		&recordingRuntime{},
		provider.NewRegistry(),
		workspace.NewRegistryWithResolvers(rootsResolver{
			root:  value,
			roots: []workspace.Context{linked, value},
		}),
		catalog,
		history.NewLogAt(filepath.Join(t.TempDir(), "sessions.jsonl")),
	)

	roots, err := service.ListWorkspaceRoots(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(roots) != 2 ||
		roots[0].ExecutionRoot != root ||
		roots[1].ExecutionRoot != worktree {
		t.Fatalf("roots = %#v", roots)
	}
}

func TestListWorkspaceRootsDegradesOnResolverFailure(t *testing.T) {
	root, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	value := workspace.Context{
		ID:            "example:" + root,
		Kind:          "example",
		Name:          "example",
		Root:          root,
		ExecutionRoot: root,
	}
	catalog := workspace.NewCatalogAt(filepath.Join(t.TempDir(), "workspaces.json"))
	if err := catalog.Add(workspace.Entry{Path: root}); err != nil {
		t.Fatal(err)
	}
	service := NewServiceWithCatalog(
		&recordingRuntime{},
		provider.NewRegistry(),
		workspace.NewRegistryWithResolvers(rootsResolver{
			root: value,
			err:  errors.New("enumeration failed"),
		}),
		catalog,
		history.NewLogAt(filepath.Join(t.TempDir(), "sessions.jsonl")),
	)

	roots, err := service.ListWorkspaceRoots(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(roots) != 1 || roots[0].ExecutionRoot != root {
		t.Fatalf("roots = %#v", roots)
	}
}

func TestSortWorkspaceRootsGroupsEachWorkspace(t *testing.T) {
	values := []workspace.Context{
		{ID: "a", Name: "alpha", Root: "/a", ExecutionRoot: "/a/feature"},
		{ID: "b", Name: "beta", Root: "/b", ExecutionRoot: "/b"},
		{ID: "a", Name: "alpha", Root: "/a", ExecutionRoot: "/a"},
		{ID: "b", Name: "beta", Root: "/b", ExecutionRoot: "/b/feature"},
	}
	sortWorkspaceRoots(values)

	got := make([]string, 0, len(values))
	for _, value := range values {
		got = append(got, value.ExecutionRoot)
	}
	want := []string{"/a", "/a/feature", "/b", "/b/feature"}
	if !slices.Equal(got, want) {
		t.Fatalf("roots = %#v, want %#v", got, want)
	}
}
