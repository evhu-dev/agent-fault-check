package proxy

import (
	"bytes"
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"sort"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/agent-fault-check/afc/internal/faults"
)

// TestProxyP95OverheadUnder2ms verifies the <= 2ms p95 proxy latency target:
// loopback upstream, warm connections, 1 KiB JSON payloads, 10 concurrent clients,
// tracing on, no faults => p95(proxied) - p95(direct) <= 2ms.
func TestProxyP95OverheadUnder2ms(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping latency benchmark in -short mode")
	}

	payload := []byte(`{"data":"` + strings.Repeat("a", 1000) + `"}`)
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write(payload)
	}))
	defer upstream.Close()

	px, err := NewHTTPProxy(Options{
		ListenAddr:      "127.0.0.1:0",
		UpstreamURL:     upstream.URL,
		UpstreamTimeout: 5 * time.Second,
		Engine:          faults.NewEngine(nil),
		Trace:           NewTraceLog("bench-run", "bench-sc", 20000),
	})
	if err != nil {
		t.Fatalf("NewHTTPProxy: %v", err)
	}
	defer px.Shutdown(context.Background())

	measure := func(targetURL string) time.Duration {
		client := &http.Client{
			Transport: &http.Transport{
				MaxIdleConns:        32,
				MaxIdleConnsPerHost: 32,
			},
		}
		defer client.CloseIdleConnections()

		// Warm up connections
		for i := 0; i < 20; i++ {
			resp, err := client.Post(targetURL, "application/json", bytes.NewReader(payload))
			if err != nil {
				t.Fatalf("warmup request failed: %v", err)
			}
			resp.Body.Close()
		}

		const workers = 10
		const perWorker = 50
		durations := make([]time.Duration, workers*perWorker)
		var wg sync.WaitGroup
		for w := 0; w < workers; w++ {
			wg.Add(1)
			go func(w int) {
				defer wg.Done()
				for i := 0; i < perWorker; i++ {
					start := time.Now()
					resp, err := client.Post(targetURL, "application/json", bytes.NewReader(payload))
					if err != nil {
						t.Errorf("request failed: %v", err)
						return
					}
					resp.Body.Close()
					durations[w*perWorker+i] = time.Since(start)
				}
			}(w)
		}
		wg.Wait()

		sort.Slice(durations, func(i, j int) bool { return durations[i] < durations[j] })
		idx := int(float64(len(durations)) * 0.95)
		if idx >= len(durations) {
			idx = len(durations) - 1
		}
		return durations[idx]
	}

	directP95 := measure(upstream.URL + "/api/v1/bench")
	proxiedP95 := measure(px.URL() + "/api/v1/bench")
	diff := proxiedP95 - directP95
	t.Logf("direct p95=%v, proxied p95=%v, overhead=%v", directP95, proxiedP95, diff)

	// Enforce the strict 2ms SLA when AFC_ENFORCE_BENCH=1 (run without -race on Linux).
	if os.Getenv("AFC_ENFORCE_BENCH") == "1" {
		if diff > 2*time.Millisecond {
			t.Fatalf("proxy p95 overhead %v exceeded 2ms target (direct=%v, proxied=%v)", diff, directP95, proxiedP95)
		}
	}
}
