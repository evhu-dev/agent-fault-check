package assertions

import (
	"context"
	"testing"

	"github.com/agent-fault-check/afc/internal/config"
	"github.com/agent-fault-check/afc/internal/faults"
	"github.com/agent-fault-check/afc/internal/proxy"
)

func TestOperationAttemptsAndMaxToolCalls(t *testing.T) {
	tr := proxy.NewTraceLog("run-1", "sc-1", 100)
	tr.Record(proxy.TraceEvent{Method: "POST", Path: "/orders", CallIndex: 1, FaultID: "f1", Event: proxy.EventAdmitted, CorrelationValues: map[string]string{"X-Operation-Id": "op-1", "X-Other-Id": "other-1"}})
	tr.Record(proxy.TraceEvent{Method: "POST", Path: "/orders", CallIndex: 1, FaultID: "f1", Event: proxy.EventFaultInjected, Status: 502, CorrelationValues: map[string]string{"X-Operation-Id": "op-1", "X-Other-Id": "other-1"}})
	tr.Record(proxy.TraceEvent{Method: "POST", Path: "/orders", CallIndex: 2, Event: proxy.EventAdmitted, CorrelationValues: map[string]string{"X-Operation-Id": "op-1", "X-Other-Id": "other-2"}})

	max2 := 2
	max1 := 1
	sc := &config.Scenario{
		ID: "sc-1",
		Assertions: []config.Assertion{
			{Type: "operation_attempts", Fault: "f1", CorrelationHeader: "X-Operation-ID", MinAfterInjection: 1},
			{Type: "operation_attempts", Fault: "f1", CorrelationHeader: "X-Other-ID", MinAfterInjection: 1},
			{Type: "max_tool_calls", Match: &config.CallMatch{Method: "POST", Path: "/orders"}, MaxCalls: &max2},
			{Type: "max_tool_calls", Match: &config.CallMatch{Method: "POST", Path: "/orders"}, MaxCalls: &max1},
		},
	}

	results := EvaluateAll(context.Background(), EvalContext{
		RunID:    "run-1",
		Spec:     &config.Spec{},
		Scenario: sc,
		Engine:   faults.NewEngine(nil),
		Trace:    tr,
	})

	if !results[0].Passed {
		t.Fatalf("expected operation_attempts (X-Operation-ID) to pass: %s", results[0].Message)
	}
	if results[1].Passed {
		t.Fatalf("expected operation_attempts (X-Other-ID) to fail when value changed")
	}
	if !results[2].Passed {
		t.Fatalf("expected max_tool_calls<=2 to pass: %s", results[2].Message)
	}
	if results[3].Passed {
		t.Fatalf("expected max_tool_calls<=1 to fail")
	}
}

func TestOperationAttemptsParallelCallBeforeTimeoutNotCounted(t *testing.T) {
	tr := proxy.NewTraceLog("run-1", "sc-1", 100)
	// Call 1 admitted (Seq 1), parallel Call 2 admitted BEFORE Call 1 times out (Seq 2),
	// then Call 1 times out (Seq 3, Status 499). Parallel Call 2 must NOT count as a retry.
	tr.Record(proxy.TraceEvent{Method: "POST", Path: "/orders", CallIndex: 1, FaultID: "f-timeout", Event: proxy.EventAdmitted, CorrelationValues: map[string]string{"X-Operation-Id": "op-1"}})
	tr.Record(proxy.TraceEvent{Method: "POST", Path: "/orders", CallIndex: 2, Event: proxy.EventAdmitted, CorrelationValues: map[string]string{"X-Operation-Id": "op-1"}})
	tr.Record(proxy.TraceEvent{Method: "POST", Path: "/orders", CallIndex: 1, FaultID: "f-timeout", Event: proxy.EventFaultInjected, Status: 499, CorrelationValues: map[string]string{"X-Operation-Id": "op-1"}})

	sc := &config.Scenario{
		ID: "sc-1",
		Assertions: []config.Assertion{
			{Type: "operation_attempts", Fault: "f-timeout", CorrelationHeader: "X-Operation-ID", MinAfterInjection: 1},
		},
	}
	results := EvaluateAll(context.Background(), EvalContext{
		RunID:    "run-1",
		Spec:     &config.Spec{},
		Scenario: sc,
		Engine:   faults.NewEngine(nil),
		Trace:    tr,
	})
	if results[0].Passed {
		t.Fatalf("expected parallel call admitted before timeout injection NOT to count as a retry")
	}

	// Now record an actual retry AFTER EventFaultInjected (Seq 4) -> must pass.
	tr.Record(proxy.TraceEvent{Method: "POST", Path: "/orders", CallIndex: 3, Event: proxy.EventAdmitted, CorrelationValues: map[string]string{"X-Operation-Id": "op-1"}})
	results = EvaluateAll(context.Background(), EvalContext{
		RunID:    "run-1",
		Spec:     &config.Spec{},
		Scenario: sc,
		Engine:   faults.NewEngine(nil),
		Trace:    tr,
	})
	if !results[0].Passed {
		t.Fatalf("expected retry admitted after timeout injection to pass: %s", results[0].Message)
	}
}

func TestResolveSQLArgs(t *testing.T) {
	resolved := resolveSQLArgs([]any{map[string]any{"runtime": "run_id"}, "cust_1", 4900}, "run-xyz")
	if len(resolved) != 3 || resolved[0] != "run-xyz" || resolved[1] != "cust_1" || resolved[2] != 4900 {
		t.Fatalf("unexpected resolved args: %#v", resolved)
	}
}

func TestTruncatedTraceFailsAssertions(t *testing.T) {
	tr := proxy.NewTraceLog("run-1", "sc-1", 1)
	tr.Record(proxy.TraceEvent{Method: "POST", Path: "/orders", CallIndex: 1, Event: proxy.EventAdmitted})
	tr.Record(proxy.TraceEvent{Method: "POST", Path: "/orders", CallIndex: 2, Event: proxy.EventAdmitted}) // truncated

	max5 := 5
	sc := &config.Scenario{
		ID: "sc-1",
		Assertions: []config.Assertion{
			{Type: "max_tool_calls", Match: &config.CallMatch{Method: "POST", Path: "/orders"}, MaxCalls: &max5},
		},
	}
	results := EvaluateAll(context.Background(), EvalContext{
		RunID:    "run-1",
		Spec:     &config.Spec{},
		Scenario: sc,
		Engine:   faults.NewEngine(nil),
		Trace:    tr,
	})
	if results[0].Passed {
		t.Fatalf("expected truncated trace to fail assertion")
	}
}
