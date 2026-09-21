package relay

import (
	"strings"
	"testing"
	"unicode/utf8"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/relaykit/dto"
	"github.com/stretchr/testify/require"
)

func TestOpenAIImageArchiveRequestExtractsPromptAndNestedSize(t *testing.T) {
	extraBody, err := common.Marshal(map[string]any{
		"google": map[string]any{
			"image_config": map[string]any{"image_size": "2K"},
		},
	})
	require.NoError(t, err)
	request := &dto.GeneralOpenAIRequest{
		Messages: []dto.Message{
			{Role: "system", Content: "Follow the style guide"},
			{Role: "user", Content: []any{
				map[string]any{"type": "text", "text": "Draw a lighthouse"},
				map[string]any{"type": "image_url", "image_url": map[string]any{"url": "https://example.com/reference.png"}},
			}},
		},
		ExtraBody: extraBody,
	}
	archive := openAIImageArchiveRequest(request)
	require.Equal(t, "Draw a lighthouse", archive.Prompt)
	require.Equal(t, "2K", archive.Size)
	require.Equal(t, "standard", archive.Quality)
}

func TestImageArchiveRequestUsesLastUserPromptAndTruncates(t *testing.T) {
	longPrompt := strings.Repeat("画", imageArchivePromptMaxRunes+20)
	openAIArchive := openAIImageArchiveRequest(&dto.GeneralOpenAIRequest{Messages: []dto.Message{
		{Role: "user", Content: "old prompt"},
		{Role: "assistant", Content: "old answer"},
		{Role: "system", Content: "private system prompt"},
		{Role: "user", Content: longPrompt},
	}})
	require.Equal(t, imageArchivePromptMaxRunes, utf8.RuneCountInString(openAIArchive.Prompt))
	require.NotContains(t, openAIArchive.Prompt, "private system prompt")

	geminiArchive := geminiImageArchiveRequest(&dto.GeminiChatRequest{Contents: []dto.GeminiChatContent{
		{Role: "user", Parts: []dto.GeminiPart{{Text: "old prompt"}}},
		{Role: "model", Parts: []dto.GeminiPart{{Text: "old answer"}}},
		{Role: "user", Parts: []dto.GeminiPart{{Text: "new"}, {Text: "prompt"}}},
	}})
	require.Equal(t, "new\nprompt", geminiArchive.Prompt)
}

func TestOpenAIImageArchiveRequestPrefersTopLevelSize(t *testing.T) {
	extraBody, err := common.Marshal(map[string]any{
		"google": map[string]any{
			"image_config": map[string]any{"image_size": "4K"},
		},
	})
	require.NoError(t, err)
	archive := openAIImageArchiveRequest(&dto.GeneralOpenAIRequest{Size: "1K", ExtraBody: extraBody})
	require.Equal(t, "1K", archive.Size)
}

func TestGeminiImageArchiveRequestExtractsPromptAndImageSizeAliases(t *testing.T) {
	camelConfig, err := common.Marshal(map[string]any{"imageSize": "2K"})
	require.NoError(t, err)
	snakeConfig, err := common.Marshal(map[string]any{"image_size": "4K"})
	require.NoError(t, err)

	for name, config := range map[string][]byte{"camel": camelConfig, "snake": snakeConfig} {
		t.Run(name, func(t *testing.T) {
			archive := geminiImageArchiveRequest(&dto.GeminiChatRequest{
				Contents: []dto.GeminiChatContent{
					{Role: "user", Parts: []dto.GeminiPart{{Text: "Draw"}, {Text: "a city at night"}}},
				},
				GenerationConfig: dto.GeminiChatGenerationConfig{ImageConfig: config},
			})
			require.Equal(t, "Draw\na city at night", archive.Prompt)
			if name == "camel" {
				require.Equal(t, "2K", archive.Size)
			} else {
				require.Equal(t, "4K", archive.Size)
			}
			require.Equal(t, "standard", archive.Quality)
		})
	}
}
