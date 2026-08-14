package controller

import (
	"net/http/httptest"
	"testing"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/setting"
	"github.com/QuantumNous/new-api/setting/ratio_setting"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

func TestGetUserGroupsReturnsConfiguredDisplayOrder(t *testing.T) {
	setupModelListControllerTestDB(t)

	originalGroupRatio := ratio_setting.GroupRatio2JSONString()
	originalUsableGroups := setting.UserUsableGroups2JSONString()
	originalDisplayOrder := setting.GroupDisplayOrder2JSONString()
	t.Cleanup(func() {
		require.NoError(t, ratio_setting.UpdateGroupRatioByJSONString(originalGroupRatio))
		require.NoError(t, setting.UpdateUserUsableGroupsByJSONString(originalUsableGroups))
		require.NoError(t, setting.UpdateGroupDisplayOrderByJSONString(originalDisplayOrder))
	})

	require.NoError(t, ratio_setting.UpdateGroupRatioByJSONString(`{"default":1,"vip":0.8,"hidden":1}`))
	require.NoError(t, setting.UpdateUserUsableGroupsByJSONString(`{"default":"Default","vip":"VIP"}`))
	require.NoError(t, setting.UpdateGroupDisplayOrderByJSONString(`["vip","default","hidden"]`))

	recorder := httptest.NewRecorder()
	ctx, _ := gin.CreateTestContext(recorder)
	ctx.Set("id", 0)
	GetUserGroups(ctx)

	var response struct {
		Success    bool                              `json:"success"`
		Data       map[string]map[string]interface{} `json:"data"`
		GroupOrder []string                          `json:"group_order"`
	}
	require.NoError(t, common.DecodeJson(recorder.Body, &response))
	require.True(t, response.Success)
	require.Len(t, response.Data, 2)
	require.Equal(t, []string{"vip", "default"}, response.GroupOrder)
}
