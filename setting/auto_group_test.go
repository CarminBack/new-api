package setting

import (
	"fmt"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestAutoGroupsPreservesLegacyEmptyAndRejectsMalformedJSON(t *testing.T) {
	original := AutoGroups2JsonString()
	t.Cleanup(func() { require.NoError(t, UpdateAutoGroupsByJsonString(original)) })
	require.NoError(t, UpdateAutoGroupsByJsonString(`["vip"]`))
	require.Error(t, UpdateAutoGroupsByJsonString(`broken`))
	assert.Equal(t, []string{"vip"}, GetAutoGroups())
	for _, value := range []string{"", "  ", "[]"} {
		require.NoError(t, UpdateAutoGroupsByJsonString(value))
		assert.Empty(t, GetAutoGroups())
	}
}

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
