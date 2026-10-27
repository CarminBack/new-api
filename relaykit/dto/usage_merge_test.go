package dto

import (
	"testing"

	kitutil "github.com/QuantumNous/new-api/relaykit/relayconvert/kitutil"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestMergeClaudeUsageCacheCreationReplacesWholeObject(t *testing.T) {
	t.Parallel()

	merged := MergeClaudeUsageNonZero(
		&ClaudeUsage{
			CacheCreation: &ClaudeCacheCreationUsage{Ephemeral1hInputTokens: 1000},
		},
		&ClaudeUsage{
			CacheCreation: &ClaudeCacheCreationUsage{
				Ephemeral5mInputTokens: 1000,
				Ephemeral1hInputTokens: 0,
			},
		},
	)

	require.NotNil(t, merged.CacheCreation)
	assert.Equal(t, 1000, merged.CacheCreation.Ephemeral5mInputTokens)
	assert.Equal(t, 0, merged.CacheCreation.Ephemeral1hInputTokens)
}

func TestMergeGeminiUsageMetadataCandidatesAndThoughtsReplacedAsPair(t *testing.T) {
	t.Parallel()

	merged := MergeGeminiUsageMetadataNonZero(
		&GeminiUsageMetadata{
			PromptTokenCount:   10,
			ThoughtsTokenCount: 100,
		},
		&GeminiUsageMetadata{
			PromptTokenCount:     10,
			CandidatesTokenCount: 150,
			ThoughtsTokenCount:   0,
			TotalTokenCount:      160,
		},
	)
	require.NotNil(t, merged)
	assert.Equal(t, 150, merged.CandidatesTokenCount)
	assert.Equal(t, 0, merged.ThoughtsTokenCount)

	billing := NewGeminiChatBillingUsage(merged)
	usage, ok := billing.CanonicalUsage()
	require.True(t, ok)
	assert.Equal(t, 150, usage.CompletionTokens)
}

func TestGeminiModalityKeysSettleConsistentlyAndDuplicateEntriesSum(t *testing.T) {
	t.Parallel()

	for _, modality := range []string{"audio", " AUDIO ", "AUDIO"} {
		t.Run("settle_"+modality, func(t *testing.T) {
			t.Parallel()
			billing := NewGeminiChatBillingUsage(&GeminiUsageMetadata{
				PromptTokenCount: 100,
				PromptTokensDetails: []GeminiPromptTokensDetails{
					{Modality: modality, TokenCount: 40},
					{Modality: "TEXT", TokenCount: 60},
				},
			})
			usage, ok := billing.CanonicalUsage()
			require.True(t, ok)
			assert.Equal(t, 40, usage.PromptTokensDetails.AudioTokens)
			assert.Equal(t, 60, usage.PromptTokensDetails.TextTokens)
		})
	}

	mergedDetails := mergeGeminiTokenDetails(
		[]GeminiPromptTokensDetails{{Modality: "AUDIO", TokenCount: 10}},
		[]GeminiPromptTokensDetails{{Modality: "audio", TokenCount: 15}},
	)
	require.Len(t, mergedDetails, 1)
	assert.Equal(t, 25, mergedDetails[0].TokenCount)

	streamMerged := MergeGeminiUsageMetadataNonZero(
		&GeminiUsageMetadata{
			PromptTokenCount:    10,
			PromptTokensDetails: []GeminiPromptTokensDetails{{Modality: "AUDIO", TokenCount: 10}},
		},
		&GeminiUsageMetadata{
			PromptTokenCount:    25,
			PromptTokensDetails: []GeminiPromptTokensDetails{{Modality: "audio", TokenCount: 15}},
		},
	)
	require.NotNil(t, streamMerged)
	streamUsage, ok := NewGeminiChatBillingUsage(streamMerged).CanonicalUsage()
	require.True(t, ok)

	decodedUsage, ok := NewGeminiChatBillingUsage(&GeminiUsageMetadata{
		PromptTokenCount: 25,
		PromptTokensDetails: []GeminiPromptTokensDetails{
			{Modality: "AUDIO", TokenCount: 10},
			{Modality: "audio", TokenCount: 15},
		},
	}).CanonicalUsage()
	require.True(t, ok)
	assert.Equal(t, decodedUsage.PromptTokensDetails.AudioTokens, streamUsage.PromptTokensDetails.AudioTokens)
	assert.Equal(t, 25, decodedUsage.PromptTokensDetails.AudioTokens)
}

func TestClaudeCacheCreationSubObjectZeroDoesNotReviveFlatLegacyFields(t *testing.T) {
	t.Parallel()

	merged := MergeClaudeUsageNonZero(
		&ClaudeUsage{ClaudeCacheCreation1hTokens: 1000},
		&ClaudeUsage{
			InputTokens: 10,
			CacheCreation: &ClaudeCacheCreationUsage{
				Ephemeral5mInputTokens: 1000,
				Ephemeral1hInputTokens: 0,
			},
		},
	)
	require.NotNil(t, merged.CacheCreation)
	assert.Equal(t, 1000, merged.CacheCreation.Ephemeral5mInputTokens)
	assert.Equal(t, 0, merged.CacheCreation.Ephemeral1hInputTokens)
	assert.Equal(t, 1000, merged.ClaudeCacheCreation5mTokens)
	assert.Equal(t, 0, merged.ClaudeCacheCreation1hTokens)

	usage, ok := NewClaudeMessagesBillingUsage(merged).CanonicalUsage()
	require.True(t, ok)
	assert.Equal(t, 1000, usage.ClaudeCacheCreation5mTokens)
	assert.Equal(t, 0, usage.ClaudeCacheCreation1hTokens)
}

func TestClaudeCacheCreationFlatFieldsStillSettleWhenSnapshotNeverHadSubObject(t *testing.T) {
	t.Parallel()

	usage, ok := NewClaudeMessagesBillingUsage(&ClaudeUsage{
		InputTokens:                 10,
		ClaudeCacheCreation1hTokens: 1000,
	}).CanonicalUsage()
	require.True(t, ok)
	assert.Equal(t, 0, usage.ClaudeCacheCreation5mTokens)
	assert.Equal(t, 1000, usage.ClaudeCacheCreation1hTokens)
}

func TestMergeBillingUsageORsEstimatedOnSameAndCrossDialect(t *testing.T) {
	t.Parallel()

	estimated := NewEstimatedGeminiChatBillingUsage(&Usage{PromptTokens: 10, CompletionTokens: 2})
	require.NotNil(t, estimated)
	require.True(t, estimated.Estimated)

	sameDialect := MergeBillingUsageNonZero(estimated, NewGeminiChatBillingUsage(&GeminiUsageMetadata{
		PromptTokenCount:     11,
		CandidatesTokenCount: 3,
		TotalTokenCount:      14,
	}))
	require.NotNil(t, sameDialect)
	assert.True(t, sameDialect.Estimated)

	crossDialect := MergeBillingUsageNonZero(estimated, NewOpenAIChatBillingUsage(&Usage{
		PromptTokens:     12,
		CompletionTokens: 4,
		TotalTokens:      16,
	}))
	require.NotNil(t, crossDialect)
	assert.True(t, crossDialect.Estimated)
	require.NotNil(t, crossDialect.OpenAIUsage)
	assert.Equal(t, 12, crossDialect.OpenAIUsage.PromptTokens)
}

func TestMergeClaudeUsageNonZeroPreservesBillingUsage(t *testing.T) {
	t.Parallel()

	currentSidecar := NewGeminiChatBillingUsage(&GeminiUsageMetadata{
		PromptTokenCount:        3868,
		TotalTokenCount:         3868,
		CachedContentTokenCount: 20,
	})
	incomingSidecar := NewGeminiChatBillingUsage(&GeminiUsageMetadata{
		PromptTokenCount:     3868,
		CandidatesTokenCount: 12,
		TotalTokenCount:      3880,
	})
	require.NotNil(t, currentSidecar)
	require.NotNil(t, incomingSidecar)

	withIncoming := MergeClaudeUsageNonZero(
		&ClaudeUsage{
			InputTokens:          3868,
			CacheReadInputTokens: 20,
			BillingUsage:         currentSidecar,
		},
		&ClaudeUsage{
			InputTokens:  3868,
			OutputTokens: 12,
			BillingUsage: incomingSidecar,
		},
	)
	require.NotNil(t, withIncoming.BillingUsage)
	assert.Equal(t, BillingUsageSourceGeminiChat, withIncoming.BillingUsage.Source)
	assert.Equal(t, BillingUsageSemanticGemini, withIncoming.BillingUsage.Semantic)
	require.NotNil(t, withIncoming.BillingUsage.GeminiUsageMetadata)
	assert.Equal(t, 12, withIncoming.BillingUsage.GeminiUsageMetadata.CandidatesTokenCount)
	assert.Equal(t, 20, withIncoming.CacheReadInputTokens)
	assert.NotSame(t, incomingSidecar, withIncoming.BillingUsage)

	keepCurrent := MergeClaudeUsageNonZero(
		&ClaudeUsage{
			InputTokens:          3868,
			CacheReadInputTokens: 20,
			BillingUsage:         currentSidecar,
		},
		&ClaudeUsage{InputTokens: 3868, OutputTokens: 12},
	)
	require.NotNil(t, keepCurrent.BillingUsage)
	require.NotNil(t, keepCurrent.BillingUsage.GeminiUsageMetadata)
	assert.Equal(t, 20, keepCurrent.BillingUsage.GeminiUsageMetadata.CachedContentTokenCount)
	assert.Equal(t, 0, keepCurrent.BillingUsage.GeminiUsageMetadata.CandidatesTokenCount)
	assert.Equal(t, 20, keepCurrent.CacheReadInputTokens)
}

func TestMergeUsageNonZeroKeepsPositiveValuesAndTakesMaxTotal(t *testing.T) {
	t.Parallel()

	merged := MergeUsageNonZero(
		&Usage{PromptTokens: 10, CompletionTokens: 5, TotalTokens: 15},
		&Usage{PromptTokens: 0, CompletionTokens: 0, TotalTokens: 20},
	)

	require.NotNil(t, merged)
	assert.Equal(t, 10, merged.PromptTokens)
	assert.Equal(t, 5, merged.CompletionTokens)
	assert.Equal(t, 20, merged.TotalTokens)
}

func TestInputTokenDetailsCacheCreationInputTokens(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name  string
		raw   string
		total int
	}{
		{
			name:  "scalar only",
			raw:   `{"cached_tokens":1200,"cache_creation_input_tokens":300}`,
			total: 300,
		},
		{
			name:  "scalar larger than cache write",
			raw:   `{"cache_write_tokens":250,"cache_creation_input_tokens":400}`,
			total: 400,
		},
		{
			name:  "cache write larger than scalar",
			raw:   `{"cache_write_tokens":250,"cache_creation_input_tokens":100}`,
			total: 250,
		},
		{
			name:  "scalar larger than cached creation",
			raw:   `{"cached_creation_tokens":300,"cache_creation_input_tokens":400}`,
			total: 400,
		},
		{
			name:  "negative scalar ignored beside cache write",
			raw:   `{"cache_write_tokens":80,"cache_creation_input_tokens":-1}`,
			total: 80,
		},
		{
			name:  "only negative scalar",
			raw:   `{"cache_creation_input_tokens":-5}`,
			total: 0,
		},
		{
			name:  "undocumented creation object ignored",
			raw:   `{"cached_tokens":1200,"cache_creation":{"ephemeral_5m_input_tokens":300}}`,
			total: 0,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			var details InputTokenDetails
			require.NoError(t, kitutil.Unmarshal([]byte(tc.raw), &details))
			assert.Equal(t, tc.total, details.CacheCreationTokensTotal())
		})
	}

	var withCreation Usage
	require.NoError(t, kitutil.Unmarshal([]byte(`{"prompt_tokens":1510,"completion_tokens":20,"total_tokens":1530,"prompt_tokens_details":{"cached_tokens":1200,"cache_creation_input_tokens":300,"cache_creation":{"ephemeral_5m_input_tokens":300}}}`), &withCreation))
	assert.Equal(t, 1510, withCreation.PromptTokens)
	assert.Equal(t, 1200, withCreation.PromptTokensDetails.CachedTokens)
	assert.Equal(t, 300, withCreation.PromptTokensDetails.CacheCreationInputTokens)
	assert.Equal(t, 300, withCreation.PromptTokensDetails.CacheCreationTokensTotal())
	assert.Zero(t, withCreation.PromptTokensDetails.CachedCreationTokens)
	assert.Zero(t, withCreation.PromptTokensDetails.CacheWriteTokens)
	assert.Zero(t, withCreation.ClaudeCacheCreation5mTokens)
	assert.Zero(t, withCreation.ClaudeCacheCreation1hTokens)

	encoded, err := kitutil.Marshal(withCreation)
	require.NoError(t, err)
	body := string(encoded)
	assert.Contains(t, body, `"cache_creation_input_tokens":300`)
	assert.NotContains(t, body, `"cache_creation":`)
	assert.NotContains(t, body, "ephemeral_5m_input_tokens")
	assert.NotContains(t, body, "cache_write_tokens")
	assert.NotContains(t, body, "cached_creation_tokens")
	assert.Contains(t, body, `"cached_tokens":1200`)
	assert.Contains(t, body, `"prompt_tokens":1510`)

	var withoutCreation Usage
	require.NoError(t, kitutil.Unmarshal([]byte(`{"prompt_tokens":1510,"completion_tokens":20,"prompt_tokens_details":{"cached_tokens":1200}}`), &withoutCreation))
	encoded, err = kitutil.Marshal(withoutCreation)
	require.NoError(t, err)
	absent := string(encoded)
	assert.NotContains(t, absent, "cache_creation_input_tokens")
	assert.NotContains(t, absent, `"cache_creation":`)
	assert.NotContains(t, absent, "ephemeral_5m_input_tokens")
	assert.NotContains(t, absent, "cache_write_tokens")
	assert.NotContains(t, absent, "cached_creation_tokens")
}

