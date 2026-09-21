package dto

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestImageSizeTier(t *testing.T) {
	tests := []struct {
		name string
		size string
		want string
		ok   bool
	}{
		{name: "empty defaults to 1k", want: "1k", ok: true},
		{name: "auto defaults to 1k", size: "auto", want: "1k", ok: true},
		{name: "1k square", size: "1024x1024", want: "1k", ok: true},
		{name: "2k canvas", size: "2048x1152", want: "2k", ok: true},
		{name: "4k canvas", size: "3840x2160", want: "4k", ok: true},
		{name: "oversized", size: "4097x2160"},
		{name: "invalid", size: "4K"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			got, ok := ImageSizeTier(test.size)
			assert.Equal(t, test.ok, ok)
			assert.Equal(t, test.want, got)
		})
	}
}

func TestChannelSettingsImageResolutionTierSupportAndValidation(t *testing.T) {
	settings := ChannelSettings{ImageResolutionTiers: map[string][]string{
		" GPT-IMAGE-2 ": {" 1K ", "2k"},
	}}

	supported, declared := settings.ImageResolutionTierSupport("gpt-image-2", "1k")
	assert.True(t, supported)
	assert.True(t, declared)
	supported, declared = settings.ImageResolutionTierSupport("gpt-image-2", "4K")
	assert.False(t, supported)
	assert.True(t, declared)
	_, declared = settings.ImageResolutionTierSupport("other-model", "1k")
	assert.False(t, declared)
	require.NoError(t, settings.ValidateImageResolutionTiers())

	invalid := []ChannelSettings{
		{ImageResolutionTiers: map[string][]string{" ": {"1k"}}},
		{ImageResolutionTiers: map[string][]string{"gpt-image-2": {}}},
		{ImageResolutionTiers: map[string][]string{"gpt-image-2": {"8k"}}},
		{ImageResolutionTiers: map[string][]string{"gpt-image-2": {"1k", " 1K "}}},
		{ImageResolutionTiers: map[string][]string{"gpt-image-2": {"1k"}, " GPT-IMAGE-2 ": {"2k"}}},
	}
	for _, settings := range invalid {
		require.Error(t, settings.ValidateImageResolutionTiers())
	}
}
