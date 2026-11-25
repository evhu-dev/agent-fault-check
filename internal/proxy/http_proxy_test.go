package proxy

import (
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/agent-fault-check/afc/internal/config"
	"github.com/agent-fault-check/afc/internal/faults"
)

func TestCommitThenFail502AndPassthrough(t *testing.T) {
	var upstreamCalls atomic.Int32
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		upstreamCalls.Add(1)
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusCreated)
		_, _ = w.Write([]byte(`{"id":"ord_1"}`))
	}))
	defer upstream.Close()

	rules := []config.FaultRule{{
		ID:       "lose-first",
		Strategy: "commit_then_fail",
		Match:    config.FaultMatch{Method: "POST", Path: "/api/v1/orders", Call: 1},
		Success:  &config.SuccessSpec{Status: 201, MeansDurableCommit: true},
		Inject:   &config.InjectSpec{Kind: "http_error", Status: 502},
	}}

	eng := faults.NewEngine(rules)
	tr := NewTraceLog("run-1", "sc-1", 100)
	px, err := NewHTTPProxy(Options{
		ListenAddr:         "127.0.0.1:0",
		UpstreamURL:        upstream.URL,
		UpstreamTimeout:    2 * time.Second,
		Engine:             eng,
		Trace:              tr,
		CorrelationHeaders: []string{"X-Operation-ID"},
	})
	if err != nil {
		t.Fatalf("NewHTTPProxy: %v", err)
	}
	defer px.Shutdown(context.Background())

	// Call 1: commits upstream (201) -> proxy returns 502
	req1, _ := http.NewRequest("POST", px.URL()+"/api/v1/orders", strings.NewReader(`{}`))
	req1.Header.Set("X-Operation-ID", "op-123")
	resp1, err := http.DefaultClient.Do(req1)
	if err != nil {
		t.Fatalf("call 1: %v", err)
	}
	resp1.Body.Close()
	if resp1.StatusCode != http.StatusBadGateway {
		t.Fatalf("expected 502 on call 1, got %d", resp1.StatusCode)
	}

	// Call 2: passes through 201
	req2, _ := http.NewRequest("POST", px.URL()+"/api/v1/orders", strings.NewReader(`{}`))
	req2.Header.Set("X-Operation-ID", "op-123")
	resp2, err := http.DefaultClient.Do(req2)
	if err != nil {
		t.Fatalf("call 2: %v", err)
	}
	body2, _ := io.ReadAll(resp2.Body)
	resp2.Body.Close()
	if resp2.StatusCode != http.StatusCreated || string(body2) != `{"id":"ord_1"}` {
		t.Fatalf("expected 201 passthrough on call 2, got %d %s", resp2.StatusCode, string(body2))
	}

	if upstreamCalls.Load() != 2 {
		t.Fatalf("expected 2 upstream calls, got %d", upstreamCalls.Load())
	}
	if unfired := eng.UnfiredFaults(); len(unfired) != 0 {
		t.Fatalf("expected all faults fired, unfired=%v", unfired)
	}
	if eng.UnknownCommitError() != nil {
		t.Fatalf("unexpected unknown commit error: %v", eng.UnknownCommitError())
	}
}