func TestCacheCreationInputTokensRoundTrip(t *testing.T) {
	t.Parallel()

	var usage Usage
	require.NoError(t, kitutil.Unmarshal([]byte(`{"prompt_tokens":1510,"prompt_tokens_details":{"cached_tokens":1200,"cache_creation_input_tokens":300}}`), &usage))
	encoded, err := kitutil.Marshal(usage)
	require.NoError(t, err)
	var again Usage
	require.NoError(t, kitutil.Unmarshal(encoded, &again))
	assert.Equal(t, 300, again.PromptTokensDetails.CacheCreationInputTokens)
	assert.Equal(t, 300, again.PromptTokensDetails.CacheCreationTokensTotal())
	assert.Zero(t, again.ClaudeCacheCreation5mTokens)
	assert.Zero(t, again.ClaudeCacheCreation1hTokens)
}

func TestMergeUsageNonZeroCacheCreationInputTokensReplacesWithoutAdding(t *testing.T) {
	t.Parallel()

	first := &Usage{}
	require.NoError(t, kitutil.Unmarshal([]byte(`{"prompt_tokens":100,"prompt_tokens_details":{"cached_tokens":10,"cache_creation_input_tokens":100}}`), first))
	second := &Usage{}
	require.NoError(t, kitutil.Unmarshal([]byte(`{"prompt_tokens":200,"prompt_tokens_details":{"cached_tokens":20,"cache_creation_input_tokens":300}}`), second))
	merged := MergeUsageNonZero(first, second)
	require.NotNil(t, merged)
	assert.Equal(t, 300, merged.PromptTokensDetails.CacheCreationTokensTotal())
	assert.Equal(t, 20, merged.PromptTokensDetails.CachedTokens)
	assert.Zero(t, merged.ClaudeCacheCreation5mTokens)
	assert.Zero(t, merged.ClaudeCacheCreation1hTokens)

	kept := MergeUsageNonZero(merged, &Usage{})
	assert.Equal(t, 300, kept.PromptTokensDetails.CacheCreationTokensTotal())

	write := &Usage{}
	require.NoError(t, kitutil.Unmarshal([]byte(`{"prompt_tokens_details":{"cache_write_tokens":250}}`), write))
	scalar := &Usage{}
	require.NoError(t, kitutil.Unmarshal([]byte(`{"prompt_tokens_details":{"cache_creation_input_tokens":100}}`), scalar))
	both := MergeUsageNonZero(write, scalar)
	assert.Equal(t, 250, both.PromptTokensDetails.CacheWriteTokens)
	assert.Equal(t, 250, both.PromptTokensDetails.CacheCreationTokensTotal())

	responses := &Usage{}
	require.NoError(t, kitutil.Unmarshal([]byte(`{"input_tokens_details":{"cache_creation_input_tokens":300}}`), responses))
	overlaid := MergeUsageNonZero(&Usage{}, responses)
	require.NotNil(t, overlaid.InputTokensDetails)
	assert.Equal(t, 300, overlaid.InputTokensDetails.CacheCreationTokensTotal())
	assert.Zero(t, overlaid.PromptTokensDetails.CacheCreationTokensTotal())
}
