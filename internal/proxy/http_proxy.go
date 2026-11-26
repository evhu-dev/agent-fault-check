package proxy

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/agent-fault-check/afc/internal/config"
	"github.com/agent-fault-check/afc/internal/faults"
)

const (
	MaxBodyBytes        = 1 << 20 // 1 MiB
	malformedJSONBody   = `{"fault":`
	defaultSyntheticErr = `{"error":"injected fault"}`
)

// HTTPProxy is a loopback fault-injecting HTTP reverse proxy for a single scenario.
type HTTPProxy struct {
	server        *http.Server
	listener      net.Listener
	upstreamURL   *url.URL
	client        *http.Client
	engine        *faults.Engine
	trace         *TraceLog
	abortAfter    int
	corrHeaders   []string
	baseCtx       context.Context
	cancelBase    context.CancelFunc
	mu            sync.Mutex
	budgetAborted bool
	bodyOversize  error
}

// Options configures an HTTPProxy instance.
type Options struct {
	ListenAddr          string
	UpstreamURL         string
	UpstreamTimeout     time.Duration
	Engine              *faults.Engine
	Trace               *TraceLog
	AbortAfterToolCalls int
	CorrelationHeaders  []string
}

// NewHTTPProxy creates and starts a loopback HTTPProxy.
func NewHTTPProxy(opts Options) (*HTTPProxy, error) {
	u, err := url.Parse(opts.UpstreamURL)
	if err != nil {
		return nil, fmt.Errorf("parse upstream_url: %w", err)
	}
	ln, err := net.Listen("tcp", opts.ListenAddr)
	if err != nil {
		return nil, fmt.Errorf("listen %q: %w", opts.ListenAddr, err)
	}

	seenCorr := make(map[string]bool, len(opts.CorrelationHeaders))
	var corr []string
	for _, h := range opts.CorrelationHeaders {
		if h != "" {
			canon := http.CanonicalHeaderKey(h)
			if !seenCorr[canon] {
				seenCorr[canon] = true
				corr = append(corr, canon)
			}
		}
	}

	baseCtx, cancelBase := context.WithCancel(context.Background())

	p := &HTTPProxy{
		listener:    ln,
		upstreamURL: u,
		client: &http.Client{
			Timeout: opts.UpstreamTimeout,
			CheckRedirect: func(*http.Request, []*http.Request) error {
				return http.ErrUseLastResponse
			},
			Transport: &http.Transport{
				Proxy:                 nil,
				DialContext:           (&net.Dialer{Timeout: opts.UpstreamTimeout}).DialContext,
				MaxIdleConns:          64,
				IdleConnTimeout:       30 * time.Second,
				DisableCompression:    true,
				ExpectContinueTimeout: 1 * time.Second,
			},
		},
		engine:      opts.Engine,
		trace:       opts.Trace,
		abortAfter:  opts.AbortAfterToolCalls,
		corrHeaders: corr,
		baseCtx:     baseCtx,
		cancelBase:  cancelBase,
	}

	p.server = &http.Server{
		Handler:           p,
		ReadHeaderTimeout: 10 * time.Second,
	}
	go func() {
		_ = p.server.Serve(ln)
	}()
	return p, nil
}

// URL returns the bound loopback base URL (e.g., "http://127.0.0.1:54321").
func (p *HTTPProxy) URL() string {
	return "http://" + p.listener.Addr().String()
}

// Shutdown drains active connections cleanly. If draining fails or times out,
// pending upstream work is canceled and active connections are force-closed.
func (p *HTTPProxy) Shutdown(ctx context.Context) error {
	err := p.server.Shutdown(ctx)
	p.cancelBase()
	if err != nil {
		_ = p.server.Close()
	}
	p.client.CloseIdleConnections()
	return err
}

// BudgetAborted reports whether a live abort_after_tool_calls limit was tripped.
func (p *HTTPProxy) BudgetAborted() bool {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.budgetAborted
}

