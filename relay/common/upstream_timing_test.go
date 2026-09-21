package common

import (
	"net/http"
	"net/http/httptrace"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestUpstreamTimingSnapshotsSafeHeadersAndSSE(t *testing.T) {
	timing := NewUpstreamTiming(time.Now())
	trace := timing.Trace()
	trace.GotConn(httptrace.GotConnInfo{Reused: true})
	trace.WroteRequest(httptrace.WroteRequestInfo{})
	trace.GotFirstResponseByte()
	timing.ResponseHeaders(http.Header{
		"Server-Timing":               []string{"queue;dur=31200, model;dur=800"},
		"X-Ttft-Ms":                   []string{"31200"},
		"Authorization":               []string{"Bearer must-not-be-logged"},
		"Set-Cookie":                  []string{"session=must-not-be-logged"},
		"X-Unrelated-Internal-Header": []string{"must-not-be-logged"},
	})
	before := timing.Snapshot()
	assert.Equal(t, "queue;dur=31200, model;dur=800", before["response_header_server_timing"])
	assert.Equal(t, "31200", before["response_header_x_ttft_ms"])
	assert.NotContains(t, before, "response_header_authorization")
	assert.NotContains(t, before, "response_header_set_cookie")
	assert.NotContains(t, before, "response_header_x_unrelated_internal_header")
	assert.Equal(t, true, before["connection_reused"])
	assert.NotContains(t, before, "first_sse_data_ms")

	timing.FirstSSEData()
	first := timing.Snapshot()
	require.Contains(t, first, "first_sse_data_ms")
	assert.GreaterOrEqual(t, first["first_sse_data_ms"], first["response_headers_ms"])
	timing.FirstSSEData()
	assert.Equal(t, first["first_sse_data_ms"], timing.Snapshot()["first_sse_data_ms"])
}

func TestUpstreamTimingTruncatesHeaderValues(t *testing.T) {
	timing := NewUpstreamTiming(time.Time{})
	timing.ResponseHeaders(http.Header{"Server-Timing": []string{string(make([]byte, 700))}})
	value, ok := timing.Snapshot()["response_header_server_timing"].(string)
	require.True(t, ok)
	assert.Len(t, value, 512)
}
