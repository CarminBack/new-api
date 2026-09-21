package helper

import (
	"testing"

	relayconstant "github.com/QuantumNous/new-api/relay/constant"
	"github.com/stretchr/testify/require"
)

func TestValidateImageModelRequest(t *testing.T) {
	tests := []struct {
		name      string
		model     string
		relayMode int
		body      string
		wantErr   string
	}{
		{
			name:      "gpt image generation allowed",
			model:     "gpt-image-2.5",
			relayMode: relayconstant.RelayModeImagesGenerations,
			body:      `{"model":"gpt-image-2.5","size":"1024x1024"}`,
		},
		{
			name:      "gpt image edit allowed",
			model:     "gpt-image-2-exact",
			relayMode: relayconstant.RelayModeImagesEdits,
		},
		{
			name:      "gpt image chat rejected",
			model:     "gpt-image-2.5-flare",
			relayMode: relayconstant.RelayModeChatCompletions,
			body:      `{"model":"gpt-image-2.5-flare","messages":[]}`,
			wantErr:   "/v1/images/generations",
		},
		{
			name:      "gpt image responses rejected",
			model:     "gpt-image-2",
			relayMode: relayconstant.RelayModeResponses,
			wantErr:   "/v1/images/generations",
		},
		{
			name:      "gemini openai image request allowed",
			model:     "gemini-3-pro-image-preview",
			relayMode: relayconstant.RelayModeChatCompletions,
			body:      `{"modalities":["text","image"],"extra_body":{"google":{"image_config":{"image_size":"2K"}}}}`,
		},
		{
			name:      "gemini native image request allowed",
			model:     "gemini-3.1-flash-image",
			relayMode: relayconstant.RelayModeGemini,
			body:      `{"generationConfig":{"responseModalities":["IMAGE"],"imageConfig":{"imageSize":"4K"}}}`,
		},
		{
			name:      "gemini pixel resolution allowed",
			model:     "gemini-3-pro-image",
			relayMode: relayconstant.RelayModeChatCompletions,
			body:      `{"modalities":["image"],"size":"1536x1024"}`,
		},
		{
			name:      "gemini missing image modality rejected",
			model:     "gemini-3.1-flash-image-preview",
			relayMode: relayconstant.RelayModeChatCompletions,
			body:      `{"size":"1024x1024"}`,
			wantErr:   "image output modality",
		},
		{
			name:      "gemini missing resolution rejected",
			model:     "gemini-3.1-flash-image-preview",
			relayMode: relayconstant.RelayModeGemini,
			body:      `{"generationConfig":{"responseModalities":["IMAGE"]}}`,
			wantErr:   "1K, 2K, or 4K",
		},
		{
			name:      "gemini auto resolution rejected",
			model:     "gemini-3.1-flash-image",
			relayMode: relayconstant.RelayModeChatCompletions,
			body:      `{"modalities":["image"],"size":"auto"}`,
			wantErr:   "1K, 2K, or 4K",
		},
		{
			name:      "ordinary model unaffected",
			model:     "gemini-3.1-pro-preview",
			relayMode: relayconstant.RelayModeChatCompletions,
			body:      `{}`,
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			err := ValidateImageModelRequest(test.model, test.relayMode, []byte(test.body))
			if test.wantErr == "" {
				require.NoError(t, err)
				return
			}
			require.ErrorContains(t, err, test.wantErr)
		})
	}
}

func TestIsGovernedImageModel(t *testing.T) {
	require.True(t, IsGovernedImageModel("gpt-image-2.5-sunburst"))
	require.True(t, IsGovernedImageModel("gemini-3-pro-image"))
	require.False(t, IsGovernedImageModel("gpt-image-1"))
	require.False(t, IsGovernedImageModel("gpt-image-20"))
	require.False(t, IsGovernedImageModel("gemini-3.1-pro-preview"))
}
