package assertions

import (
	"fmt"
	"net/http"

	"github.com/agent-fault-check/afc/internal/config"
	"github.com/agent-fault-check/afc/internal/proxy"
)

func evalOperationAttempts(events []proxy.TraceEvent, a config.Assertion) Result {
	target := "fault=" + a.Fault
	canonHdr := http.CanonicalHeaderKey(a.CorrelationHeader)
	var injected *proxy.TraceEvent
	for i := range events {
		ev := &events[i]
		if ev.FaultID == a.Fault && ev.Event == proxy.EventFaultInjected {
			injected = ev
			break
		}
	}
	if injected == nil {
		return Result{
			Type:    a.Type,
			Target:  target,
			Passed:  false,
			Message: fmt.Sprintf("fault %q never injected; cannot verify retry attempts", a.Fault),
		}
	}
	injectedCorr := injected.CorrelationValues[canonHdr]
	if injectedCorr == "" {
		return Result{
			Type:    a.Type,
			Target:  target,
			Passed:  false,
			Message: fmt.Sprintf("faulted request for %q was missing correlation header %q", a.Fault, a.CorrelationHeader),
		}
	}

	retries := 0
	for _, ev := range events {
		if ev.Seq <= injected.Seq {
			continue
		}
		if ev.Event == proxy.EventAdmitted &&
			ev.Method == injected.Method &&
			ev.Path == injected.Path &&
			ev.CorrelationValues[canonHdr] == injectedCorr {
			retries++
		}
	}

	if retries < a.MinAfterInjection {
		return Result{
			Type:    a.Type,
			Target:  target,
			Passed:  false,
			Message: fmt.Sprintf("expected >= %d retry attempts with %s=%q after %q, got %d", a.MinAfterInjection, a.CorrelationHeader, injectedCorr, a.Fault, retries),
		}
	}
	return Result{
		Type:    a.Type,
		Target:  target,
		Passed:  true,
		Message: fmt.Sprintf("observed %d retry attempts after %q", retries, a.Fault),
	}
}

func evalMaxToolCalls(events []proxy.TraceEvent, a config.Assertion) Result {
	target := a.Match.Method + " " + a.Match.Path
	count := 0
	for _, ev := range events {
		if ev.Event == proxy.EventAdmitted && ev.Method == a.Match.Method && ev.Path == a.Match.Path {
			count++
		}
	}
	maxCalls := *a.MaxCalls
	if count > maxCalls {
		return Result{
			Type:    a.Type,
			Target:  target,
			Passed:  false,
			Message: fmt.Sprintf("max_tool_calls exceeded for %s: expected <= %d, actual %d", target, maxCalls, count),
		}
	}
	return Result{
		Type:    a.Type,
		Target:  target,
		Passed:  true,
		Message: fmt.Sprintf("tool calls for %s within budget (%d <= %d)", target, count, maxCalls),
	}
}
