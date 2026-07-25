package setting

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestUpdateAutoGroupsByJsonString(t *testing.T) {
	original := append([]string(nil), autoGroups...)
	t.Cleanup(func() {
		autoGroups = original
	})

	require.NoError(t, UpdateAutoGroupsByJsonString(`["default","pro"]`))
	require.Equal(t, []string{"default", "pro"}, GetAutoGroups())

	require.Error(t, UpdateAutoGroupsByJsonString(`{"invalid":true}`))
	require.Equal(t, []string{"default", "pro"}, GetAutoGroups(), "invalid JSON must preserve the last valid configuration")

	require.NoError(t, UpdateAutoGroupsByJsonString("  "))
	require.Empty(t, GetAutoGroups(), "a blank persisted value represents an empty list")
}
