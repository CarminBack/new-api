package common

import (
	"crypto/tls"
	"github.com/tidwall/gjson"
	"net/http"
	"net/http/httptrace"
	"strings"
	"sync"
	"time"
)

// UpstreamTiming measures one HTTP exchange, not provider model-only TTFT.
type UpstreamTiming struct {
	mu                               sync.Mutex
	start                            time.Time
	values                           map[string]any
	dnsStart, connectStart, tlsStart time.Time
}

func NewUpstreamTiming(requestStart time.Time) *UpstreamTiming {
	now := time.Now()
	timing := &UpstreamTiming{start: now, values: map[string]any{}}
	if !requestStart.IsZero() {
		timing.values["request_to_dispatch_ms"] = max(0, now.Sub(requestStart).Milliseconds())
	}
	return timing
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
		DNSStart: func(httptrace.DNSStartInfo) {
			t.mu.Lock()
			defer t.mu.Unlock()
			t.dnsStart = time.Now()
		},
		DNSDone: func(httptrace.DNSDoneInfo) {
			t.mu.Lock()
			defer t.mu.Unlock()
			if !t.dnsStart.IsZero() {
				t.values["dns_ms"] = time.Since(t.dnsStart).Milliseconds()
			}
		},
		ConnectStart: func(string, string) {
			t.mu.Lock()
			defer t.mu.Unlock()
			t.connectStart = time.Now()
		},
		ConnectDone: func(string, string, error) {
			t.mu.Lock()
			defer t.mu.Unlock()
			if !t.connectStart.IsZero() {
				t.values["connect_ms"] = time.Since(t.connectStart).Milliseconds()
			}
		},
		TLSHandshakeStart: func() {
			t.mu.Lock()
			defer t.mu.Unlock()
			t.tlsStart = time.Now()
		},
		TLSHandshakeDone: func(tls.ConnectionState, error) {
			t.mu.Lock()
			defer t.mu.Unlock()
			if !t.tlsStart.IsZero() {
				t.values["tls_ms"] = time.Since(t.tlsStart).Milliseconds()
			}
		},
		GotConn: func(info httptrace.GotConnInfo) {
			t.mu.Lock()
			defer t.mu.Unlock()
			t.values["connection_reused"] = info.Reused
			t.values["connection_ready_ms"] = time.Since(t.start).Milliseconds()
		},
		WroteRequest: func(info httptrace.WroteRequestInfo) {
			if info.Err == nil {
				t.mark("request_written_ms")
			}
		},
		GotFirstResponseByte: func() { t.mark("first_response_byte_ms") },
	}
}

func (t *UpstreamTiming) ResponseHeaders(headers http.Header) {
	t.mark("response_headers_ms")
	for _, name := range []string{
		"Server-Timing",
		"X-Openai-Processing-Ms",
		"Openai-Processing-Ms",
		"X-Upstream-Response-Time",
		"X-Response-Time",
		"X-Processing-Time",
		"X-Request-Duration",
		"X-Envoy-Upstream-Service-Time",
		"X-Ttft",
		"X-Ttft-Ms",
	} {
		value := strings.Join(headers.Values(name), ", ")
		if value == "" {
			continue
		}
		if len(value) > 512 {
			value = value[:512]
		}
		t.mu.Lock()
		t.values["response_header_"+strings.ToLower(strings.ReplaceAll(name, "-", "_"))] = value
		t.mu.Unlock()
	}
}

func (t *UpstreamTiming) FirstSSEData() { t.mark("first_sse_data_ms") }

// FirstMeaningfulData excludes role/created/usage events and keepalives.
// Unknown protocols produce no sample rather than a misleading zero latency.
func (t *UpstreamTiming) FirstMeaningfulData(data string) {
	value := gjson.Parse(data)
	meaningful := false
	switch value.Get("type").String() {
	case "response.output_text.delta", "response.reasoning_text.delta", "response.reasoning_summary_text.delta", "response.function_call_arguments.delta":
		meaningful = value.Get("delta").Type == gjson.String && value.Get("delta").String() != ""
	case "content_block_delta":
		for _, key := range []string{"delta.text", "delta.thinking", "delta.partial_json"} {
			if value.Get(key).Type == gjson.String && value.Get(key).String() != "" {
				meaningful = true
			}
		}
	default:
		for _, choice := range value.Get("choices").Array() {
			for _, key := range []string{"text", "delta.content", "delta.reasoning_content", "delta.reasoning"} {
				if choice.Get(key).Type == gjson.String && choice.Get(key).String() != "" {
					meaningful = true
				}
			}
			for _, call := range choice.Get("delta.tool_calls").Array() {
				if call.Get("function.arguments").String() != "" {
					meaningful = true
				}
			}
		}
	}
	if meaningful {
		t.mark("first_meaningful_data_ms")
	}
}
func (t *UpstreamTiming) Snapshot() map[string]any {
	t.mu.Lock()
	defer t.mu.Unlock()
	result := make(map[string]any, len(t.values))
	for key, value := range t.values {
		result[key] = value
	}
	return result
}
