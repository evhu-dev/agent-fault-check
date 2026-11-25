package assertions

import (
	"context"
	"fmt"
	"sync"
	"time"

	"github.com/agent-fault-check/afc/internal/config"
	"github.com/agent-fault-check/afc/internal/faults"
	"github.com/agent-fault-check/afc/internal/proxy"
)

const DefaultAssertionDeadline = 5 * time.Second

// Result represents the outcome of evaluating a single assertion.
type Result struct {
	Type     string `json:"type"`
	Target   string `json:"target,omitempty"`
	Passed   bool   `json:"passed"`
	InfraErr bool   `json:"infra_error,omitempty"`
	Message  string `json:"message"`
}

// EvalContext provides run-scoped inputs for assertion evaluation.
type EvalContext struct {
	RunID    string
	Spec     *config.Spec
	Scenario *config.Scenario
	Engine   *faults.Engine
	Trace    *proxy.TraceLog
}

// AdapterFunc evaluates a single assertion type.
type AdapterFunc func(ctx context.Context, ec EvalContext, a config.Assertion) Result

// StoreReadyFunc checks whether a configured state store is reachable and ready.
type StoreReadyFunc func(ctx context.Context, st config.StoreEntry) error

var (
	registryMu        sync.RWMutex
	proAdapters       = make(map[string]AdapterFunc)
	proStoreReadiness = make(map[string]StoreReadyFunc)
)

// RegisterProAdapter registers a paid assertion adapter (called by -tags pro builds).
func RegisterProAdapter(assertionType string, fn AdapterFunc) {
	registryMu.Lock()
	defer registryMu.Unlock()
	proAdapters[assertionType] = fn
}

// RegisterProStoreReady registers a readiness checker for a paid store type.
func RegisterProStoreReady(storeType string, fn StoreReadyFunc) {
	registryMu.Lock()
	defer registryMu.Unlock()
	proStoreReadiness[storeType] = fn
}

// CheckStoreReady runs readiness verification for a configured state store.
func CheckStoreReady(ctx context.Context, st config.StoreEntry) error {
	if st.Type == "postgres" {
		return CheckPostgresReady(ctx, st.DSNEnv)
	}
	registryMu.RLock()
	fn := proStoreReadiness[st.Type]
	registryMu.RUnlock()
	if fn != nil {
		return fn(ctx, st)
	}
	return nil
}

// HasProAdapter reports whether a paid assertion adapter is registered in this build.
func HasProAdapter(assertionType string) bool {
	registryMu.RLock()
	defer registryMu.RUnlock()
	_, ok := proAdapters[assertionType]
	return ok
}

// EvaluateAll runs all assertions for a scenario with per-assertion deadlines.
func EvaluateAll(parentCtx context.Context, ec EvalContext) []Result {
	if parentCtx == nil {
		parentCtx = context.Background()
	}
	events, truncated := ec.Trace.Snapshot()
	results := make([]Result, 0, len(ec.Scenario.Assertions))

	for _, a := range ec.Scenario.Assertions {
		ctx, cancel := context.WithTimeout(parentCtx, DefaultAssertionDeadline)
		var res Result
		switch a.Type {
		case "operation_attempts":
			if truncated {
				res = Result{Type: a.Type, Passed: false, Message: "trace log truncated before operation_attempts could be verified"}
			} else {
				res = evalOperationAttempts(events, a)
			}
		case "max_tool_calls":
			if truncated {
				res = Result{Type: a.Type, Passed: false, Message: "trace log truncated before max_tool_calls could be verified"}
			} else {
				res = evalMaxToolCalls(events, a)
			}
		case "sql_row_count":
			res = evalSQLRowCount(ctx, ec, a)
		default:
			registryMu.RLock()
			fn := proAdapters[a.Type]
			registryMu.RUnlock()
			if fn != nil {
				res = fn(ctx, ec, a)
			} else {
				res = Result{
					Type:     a.Type,
					Passed:   false,
					InfraErr: true,
					Message:  fmt.Sprintf("assertion type %q is not available in this build (requires Pro distribution)", a.Type),
				}
			}
		}
		cancel()
		results = append(results, res)
	}
	return results
}