func TestCommitThenFailTimeoutAttribution(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusCreated)
	}))
	defer upstream.Close()

	// Case 1: client times out before hold expires -> fault marked fired
	eng1 := faults.NewEngine([]config.FaultRule{{
		ID:       "hold-1",
		Strategy: "commit_then_fail",
		Match:    config.FaultMatch{Method: "POST", Path: "/pay", Call: 1},
		Success:  &config.SuccessSpec{Status: 201, MeansDurableCommit: true},
		Inject:   &config.InjectSpec{Kind: "timeout", HoldMs: 500},
	}})
	px1, err := NewHTTPProxy(Options{
		ListenAddr:      "127.0.0.1:0",
		UpstreamURL:     upstream.URL,
		UpstreamTimeout: 2 * time.Second,
		Engine:          eng1,
		Trace:           NewTraceLog("run-t1", "sc-t1", 100),
	})
	if err != nil {
		t.Fatalf("NewHTTPProxy: %v", err)
	}
	defer px1.Shutdown(context.Background())

	shortClient := &http.Client{Timeout: 60 * time.Millisecond}
	_, err = shortClient.Post(px1.URL()+"/pay", "application/json", strings.NewReader(`{}`))
	if err == nil {
		t.Fatal("expected client timeout error")
	}
	time.Sleep(30 * time.Millisecond)
	if len(eng1.UnfiredFaults()) != 0 {
		t.Fatalf("expected timeout fault to be marked fired")
	}

	// Case 2: hold expires before client timeout -> 504 returned, fault marked unfired
	eng2 := faults.NewEngine([]config.FaultRule{{
		ID:       "hold-2",
		Strategy: "commit_then_fail",
		Match:    config.FaultMatch{Method: "POST", Path: "/pay", Call: 1},
		Success:  &config.SuccessSpec{Status: 201, MeansDurableCommit: true},
		Inject:   &config.InjectSpec{Kind: "timeout", HoldMs: 30},
	}})
	px2, err := NewHTTPProxy(Options{
		ListenAddr:      "127.0.0.1:0",
		UpstreamURL:     upstream.URL,
		UpstreamTimeout: 2 * time.Second,
		Engine:          eng2,
		Trace:           NewTraceLog("run-t2", "sc-t2", 100),
	})
	if err != nil {
		t.Fatalf("NewHTTPProxy: %v", err)
	}
	defer px2.Shutdown(context.Background())

	resp, err := http.Post(px2.URL()+"/pay", "application/json", strings.NewReader(`{}`))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusGatewayTimeout {
		t.Fatalf("expected 504 when hold expires, got %d", resp.StatusCode)
	}
	if len(eng2.UnfiredFaults()) != 1 {
		t.Fatalf("expected fault to remain unfired when hold expired first")
	}
}

func TestFailBeforeUpstreamAndMalformedJSONAndBudget(t *testing.T) {
	var hits atomic.Int32
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits.Add(1)
		if r.URL.Path == "/json" {
			w.Header().Set("Content-Type", "application/json")
			w.Header().Set("ETag", `"abc"`)
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte(`{"ok":true}`))
			return
		}
		w.Header().Set("Content-Type", "text/plain")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("plain text"))
	}))
	defer upstream.Close()

	rules := []config.FaultRule{
		{
			ID:       "abort-step",
			Strategy: "fail_before_upstream",
			Match:    config.FaultMatch{Method: "POST", Path: "/charge", Call: 1},
			Inject:   &config.InjectSpec{Kind: "http_error", Status: 503},
		},
		{
			ID:       "corrupt-json",
			Strategy: "malformed_json",
			Match:    config.FaultMatch{Method: "GET", Path: "/json", Call: 1},
		},
		{
			ID:       "non-json-unfired",
			Strategy: "malformed_json",
			Match:    config.FaultMatch{Method: "GET", Path: "/text", Call: 1},
		},
	}

	eng := faults.NewEngine(rules)
	px, err := NewHTTPProxy(Options{
		ListenAddr:          "127.0.0.1:0",
		UpstreamURL:         upstream.URL,
		UpstreamTimeout:     2 * time.Second,
		Engine:              eng,
		Trace:               NewTraceLog("run-2", "sc-2", 100),
		AbortAfterToolCalls: 3,
	})
	if err != nil {
		t.Fatalf("NewHTTPProxy: %v", err)
	}
	defer px.Shutdown(context.Background())

	// 1. fail_before_upstream -> 503, 0 upstream hits
	r1, err := http.Post(px.URL()+"/charge", "application/json", nil)
	if err != nil {
		t.Fatalf("post /charge: %v", err)
	}
	r1.Body.Close()
	if r1.StatusCode != 503 || hits.Load() != 0 {
		t.Fatalf("expected 503 with 0 upstream hits, got %d hits=%d", r1.StatusCode, hits.Load())
	}

	// 2. malformed_json on valid JSON -> 200 with {"fault": and no ETag
	r2, err := http.Get(px.URL() + "/json")
	if err != nil {
		t.Fatalf("get /json: %v", err)
	}
	b2, _ := io.ReadAll(r2.Body)
	r2.Body.Close()
	if r2.StatusCode != 200 || string(b2) != `{"fault":` || r2.Header.Get("ETag") != "" {
		t.Fatalf("unexpected malformed_json result: status=%d body=%q etag=%q", r2.StatusCode, string(b2), r2.Header.Get("ETag"))
	}

	// 3. malformed_json on text/plain -> passthrough, unfired
	r3, err := http.Get(px.URL() + "/text")
	if err != nil {
		t.Fatalf("get /text: %v", err)
	}
	b3, _ := io.ReadAll(r3.Body)
	r3.Body.Close()
	if string(b3) != "plain text" {
		t.Fatalf("expected plain text passthrough, got %q", string(b3))
	}
	if unfired := eng.UnfiredFaults(); len(unfired) != 1 || unfired[0] != "non-json-unfired" {
		t.Fatalf("expected only non-json-unfired to be unfired, got %v", unfired)
	}

	// 4. 4th call trips abort_after_tool_calls=3 -> 429
	r4, err := http.Get(px.URL() + "/text")
	if err != nil {
		t.Fatalf("get /text (4th): %v", err)
	}
	r4.Body.Close()
	if r4.StatusCode != http.StatusTooManyRequests || !px.BudgetAborted() {
		t.Fatalf("expected 429 budget abort on 4th call, got %d aborted=%v", r4.StatusCode, px.BudgetAborted())
	}
}

