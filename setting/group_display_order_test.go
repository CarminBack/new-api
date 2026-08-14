package setting

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestGroupDisplayOrder(t *testing.T) {
	original := GroupDisplayOrder2JSONString()
	t.Cleanup(func() {
		require.NoError(t, UpdateGroupDisplayOrderByJSONString(original))
	})

	require.NoError(t, UpdateGroupDisplayOrderByJSONString(`["vip","default","vip","missing",""]`))
	require.Equal(t, []string{"vip", "default", "missing"}, GetGroupDisplayOrder())
	require.Equal(
		t,
		[]string{"vip", "default", "alpha", "new"},
		OrderGroupNames([]string{"new", "default", "vip", "alpha"}),
	)

	require.Error(t, UpdateGroupDisplayOrderByJSONString(`{"invalid":true}`))
	require.Equal(t, []string{"vip", "default", "missing"}, GetGroupDisplayOrder())

	require.NoError(t, UpdateGroupDisplayOrderByJSONString("  "))
	require.Empty(t, GetGroupDisplayOrder())
}
