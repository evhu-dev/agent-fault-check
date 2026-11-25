package faults

import (
	"errors"
	"testing"

	"github.com/agent-fault-check/afc/internal/config"
)

func TestEngineAdmitFireAndUnknownCommit(t *testing.T) {
	rules := []config.FaultRule{
		{ID: "f1", Strategy: "fail_before_upstream", Match: config.FaultMatch{Method: "POST", Path: "/orders", Call: 1}},
		{ID: "f2", Strategy: "malformed_json", Match: config.FaultMatch{Method: "GET", Path: "/items", Call: 2}},
	}
	eng := NewEngine(rules)

	idx, total, r := eng.Admit("POST", "/orders")
	if idx != 1 || total != 1 || r == nil || r.ID != "f1" {
		t.Fatalf("unexpected admit 1: idx=%d total=%d rule=%v", idx, total, r)
	}
	eng.MarkFired("f1")

	idx, total, r = eng.Admit("GET", "/items")
	if idx != 1 || total != 2 || r != nil {
		t.Fatalf("unexpected admit 2: idx=%d total=%d rule=%v", idx, total, r)
	}

	if got := eng.CountFor("POST", "/orders"); got != 1 {
		t.Fatalf("expected CountFor=1, got %d", got)
	}

	unfired := eng.UnfiredFaults()
	if len(unfired) != 1 || unfired[0] != "f2" {
		t.Fatalf("expected unfired=[f2], got %v", unfired)
	}

	err1 := errors.New("first unknown")
	err2 := errors.New("second unknown")
	eng.RecordUnknownCommit(err1)
	eng.RecordUnknownCommit(err2)
	if !errors.Is(eng.UnknownCommitError(), err1) {
		t.Fatalf("expected first unknown commit error to be preserved, got %v", eng.UnknownCommitError())
	}
}
