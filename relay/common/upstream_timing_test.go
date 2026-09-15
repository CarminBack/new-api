package common

import (
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"net/http/httptrace"
	"testing"
	"time"
)

func TestUpstreamTimingSnapshotsDistinguishHeadersAndSSE(t *testing.T) {
	timing := NewUpstreamTiming(time.Now())
	trace := timing.Trace()
	trace.GotConn(httptrace.GotConnInfo{Reused: true})
	trace.WroteRequest(httptrace.WroteRequestInfo{})
	trace.GotFirstResponseByte()
	timing.ResponseHeaders()
	before := timing.Snapshot()
	assert.Equal(t, true, before["connection_reused"])
	assert.NotContains(t, before, "dns_ms")
	assert.NotContains(t, before, "first_sse_data_ms")
	timing.FirstSSEData()
	first := timing.Snapshot()
	require.Contains(t, first, "first_sse_data_ms")
	assert.GreaterOrEqual(t, first["first_sse_data_ms"], first["response_headers_ms"])
	timing.FirstSSEData()
	assert.Equal(t, first["first_sse_data_ms"], timing.Snapshot()["first_sse_data_ms"])
	assert.NotContains(t, before, "first_sse_data_ms")
}