// BodyOversizeError returns an error if a request or response exceeded MaxBodyBytes.
func (p *HTTPProxy) BodyOversizeError() error {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.bodyOversize
}

func (p *HTTPProxy) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if isUnsupportedUpgrade(r) {
		writeJSONError(w, http.StatusNotImplemented, `{"error":"streaming or upgraded requests are not supported"}`)
		return
	}

	method := strings.ToUpper(r.Method)
	path := r.URL.EscapedPath()
	if path == "" {
		path = "/"
	}

	callIdx, totalCalls, rule := p.engine.Admit(method, path)
	corrVals := p.extractCorrelations(r)
	faultID := ""
	if rule != nil && !(p.abortAfter > 0 && totalCalls > p.abortAfter) {
		faultID = rule.ID
	}

	p.trace.Record(TraceEvent{
		Method:            method,
		Path:              path,
		CallIndex:         callIdx,
		FaultID:           faultID,
		Event:             EventAdmitted,
		CorrelationValues: corrVals,
	})

	if p.abortAfter > 0 && totalCalls > p.abortAfter {
		p.mu.Lock()
		p.budgetAborted = true
		p.mu.Unlock()
		p.trace.Record(TraceEvent{
			Method:            method,
			Path:              path,
			CallIndex:         callIdx,
			Event:             EventBudgetAborted,
			Status:            http.StatusTooManyRequests,
			CorrelationValues: corrVals,
		})
		writeJSONError(w, http.StatusTooManyRequests, `{"error":"tool call budget exceeded"}`)
		return
	}

	reqBody, ok := p.readBoundedRequestBody(w, r)
	if !ok {
		return
	}

	if rule != nil && rule.Strategy == "fail_before_upstream" {
		p.trace.Record(TraceEvent{
			Method:            method,
			Path:              path,
			CallIndex:         callIdx,
			FaultID:           rule.ID,
			Event:             EventFaultInjected,
			Status:            rule.Inject.Status,
			CorrelationValues: corrVals,
		})
		p.engine.MarkFired(rule.ID)
		writeJSONError(w, rule.Inject.Status, defaultSyntheticErr)
		p.trace.Record(TraceEvent{
			Method:    method,
			Path:      path,
			CallIndex: callIdx,
			FaultID:   rule.ID,
			Event:     EventCompleted,
			Status:    rule.Inject.Status,
		})
		return
	}

	p.trace.Record(TraceEvent{
		Method:    method,
		Path:      path,
		CallIndex: callIdx,
		FaultID:   faultID,
		Event:     EventForwarded,
	})

	detachUpstreamCancel := rule != nil && rule.Strategy == "commit_then_fail"
	resp, respBody, err := p.forwardUpstream(r, reqBody, detachUpstreamCancel)
	if err != nil {
		p.trace.Record(TraceEvent{
			Method:    method,
			Path:      path,
			CallIndex: callIdx,
			FaultID:   faultID,
			Event:     EventUpstreamError,
		})
		if errors.Is(err, errUnsupportedStreaming) {
			writeJSONError(w, http.StatusNotImplemented, `{"error":"streaming upstream responses are not supported"}`)
			return
		}
		if rule != nil && rule.Strategy == "commit_then_fail" {
			p.engine.RecordUnknownCommit(fmt.Errorf("fault %q: upstream call failed with unknown commit outcome: %w", rule.ID, err))
			p.trace.Record(TraceEvent{
				Method:    method,
				Path:      path,
				CallIndex: callIdx,
				FaultID:   rule.ID,
				Event:     EventFaultUnfired,
			})
		}
		writeJSONError(w, http.StatusBadGateway, `{"error":"upstream request failed"}`)
		return
	}

	if rule == nil {
		p.trace.Record(TraceEvent{
			Method:    method,
			Path:      path,
			CallIndex: callIdx,
			Event:     EventUpstreamOK,
			Status:    resp.StatusCode,
		})
		writeUpstreamResponse(w, resp.Header, resp.StatusCode, respBody)
		p.trace.Record(TraceEvent{
			Method:    method,
			Path:      path,
			CallIndex: callIdx,
			Event:     EventCompleted,
			Status:    resp.StatusCode,
		})
		return
	}

	p.handleTargetedResponse(w, r, rule, callIdx, method, path, corrVals, resp, respBody)
}

