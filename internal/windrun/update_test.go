package windrun

import (
	"testing"

	"github.com/trentkm/stormlight/internal/agent"
	"github.com/trentkm/stormlight/internal/session"
)

func TestApplyUpdateRecordsProviderSessionName(t *testing.T) {
	managedAgent := agent.Agent{
		Name:        "focused fixer",
		SessionName: "old name",
	}
	updated := applyUpdate(
		managedAgent,
		session.Update{SessionName: "focused fixer"},
	)
	if updated.SessionName != "focused fixer" {
		t.Fatalf("session name = %q", updated.SessionName)
	}
}
