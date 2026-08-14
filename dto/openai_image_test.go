package dto_test

import (
	"testing"

	"github.com/QuantumNous/new-api/relaykit/dto"
	"github.com/stretchr/testify/require"
)

func TestImageRequestBuiltInUnitPrice(t *testing.T) {
	tests := []struct {
		name    string
		request dto.ImageRequest
		want    float64
	}{
		{
			name: "gpt-image-2 medium 2k square",
			request: dto.ImageRequest{
				Model:   "gpt-image-2",
				Size:    "2048x2048",
				Quality: "medium",
			},
			want: 0.10704,
		},
		{
			name: "gpt-image-2 high 4k landscape",
			request: dto.ImageRequest{
				Model:   "gpt-image-2",
				Size:    "3840x2160",
				Quality: "high",
			},
			want: 0.40026,
		},
		{
			name: "banana 2 4k",
			request: dto.ImageRequest{
				Model: "gemini-3.1-flash-image",
				Size:  "4096x4096",
			},
			want: 0.151,
		},
		{
			name: "banana pro 2k",
			request: dto.ImageRequest{
				Model: "gemini-3-pro-image",
				Size:  "2048x2048",
			},
			want: 0.134,
		},
		{
			name: "empty size defaults to 1k",
			request: dto.ImageRequest{
				Model: "gemini-3.1-flash-image",
			},
			want: 0.067,
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			meta := test.request.GetTokenCountMeta()
			require.InDelta(t, test.want, meta.ImageUnitPrice, 0.000001)
		})
	}
}

func TestImageRequestUnknownBuiltInPriceKeepsLegacyImageRatio(t *testing.T) {
	req := dto.ImageRequest{
		Model:   "dall-e-3",
		Size:    "1024x1792",
		Quality: "hd",
	}

	meta := req.GetTokenCountMeta()

	require.Zero(t, meta.ImageUnitPrice)
	require.Equal(t, 3.0, meta.ImagePriceRatio)
}

func TestImageSizeTier(t *testing.T) {
	tests := []struct {
		name     string
		size     string
		wantTier string
		wantOK   bool
	}{
		{name: "auto defaults to 1k", size: "auto", wantTier: "1k", wantOK: true},
		{name: "empty defaults to 1k", wantTier: "1k", wantOK: true},
		{name: "1k square", size: "1024x1024", wantTier: "1k", wantOK: true},
		{name: "portrait reaches 2k", size: "1024x1536", wantTier: "2k", wantOK: true},
		{name: "2k canvas landscape", size: "2048x1152", wantTier: "2k", wantOK: true},
		{name: "4k canvas landscape", size: "3840x2160", wantTier: "4k", wantOK: true},
		{name: "4k canvas portrait", size: "2160x3840", wantTier: "4k", wantOK: true},
		{name: "invalid", size: "large", wantOK: false},
		{name: "above 4k", size: "4097x4096", wantOK: false},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			tier, ok := dto.ImageSizeTier(test.size)
			require.Equal(t, test.wantOK, ok)
			require.Equal(t, test.wantTier, tier)
		})
	}
}
