package logger

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestUpstreamUsagePathsCoverProviderPayloads(t *testing.T) {
	tests := []struct {
		name    string
		payload string
		path    string
	}{
		{name: "responses", payload: `{"response":{"usage":{"input_tokens":12}}}`, path: "response.usage"},
		{name: "openai chat", payload: `{"usage":{"prompt_tokens":12}}`, path: "usage"},
		{name: "claude start", payload: `{"message":{"usage":{"input_tokens":12}}}`, path: "message.usage"},
		{name: "gemini", payload: `{"usageMetadata":{"promptTokenCount":12}}`, path: "usageMetadata"},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			entries := extractUpstreamUsage([]byte(test.payload))
			require.Len(t, entries, 1)
			assert.Equal(t, test.path, entries[0].path)
			assert.NotEmpty(t, entries[0].raw)
		})
	}
}

func TestExtractUpstreamUsageIgnoresResponseContent(t *testing.T) {
	payload := []byte(`{"response":{"output":[{"text":"private response"}]}}`)
	assert.Empty(t, extractUpstreamUsage(payload))
}
