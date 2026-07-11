package ratio_setting

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestImageGroupPriceUpdate(t *testing.T) {
	original := ImageGroupPrice2JSONString()
	t.Cleanup(func() {
		require.NoError(t, UpdateImageGroupPriceByJSONString(original))
	})

	require.NoError(t, UpdateImageGroupPriceByJSONString(`{"1k":0.11,"2k":0.15,"4k":0.21}`))

	price, ok := GetImageGroupPrice("2K")
	require.True(t, ok)
	require.Equal(t, 0.15, price)
}

func TestImageGroupPriceRejectsInvalidSettings(t *testing.T) {
	require.Error(t, CheckImageGroupPrice(`{"1k":0.1,"2k":0.14}`))
	require.Error(t, CheckImageGroupPrice(`{"1k":0.1,"2k":-0.14,"4k":0.2}`))
	require.Error(t, CheckImageGroupPrice(`{"1k":0.1,"2k":0.14,"4k":0.2,"8k":0.4}`))
}
