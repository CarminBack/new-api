package common

import (
	"crypto/tls"
	"net/http/httptrace"
	"sync"
	"time"
)

// UpstreamTiming measures one HTTP exchange, not the provider's model-only TTFT.
// Trace callbacks can run concurrently; only immutable snapshots are logged.
type UpstreamTiming struct {
	mu                               sync.Mutex
	start                            time.Time
	values                           map[string]interface{}
	dnsStart, connectStart, tlsStart time.Time
}

func NewUpstreamTiming(requestStart time.Time) *UpstreamTiming {
	now := time.Now()
	t := &UpstreamTiming{start: now, values: map[string]interface{}{}}
	if !requestStart.IsZero() {
		t.values["request_to_dispatch_ms"] = max(0, now.Sub(requestStart).Milliseconds())
	}
	return t
}

func (t *UpstreamTiming) mark(key string) {
	t.mu.Lock()
	defer t.mu.Unlock()
	if _, exists := t.values[key]; !exists {
		t.values[key] = time.Since(t.start).Milliseconds()
	}
}

func (t *UpstreamTiming) Trace() *httptrace.ClientTrace {
	return &httptrace.ClientTrace{
		DNSStart: func(httptrace.DNSStartInfo) { t.mu.Lock(); defer t.mu.Unlock(); t.dnsStart = time.Now() },
		DNSDone: func(httptrace.DNSDoneInfo) {
			t.mu.Lock()
			defer t.mu.Unlock()
			if !t.dnsStart.IsZero() {
				t.values["dns_ms"] = time.Since(t.dnsStart).Milliseconds()
			}
		},
		ConnectStart: func(string, string) { t.mu.Lock(); defer t.mu.Unlock(); t.connectStart = time.Now() },
		ConnectDone: func(string, string, error) {
			t.mu.Lock()
			defer t.mu.Unlock()
			if !t.connectStart.IsZero() {
				t.values["connect_ms"] = time.Since(t.connectStart).Milliseconds()
			}
		},
		TLSHandshakeStart: func() { t.mu.Lock(); defer t.mu.Unlock(); t.tlsStart = time.Now() },
		TLSHandshakeDone: func(tls.ConnectionState, error) {
			t.mu.Lock()
			defer t.mu.Unlock()
			if !t.tlsStart.IsZero() {
				t.values["tls_ms"] = time.Since(t.tlsStart).Milliseconds()
			}
		},
		GotConn: func(i httptrace.GotConnInfo) {
			t.mu.Lock()
			defer t.mu.Unlock()
			t.values["connection_reused"] = i.Reused
			t.values["connection_ready_ms"] = time.Since(t.start).Milliseconds()
		},
		WroteRequest: func(i httptrace.WroteRequestInfo) {
			if i.Err == nil {
				t.mark("request_written_ms")
			}
		},
		GotFirstResponseByte: func() { t.mark("first_response_byte_ms") },
	}
}

func (t *UpstreamTiming) ResponseHeaders() { t.mark("response_headers_ms") }
func (t *UpstreamTiming) FirstSSEData()    { t.mark("first_sse_data_ms") }
func (t *UpstreamTiming) Snapshot() map[string]interface{} {
	t.mu.Lock()
	defer t.mu.Unlock()
	result := make(map[string]interface{}, len(t.values))
	for k, v := range t.values {
		result[k] = v
	}
	return result
}
