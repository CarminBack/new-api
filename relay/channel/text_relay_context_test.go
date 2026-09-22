package channel

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/QuantumNous/new-api/common"
	relaycommon "github.com/QuantumNous/new-api/relay/common"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

func TestTextUpstreamCancellationBeforeHeadersAndDuringStream(t *testing.T) {
	for _, stream := range []bool{false, true} {
		name := "headers"
		if stream {
			name = "stream"
		}
		t.Run(name, func(t *testing.T) {
			entered := make(chan struct{})
			canceled := make(chan struct{})
			release := make(chan struct{})
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				_, _ = io.Copy(io.Discard, r.Body)
				if stream {
					w.Header().Set("Content-Type", "text/event-stream")
					w.WriteHeader(200)
					w.(http.Flusher).Flush()
				}
				close(entered)
				select {
				case <-r.Context().Done():
					close(canceled)
				case <-release:
				}
			}))
			defer server.Close()
			defer close(release)
			parent, cancel := context.WithCancel(context.Background())
			defer cancel()
			c, _ := gin.CreateTestContext(httptest.NewRecorder())
			c.Request = httptest.NewRequest("POST", "/v1/responses", strings.NewReader(`{}`)).WithContext(parent)
			req, err := http.NewRequest("POST", server.URL, strings.NewReader(`{}`))
			require.NoError(t, err)
			info := &relaycommon.RelayInfo{ChannelMeta: &relaycommon.ChannelMeta{}}
			finished := make(chan error, 1)
			bodyReady := make(chan struct{})
			go func() {
				resp, err := doRequest(c, req, info)
				if err == nil {
					defer resp.Body.Close()
					close(bodyReady)
					_, err = io.ReadAll(resp.Body)
				}
				finished <- err
			}()
			select {
			case <-entered:
			case <-time.After(3 * time.Second):
				t.Fatal("upstream was not reached")
			}
			if stream {
				select {
				case <-bodyReady:
				case <-time.After(3 * time.Second):
					t.Fatal("headers never returned")
				}
			}
			select {
			case <-canceled:
				t.Fatal("upstream canceled before client cancellation")
			default:
			}
			cancel()
			select {
			case err := <-finished:
				require.Error(t, err)
			case <-time.After(3 * time.Second):
				t.Fatal("upstream read did not cancel")
			}
			select {
			case <-canceled:
			case <-time.After(3 * time.Second):
				t.Fatal("server did not observe cancellation")
			}
		})
	}
}

func TestTextFirstResponseTimeoutCancelsOnlyTheAttempt(t *testing.T) {
	originalTimeout := common.TextFirstResponseTimeout
	common.TextFirstResponseTimeout = 1
	t.Cleanup(func() { common.TextFirstResponseTimeout = originalTimeout })
	for _, sendHeaders := range []bool{false, true} {
		t.Run(fmt.Sprint(sendHeaders), func(t *testing.T) {
			release := make(chan struct{})
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				_, _ = io.Copy(io.Discard, r.Body)
				if sendHeaders {
					w.WriteHeader(200)
					w.(http.Flusher).Flush()
				}
				select {
				case <-r.Context().Done():
				case <-release:
				}
			}))
			defer server.Close()
			defer close(release)
			c, _ := gin.CreateTestContext(httptest.NewRecorder())
			c.Request = httptest.NewRequest("POST", "/v1/responses", strings.NewReader(`{}`))
			req, err := http.NewRequest("POST", server.URL, strings.NewReader(`{}`))
			require.NoError(t, err)
			resp, err := doRequest(c, req, &relaycommon.RelayInfo{ChannelMeta: &relaycommon.ChannelMeta{}})
			if err == nil {
				defer resp.Body.Close()
				_, err = io.ReadAll(resp.Body)
			}
			require.Error(t, err)
			require.NoError(t, c.Request.Context().Err(), "another channel can still be attempted")
		})
	}
}

func TestTextFirstByteDisarmsAttemptTimer(t *testing.T) {
	firstByte := make(chan struct{})
	released := false
	body := &textRelayResponseBody{ReadCloser: io.NopCloser(strings.NewReader("data")), firstByte: func() { close(firstByte) }, release: func() { released = true }}
	buffer := make([]byte, 1)
	_, err := body.Read(buffer)
	require.NoError(t, err)
	select {
	case <-firstByte:
	default:
		t.Fatal("first body byte did not disarm attempt timer")
	}
	_, err = io.ReadAll(body)
	require.NoError(t, err)
	require.False(t, released, "stream remains alive after first byte")
	require.NoError(t, body.Close())
	require.True(t, released)
}

func TestImageRequestKeepsExistingCancellationBehavior(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { _, _ = w.Write([]byte(`{}`)) }))
	defer server.Close()
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	c, _ := gin.CreateTestContext(httptest.NewRecorder())
	c.Request = httptest.NewRequest("POST", "/v1/images/generations", strings.NewReader(`{}`)).WithContext(ctx)
	req, err := http.NewRequest("POST", server.URL, strings.NewReader(`{}`))
	require.NoError(t, err)
	resp, err := doRequest(c, req, &relaycommon.RelayInfo{ChannelMeta: &relaycommon.ChannelMeta{}})
	require.NoError(t, err)
	require.NoError(t, resp.Body.Close())
}
