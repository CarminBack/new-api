package setting

import (
	"fmt"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestUpdateMaxTokenAutoGroupsAcceptsAnyPositiveInteger(t *testing.T) {
	original := GetMaxTokenAutoGroups()
	t.Cleanup(func() {
		require.NoError(t, UpdateMaxTokenAutoGroups(fmt.Sprintf("%d", original)))
	})

	require.NoError(t, UpdateMaxTokenAutoGroups("123456"))
	assert.Equal(t, 123456, GetMaxTokenAutoGroups())
}

func TestUpdateMaxTokenAutoGroupsRejectsInvalidValuesWithoutChangingState(t *testing.T) {
	original := GetMaxTokenAutoGroups()
	for _, value := range []string{"", "0", "-1", "1.5", "not-a-number"} {
		t.Run(value, func(t *testing.T) {
			assert.Error(t, UpdateMaxTokenAutoGroups(value))
			assert.Equal(t, original, GetMaxTokenAutoGroups())
		})
	}
}

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
