package runner

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/agent-fault-check/afc/internal/config"
	"github.com/agent-fault-check/afc/internal/faults"
	"github.com/agent-fault-check/afc/internal/proxy"
)

func TestTeardownFailurePreservedOnPassAndFail(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	defer upstream.Close()

	maxCalls := 5
	makeSpec := func(agentCmd string) *config.Spec {
		return &config.Spec{
			Version: "1.0",
			Name:    "teardown-test",
			Agent: config.AgentConfig{
				Argv:           []string{"sh", "-c", agentCmd},
				TimeoutSeconds: 5,
			},
			Fixture: config.FixtureConfig{
				SetupArgv:              []string{"sh", "-c", "sleep 0.02"},
				TeardownArgv:           []string{"sh", "-c", "echo boom >&2; exit 1"},
				SetupTimeoutSeconds:    5,
				QuiesceTimeoutSeconds:  5,
				TeardownTimeoutSeconds: 5,
			},
			Proxy: config.ProxyConfig{
				Transport:         "http",
				ListenAddr:        "127.0.0.1:0",
				UpstreamURL:       upstream.URL,
				UpstreamTimeoutMs: 2000,
			},
			Scenarios: []config.Scenario{{
				ID:     "sc-1",
				Prompt: "test",
				Faults: []config.FaultRule{{
					ID:       "f1",
					Strategy: "fail_before_upstream",
					Match:    config.FaultMatch{Method: "POST", Path: "/hit", Call: 1},
					Inject:   &config.InjectSpec{Kind: "http_error", Status: 500},
				}},
				Assertions: []config.Assertion{{
					Type:     "max_tool_calls",
					Match:    &config.CallMatch{Method: "POST", Path: "/hit"},
					MaxCalls: &maxCalls,
				}},
			}},
		}
	}

	// 1. Passing scenario with failing teardown -> upgraded to StatusError (exit 2), Teardown & DurationMs preserved
	passAgent := `curl -fsS -X POST "$TOOL_API_BASE_URL/hit" >/dev/null 2>&1 || true`
	res := RunSuite(context.Background(), makeSpec(passAgent), filepath.Join(t.TempDir(), "spec.yaml"), "")
	if res.ExitCode != 2 {
		t.Fatalf("expected exit 2 when teardown fails on passing scenario, got %d (%+v)", res.ExitCode, res.Scenarios)
	}
	sc := res.Scenarios[0]
	if sc.Status != StatusError || sc.Teardown == "" || !strings.Contains(sc.Teardown, "boom") {
		t.Fatalf("expected StatusError with teardown error, got %+v", sc)
	}
	if sc.DurationMs <= 0 {
		t.Fatalf("expected positive DurationMs, got %d", sc.DurationMs)
	}

	// 2. Failing scenario (unfired fault) with failing teardown -> upgraded to StatusError (exit 2), both errors preserved
	resFail := RunSuite(context.Background(), makeSpec("exit 0"), filepath.Join(t.TempDir(), "spec.yaml"), "")
	if resFail.ExitCode != 2 {
		t.Fatalf("expected exit 2 when teardown fails on failing scenario, got %d (%+v)", resFail.ExitCode, resFail.Scenarios)
	}
	scFail := resFail.Scenarios[0]
	if scFail.Status != StatusError || scFail.Teardown == "" || !strings.Contains(scFail.Error, "declared faults never fired") {
		t.Fatalf("expected both primary failure and teardown error preserved, got %+v", scFail)
	}
}

func TestTimeoutKillsSurvivingDescendants(t *testing.T) {
	pidFile := filepath.Join(t.TempDir(), "descendant.pid")
	// Direct child spawns a descendant that ignores SIGTERM and closes stdio,
	// while the direct child itself exits immediately on SIGTERM.
	script := `
sh -c 'trap "" TERM; sleep 30' >/dev/null 2>&1 </dev/null &
echo $! > "$1"
sleep 30
`
	ctx, cancel := context.WithTimeout(context.Background(), 300*time.Millisecond)
	defer cancel()

	err := runArgv(ctx, []string{"sh", "-c", script, "sh", pidFile}, t.TempDir(), []string{"PATH=" + os.Getenv("PATH")}, nil)
	if err == nil {
		t.Fatal("expected timeout error")
	}

	raw, readErr := os.ReadFile(pidFile)
	if readErr != nil {
		t.Fatalf("read descendant pid: %v", readErr)
	}
	pid, convErr := strconv.Atoi(strings.TrimSpace(string(raw)))
	if convErr != nil {
		t.Fatalf("parse descendant pid %q: %v", string(raw), convErr)
	}

	// Give OS a brief moment to reap the SIGKILLed descendant.
	deadline := time.Now().Add(1 * time.Second)
	for time.Now().Before(deadline) {
		if err := syscall.Kill(pid, 0); err != nil {
			return // process is gone
		}
		time.Sleep(20 * time.Millisecond)
	}
	_ = syscall.Kill(pid, syscall.SIGKILL)
	t.Fatalf("descendant process %d survived timeout cleanup", pid)
}

func TestProxyShutdownCancelsStuckCommitThenFailUpstream(t *testing.T) {
	upstreamStarted := make(chan struct{})
	upstreamCanceled := make(chan struct{})
	releaseHandler := make(chan struct{})

	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Drain request body so Go's HTTP/1 server arms client-disconnect detection.
		_, _ = io.Copy(io.Discard, r.Body)
		close(upstreamStarted)
		select {
		case <-r.Context().Done():
			close(upstreamCanceled)
		case <-releaseHandler:
		}
	}))
	defer upstream.Close()
	defer close(releaseHandler) // LIFO: unblocks handler before upstream.Close() runs.

	eng := faults.NewEngine([]config.FaultRule{{
		ID:       "stuck",
		Strategy: "commit_then_fail",
		Match:    config.FaultMatch{Method: "POST", Path: "/slow", Call: 1},
		Success:  &config.SuccessSpec{Status: 201, MeansDurableCommit: true},
		Inject:   &config.InjectSpec{Kind: "http_error", Status: 502},
	}})
	px, err := proxy.NewHTTPProxy(proxy.Options{
		ListenAddr:      "127.0.0.1:0",
		UpstreamURL:     upstream.URL,
		UpstreamTimeout: 10 * time.Second,
		Engine:          eng,
		Trace:           proxy.NewTraceLog("run-1", "sc-1", 100),
	})
	if err != nil {
		t.Fatalf("NewHTTPProxy: %v", err)
	}

	go func() {
		client := &http.Client{Timeout: 50 * time.Millisecond}
		_, _ = client.Post(px.URL()+"/slow", "application/json", strings.NewReader(`{}`))
	}()

	select {
	case <-upstreamStarted:
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for upstream handler to start")
	}

	// Drain with a short timeout -> must return error AND cancel the stuck upstream request.
	drainCtx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()
	if err := px.Shutdown(drainCtx); err == nil {
		t.Fatal("expected proxy Shutdown to return timeout error when upstream is stuck")
	}

	select {
	case <-upstreamCanceled:
	case <-time.After(2 * time.Second):
		t.Fatal("expected stuck commit_then_fail upstream request to be canceled on Shutdown timeout")
	}
}
