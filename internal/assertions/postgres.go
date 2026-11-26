package assertions

import (
	"context"
	"fmt"
	"os"
	"time"

	"github.com/agent-fault-check/afc/internal/config"
	"github.com/jackc/pgx/v5"
)

// CheckPostgresReady retries Postgres connectivity at 1s intervals within a 10s total deadline.
func CheckPostgresReady(ctx context.Context, dsnEnv string) error {
	dsn := os.Getenv(dsnEnv)
	if dsn == "" {
		return fmt.Errorf("environment variable %q is not set", dsnEnv)
	}
	deadlineCtx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()

	var lastErr error
	for {
		attemptCtx, attemptCancel := context.WithTimeout(deadlineCtx, 2*time.Second)
		conn, err := pgx.Connect(attemptCtx, dsn)
		if err == nil {
			err = conn.Ping(attemptCtx)
			_ = conn.Close(attemptCtx)
		}
		attemptCancel()
		if err == nil {
			return nil
		}
		lastErr = err

		select {
		case <-deadlineCtx.Done():
			return fmt.Errorf("postgres store (%s) not ready within 10s: %w", dsnEnv, lastErr)
		case <-time.After(1 * time.Second):
		}
	}
}

func evalSQLRowCount(ctx context.Context, ec EvalContext, a config.Assertion) Result {
	target := "store=" + a.Store
	st := ec.Spec.StateStores[a.Store]
	dsn := os.Getenv(st.DSNEnv)
	if dsn == "" {
		return Result{Type: a.Type, Target: target, InfraErr: true, Message: fmt.Sprintf("postgres env %q is empty", st.DSNEnv)}
	}

	conn, err := pgx.Connect(ctx, dsn)
	if err != nil {
		return Result{Type: a.Type, Target: target, InfraErr: true, Message: fmt.Sprintf("connect postgres (%s): %v", a.Store, err)}
	}
	var tx pgx.Tx
	defer func() {
		cleanupCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), time.Second)
		defer cancel()
		if tx != nil {
			_ = tx.Rollback(cleanupCtx)
		}
		_ = conn.Close(cleanupCtx)
	}()

	tx, err = conn.BeginTx(ctx, pgx.TxOptions{AccessMode: pgx.ReadOnly})
	if err != nil {
		return Result{Type: a.Type, Target: target, InfraErr: true, Message: fmt.Sprintf("begin read-only tx (%s): %v", a.Store, err)}
	}

	args := resolveSQLArgs(a.Args, ec.RunID)
	var actual int64
	if err := tx.QueryRow(ctx, a.Query, args...).Scan(&actual); err != nil {
		return Result{Type: a.Type, Target: target, InfraErr: true, Message: fmt.Sprintf("query failed (%s): %v", a.Store, err)}
	}
	if actual < 0 {
		return Result{Type: a.Type, Target: target, InfraErr: true, Message: fmt.Sprintf("query returned negative count %d (%s)", actual, a.Store)}
	}

	expected := int64(*a.Expected)
	if actual != expected {
		return Result{
			Type:    a.Type,
			Target:  target,
			Passed:  false,
			Message: fmt.Sprintf("sql_row_count (store %q) mismatch: expected %d, actual %d", a.Store, expected, actual),
		}
	}
	return Result{
		Type:    a.Type,
		Target:  target,
		Passed:  true,
		Message: fmt.Sprintf("sql_row_count (store %q) matched %d", a.Store, actual),
	}
}

func resolveSQLArgs(raw []any, runID string) []any {
	out := make([]any, len(raw))
	for i, v := range raw {
		if m, ok := v.(map[string]any); ok {
			if rt, ok := m["runtime"].(string); ok && rt == "run_id" {
				out[i] = runID
				continue
			}
		}
		out[i] = v
	}
	return out
}
