package license

import (
	"context"
	"testing"

	"github.com/agent-fault-check/afc/internal/config"
)

func TestResolveKeyPrecedence(t *testing.T) {
	t.Setenv("AFC_LICENSE_KEY", "env-key")
	if got := ResolveKey("flag-key"); got != "flag-key" {
		t.Fatalf("expected flag precedence, got %q", got)
	}
	if got := ResolveKey(""); got != "env-key" {
		t.Fatalf("expected env fallback, got %q", got)
	}
}

func TestPreflightFreeAndPaidGating(t *testing.T) {
	origValidator := paidValidator
	t.Cleanup(func() { paidValidator = origValidator })
	paidValidator = nil

	freeSpec := &config.Spec{
		Scenarios: []config.Scenario{
			{ID: "s1", Assertions: []config.Assertion{{Type: "sql_row_count"}}},
			{ID: "s2", Assertions: []config.Assertion{{Type: "max_tool_calls"}}},
			{ID: "s3", Assertions: []config.Assertion{{Type: "operation_attempts"}}},
		},
	}

	// Free-only spec passes even if a key is present in an OSS build
	tier, err := Preflight(context.Background(), freeSpec, "", "", "", "unused-key")
	if err != nil || tier != TierFree {
		t.Fatalf("expected Free tier pass on OSS build for free-only spec, got tier=%q err=%v", tier, err)
	}

	// SARIF requires Pro
	if _, err := Preflight(context.Background(), freeSpec, "", "out.sarif", "", ""); err == nil {
		t.Fatal("expected error for SARIF without license")
	}

	// >3 scenarios requires Pro
	fourScenarios := &config.Spec{
		Scenarios: append(append([]config.Scenario{}, freeSpec.Scenarios...), config.Scenario{ID: "s4"}),
	}
	if _, err := Preflight(context.Background(), fourScenarios, "", "", "", ""); err == nil {
		t.Fatal("expected error for 4 scenarios without license")
	}

	// Pro assertion requires Pro
	proSpec := &config.Spec{
		Scenarios: []config.Scenario{
			{ID: "s1", Assertions: []config.Assertion{{Type: "redis_key_count"}}},
		},
	}
	if _, err := Preflight(context.Background(), proSpec, "", "", "", ""); err == nil {
		t.Fatal("expected error for redis_key_count without license")
	}
}