func (p *HTTPProxy) handleTargetedResponse(
	w http.ResponseWriter,
	r *http.Request,
	rule *config.FaultRule,
	callIdx int,
	method, path string,
	corrVals map[string]string,
	resp *http.Response,
	respBody []byte,
) {
	switch rule.Strategy {
	case "commit_then_fail":
		if resp.StatusCode != rule.Success.Status {
			p.trace.Record(TraceEvent{
				Method:    method,
				Path:      path,
				CallIndex: callIdx,
				FaultID:   rule.ID,
				Event:     EventUpstreamNonMatch,
				Status:    resp.StatusCode,
			})
			p.trace.Record(TraceEvent{
				Method:    method,
				Path:      path,
				CallIndex: callIdx,
				FaultID:   rule.ID,
				Event:     EventFaultUnfired,
			})
			writeUpstreamResponse(w, resp.Header, resp.StatusCode, respBody)
			p.trace.Record(TraceEvent{
				Method:    method,
				Path:      path,
				CallIndex: callIdx,
				FaultID:   rule.ID,
				Event:     EventCompleted,
				Status:    resp.StatusCode,
			})
			return
		}

		p.trace.Record(TraceEvent{
			Method:    method,
			Path:      path,
			CallIndex: callIdx,
			FaultID:   rule.ID,
			Event:     EventUpstreamOK,
			Status:    resp.StatusCode,
		})

		if rule.Inject.Kind == "http_error" {
			p.engine.MarkFired(rule.ID)
			p.trace.Record(TraceEvent{
				Method:            method,
				Path:              path,
				CallIndex:         callIdx,
				FaultID:           rule.ID,
				Event:             EventFaultInjected,
				Status:            rule.Inject.Status,
				CorrelationValues: corrVals,
			})
			writeJSONError(w, rule.Inject.Status, defaultSyntheticErr)
			p.trace.Record(TraceEvent{
				Method:    method,
				Path:      path,
				CallIndex: callIdx,
				FaultID:   rule.ID,
				Event:     EventCompleted,
				Status:    rule.Inject.Status,
			})
			return
		}

		// kind == "timeout": withhold response until client disconnects or hold_ms expires.
		timer := time.NewTimer(time.Duration(rule.Inject.HoldMs) * time.Millisecond)
		defer timer.Stop()

		select {
		case <-r.Context().Done():
			p.engine.MarkFired(rule.ID)
			p.trace.Record(TraceEvent{
				Method:            method,
				Path:              path,
				CallIndex:         callIdx,
				FaultID:           rule.ID,
				Event:             EventFaultInjected,
				Status:            499,
				CorrelationValues: corrVals,
			})
			p.trace.Record(TraceEvent{
				Method:    method,
				Path:      path,
				CallIndex: callIdx,
				FaultID:   rule.ID,
				Event:     EventCompleted,
				Status:    499,
			})
		case <-timer.C:
			p.engine.MarkFired(rule.ID)
			p.trace.Record(TraceEvent{
				Method:            method,
				Path:              path,
				CallIndex:         callIdx,
				FaultID:           rule.ID,
				Event:             EventFaultInjected,
				Status:            http.StatusGatewayTimeout,
				CorrelationValues: corrVals,
			})
			writeJSONError(w, http.StatusGatewayTimeout, `{"error":"injected timeout hold expired"}`)
			p.trace.Record(TraceEvent{
				Method:    method,
				Path:      path,
				CallIndex: callIdx,
				FaultID:   rule.ID,
				Event:     EventCompleted,
				Status:    http.StatusGatewayTimeout,
			})
		}

	case "malformed_json":
		ct := strings.ToLower(resp.Header.Get("Content-Type"))
		isJSONContentType := ct == "" || strings.Contains(ct, "json")
		if !isJSONContentType || !json.Valid(respBody) {
			p.trace.Record(TraceEvent{
				Method:    method,
				Path:      path,
				CallIndex: callIdx,
				FaultID:   rule.ID,
				Event:     EventFaultUnfired,
				Status:    resp.StatusCode,
			})
			writeUpstreamResponse(w, resp.Header, resp.StatusCode, respBody)
			p.trace.Record(TraceEvent{
				Method:    method,
				Path:      path,
				CallIndex: callIdx,
				FaultID:   rule.ID,
				Event:     EventCompleted,
				Status:    resp.StatusCode,
			})
			return
		}

		p.engine.MarkFired(rule.ID)
		p.trace.Record(TraceEvent{
			Method:            method,
			Path:              path,
			CallIndex:         callIdx,
			FaultID:           rule.ID,
			Event:             EventFaultInjected,
			Status:            resp.StatusCode,
			CorrelationValues: corrVals,
		})
		hdr := resp.Header.Clone()
		hdr.Del("Content-Encoding")
		hdr.Del("ETag")
		hdr.Del("Content-MD5")
		hdr.Del("Digest")
		hdr.Set("Content-Type", "application/json")
		hdr.Set("Content-Length", strconv.Itoa(len(malformedJSONBody)))
		writeUpstreamResponse(w, hdr, resp.StatusCode, []byte(malformedJSONBody))
		p.trace.Record(TraceEvent{
			Method:    method,
			Path:      path,
			CallIndex: callIdx,
			FaultID:   rule.ID,
			Event:     EventCompleted,
			Status:    resp.StatusCode,
		})
	}
}

