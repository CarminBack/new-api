package ratio_setting

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestImageGroupResolutionRatioUpdate(t *testing.T) {
	original := ImageGroupResolutionRatio2JSONString()
	t.Cleanup(func() {
		require.NoError(t, UpdateImageGroupResolutionRatioByJSONString(original))
	})

	require.NoError(t, UpdateImageGroupResolutionRatioByJSONString(`{"1k":1,"2k":1.6,"4k":2}`))
	ratio, ok := GetImageGroupResolutionRatio(" 2K ")
	require.True(t, ok)
	assert.Equal(t, 1.6, ratio)
}

func TestImageGroupResolutionRatioRejectsInvalidSettings(t *testing.T) {
	tests := []string{
		`{"1k":1,"2k":1.6}`,
		`{"1k":1,"2k":0,"4k":2}`,
		`{"1k":1,"2k":1.6,"4k":2,"8k":4}`,
	}
	for _, value := range tests {
		require.Error(t, CheckImageGroupResolutionRatio(value))
	}
}