func TestConcurrentAdmissionRaceFree(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	defer upstream.Close()

	eng := faults.NewEngine(nil)
	px, err := NewHTTPProxy(Options{
		ListenAddr:      "127.0.0.1:0",
		UpstreamURL:     upstream.URL,
		UpstreamTimeout: 2 * time.Second,
		Engine:          eng,
		Trace:           NewTraceLog("run-c", "sc-c", 500),
	})
	if err != nil {
		t.Fatalf("NewHTTPProxy: %v", err)
	}
	defer px.Shutdown(context.Background())

	var wg sync.WaitGroup
	for i := 0; i < 20; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			resp, err := http.Get(px.URL() + "/ping")
			if err == nil {
				resp.Body.Close()
			}
		}()
	}
	wg.Wait()
	if got := eng.CountFor("GET", "/ping"); got != 20 {
		t.Fatalf("expected 20 admitted calls, got %d", got)
	}
}

type errReader struct{}

func (errReader) Read([]byte) (int, error) { return 0, errors.New("connection reset by peer") }

func TestProxyUnitHelpers(t *testing.T) {
	// 1. joinURLPath preserves base path with RawPath
	base, _ := url.Parse("http://127.0.0.1:8000/v1")
	reqURL, _ := url.Parse("/orders/a%2Fb")
	p, rp := joinURLPath(base, reqURL)
	if p != "/v1/orders/a/b" || rp != "/v1/orders/a%2Fb" {
		t.Fatalf("unexpected joinURLPath: path=%q rawpath=%q", p, rp)
	}

	// 2. copyHeaders strips connection-nominated hop-by-hop headers
	src := http.Header{
		"Connection":   []string{"X-Custom-Hop, Keep-Alive"},
		"X-Custom-Hop": []string{"secret"},
		"X-Keep":       []string{"ok"},
	}
	dst := make(http.Header)
	copyHeaders(dst, src)
	if dst.Get("X-Custom-Hop") != "" || dst.Get("Connection") != "" || dst.Get("X-Keep") != "ok" {
		t.Fatalf("unexpected copied headers: %v", dst)
	}

	// 3. extractCorrelations captures all headers deterministically
	px := &HTTPProxy{corrHeaders: []string{"X-Operation-Id", "X-Other-Id"}}
	req := httptest.NewRequest("POST", "/orders", nil)
	req.Header.Set("X-Operation-ID", "op-1")
	req.Header.Set("X-Other-ID", "other-2")
	got := px.extractCorrelations(req)
	if got["X-Operation-Id"] != "op-1" || got["X-Other-Id"] != "other-2" {
		t.Fatalf("unexpected correlations: %v", got)
	}

	// 4. readBoundedRequestBody distinguishes client read error from MaxBytesError
	rec := httptest.NewRecorder()
	badReq := httptest.NewRequest("POST", "/orders", errReader{})
	if _, ok := px.readBoundedRequestBody(rec, badReq); ok {
		t.Fatal("expected read error to fail")
	}
	if px.BodyOversizeError() != nil {
		t.Fatalf("client read error should not set BodyOversizeError: %v", px.BodyOversizeError())
	}
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("expected 400 for client read error, got %d", rec.Code)
	}

	recBig := httptest.NewRecorder()
	bigReq := httptest.NewRequest("POST", "/orders", strings.NewReader(strings.Repeat("a", MaxBodyBytes+10)))
	if _, ok := px.readBoundedRequestBody(recBig, bigReq); ok {
		t.Fatal("expected oversize body to fail")
	}
	if px.BodyOversizeError() == nil {
		t.Fatal("expected BodyOversizeError to be set for oversize body")
	}
	if recBig.Code != http.StatusRequestEntityTooLarge {
		t.Fatalf("expected 413 for oversize body, got %d", recBig.Code)
	}
}