var errUnsupportedStreaming = errors.New("upstream returned unsupported streaming response")

func (p *HTTPProxy) forwardUpstream(inReq *http.Request, reqBody []byte, detachCancel bool) (*http.Response, []byte, error) {
	targetURL := *p.upstreamURL
	targetURL.Path, targetURL.RawPath = joinURLPath(p.upstreamURL, inReq.URL)
	targetURL.RawQuery = inReq.URL.RawQuery

	reqCtx := inReq.Context()
	if detachCancel {
		// Detach client disconnect so commit_then_fail does not abort upstream mid-commit,
		// while still tying lifetime to p.baseCtx so proxy shutdown can cancel stuck work.
		reqCtx = p.baseCtx
	}
	outReq, err := http.NewRequestWithContext(reqCtx, inReq.Method, targetURL.String(), bytes.NewReader(reqBody))
	if err != nil {
		return nil, nil, err
	}
	copyHeaders(outReq.Header, inReq.Header)
	outReq.Header.Del("Accept-Encoding") // Ensure uncompressed upstream JSON for bounded inspection.
	outReq.ContentLength = int64(len(reqBody))

	resp, err := p.client.Do(outReq)
	if err != nil {
		return nil, nil, err
	}
	defer resp.Body.Close()

	if strings.Contains(strings.ToLower(resp.Header.Get("Content-Type")), "text/event-stream") {
		return nil, nil, errUnsupportedStreaming
	}

	limited := io.LimitReader(resp.Body, MaxBodyBytes+1)
	body, err := io.ReadAll(limited)
	if err != nil {
		return nil, nil, err
	}
	if len(body) > MaxBodyBytes {
		p.mu.Lock()
		if p.bodyOversize == nil {
			p.bodyOversize = fmt.Errorf("upstream response exceeded %d bytes", MaxBodyBytes)
		}
		p.mu.Unlock()
		return nil, nil, errors.New("upstream response exceeds buffering limit")
	}
	return resp, body, nil
}

func (p *HTTPProxy) readBoundedRequestBody(w http.ResponseWriter, r *http.Request) ([]byte, bool) {
	r.Body = http.MaxBytesReader(w, r.Body, MaxBodyBytes)
	body, err := io.ReadAll(r.Body)
	if err != nil {
		var maxBytesErr *http.MaxBytesError
		if errors.As(err, &maxBytesErr) {
			p.mu.Lock()
			if p.bodyOversize == nil {
				p.bodyOversize = fmt.Errorf("request body exceeded %d bytes: %w", MaxBodyBytes, err)
			}
			p.mu.Unlock()
			writeJSONError(w, http.StatusRequestEntityTooLarge, `{"error":"request body exceeds limit"}`)
			return nil, false
		}
		writeJSONError(w, http.StatusBadRequest, `{"error":"failed to read request body"}`)
		return nil, false
	}
	return body, true
}

func (p *HTTPProxy) extractCorrelations(r *http.Request) map[string]string {
	if len(p.corrHeaders) == 0 {
		return nil
	}
	var out map[string]string
	for _, h := range p.corrHeaders {
		if v := strings.TrimSpace(r.Header.Get(h)); v != "" {
			if out == nil {
				out = make(map[string]string, len(p.corrHeaders))
			}
			out[h] = v
		}
	}
	return out
}

func isUnsupportedUpgrade(r *http.Request) bool {
	if r.Header.Get("Upgrade") != "" {
		return true
	}
	return strings.Contains(strings.ToLower(r.Header.Get("Accept")), "text/event-stream")
}

func writeJSONError(w http.ResponseWriter, status int, body string) {
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Content-Length", strconv.Itoa(len(body)))
	w.WriteHeader(status)
	_, _ = w.Write([]byte(body))
}

func writeUpstreamResponse(w http.ResponseWriter, src http.Header, status int, body []byte) {
	copyHeaders(w.Header(), src)
	w.Header().Set("Content-Length", strconv.Itoa(len(body)))
	w.WriteHeader(status)
	_, _ = w.Write(body)
}

var hopByHopHeaders = map[string]bool{
	"Connection":          true,
	"Keep-Alive":          true,
	"Proxy-Authenticate":  true,
	"Proxy-Authorization": true,
	"Te":                  true,
	"Trailers":            true,
	"Transfer-Encoding":   true,
	"Upgrade":             true,
}

func copyHeaders(dst, src http.Header) {
	var connHop map[string]bool
	for _, connVal := range src.Values("Connection") {
		for _, tok := range strings.Split(connVal, ",") {
			if name := strings.TrimSpace(tok); name != "" {
				if connHop == nil {
					connHop = make(map[string]bool)
				}
				connHop[http.CanonicalHeaderKey(name)] = true
			}
		}
	}
	for k, vals := range src {
		canon := http.CanonicalHeaderKey(k)
		if hopByHopHeaders[canon] || connHop[canon] {
			continue
		}
		for _, v := range vals {
			dst.Add(k, v)
		}
	}
}

func joinURLPath(a, b *url.URL) (path, rawpath string) {
	if a.RawPath == "" && b.RawPath == "" {
		return singleJoiningSlash(a.Path, b.Path), ""
	}
	apath := a.EscapedPath()
	bpath := b.EscapedPath()
	aslash := strings.HasSuffix(apath, "/")
	bslash := strings.HasPrefix(bpath, "/")
	switch {
	case aslash && bslash:
		return a.Path + b.Path[1:], apath + bpath[1:]
	case !aslash && !bslash:
		return a.Path + "/" + b.Path, apath + "/" + bpath
	}
	return a.Path + b.Path, apath + bpath
}

func singleJoiningSlash(a, b string) string {
	aslash := strings.HasSuffix(a, "/")
	bslash := strings.HasPrefix(b, "/")
	switch {
	case aslash && bslash:
		return a + b[1:]
	case !aslash && !bslash:
		return a + "/" + b
	}
	return a + b
}
