package service

import (
	"net/http/httptest"
	"testing"

	relaycommon "github.com/QuantumNous/new-api/relay/common"
	"github.com/QuantumNous/new-api/relaykit/dto"
	"github.com/QuantumNous/new-api/setting/ratio_setting"
	hosttypes "github.com/QuantumNous/new-api/types"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestPrepareImageBillingRefreshesResolutionRatio(t *testing.T) {
	gin.SetMode(gin.TestMode)
	saved := ratio_setting.ImageGroupResolutionRatio2JSONString()
	t.Cleanup(func() {
		require.NoError(t, ratio_setting.UpdateImageGroupResolutionRatioByJSONString(saved))
	})
	require.NoError(t, ratio_setting.UpdateImageGroupResolutionRatioByJSONString(`{"1k":1,"2k":1.6,"4k":2}`))

	billing := &recordingBillingSettler{}
	info := &relaycommon.RelayInfo{
		UsingGroup: "Image",
		Request:    &dto.ImageRequest{Size: "1440x1920"},
		Billing:    billing,
		PriceData: hosttypes.PriceData{
			UsePrice:       true,
			ModelPrice:     0.2,
			GroupRatioInfo: hosttypes.GroupRatioInfo{GroupRatio: 1},
		},
	}
	ctx, _ := gin.CreateTestContext(httptest.NewRecorder())

	apiErr := PrepareImageBillingForRequest(ctx, info, 1)

	require.Nil(t, apiErr)
	assert.Equal(t, []int{160000}, billing.reserveTargets)
	assert.Equal(t, 160000, info.PriceData.QuotaToPreConsume)
	ratio, ok := info.PriceData.OtherRatios()["image_resolution"]
	assert.True(t, ok)
	assert.Equal(t, 1.6, ratio)
}

func TestPrepareImageBillingUsesResolvedOutboundResolution(t *testing.T) {
	gin.SetMode(gin.TestMode)
	saved := ratio_setting.ImageGroupResolutionRatio2JSONString()
	t.Cleanup(func() {
		require.NoError(t, ratio_setting.UpdateImageGroupResolutionRatioByJSONString(saved))
	})
	require.NoError(t, ratio_setting.UpdateImageGroupResolutionRatioByJSONString(`{"1k":1,"2k":1.6,"4k":2}`))

	billing := &recordingBillingSettler{}
	info := &relaycommon.RelayInfo{
		UsingGroup: "Image",
		Request:    &dto.ImageRequest{Size: "1K"},
		Billing:    billing,
		PriceData: hosttypes.PriceData{
			UsePrice:       true,
			ModelPrice:     0.2,
			GroupRatioInfo: hosttypes.GroupRatioInfo{GroupRatio: 1},
		},
	}
	ctx, _ := gin.CreateTestContext(httptest.NewRecorder())

	apiErr := PrepareImageBillingForResolvedRequest(ctx, info, 1, &dto.ImageRequest{Size: "4K"})

	require.Nil(t, apiErr)
	assert.Equal(t, []int{200000}, billing.reserveTargets)
	assert.Equal(t, 2.0, info.PriceData.OtherRatios()["image_resolution"])
}

func TestPrepareImageBillingClearsResolutionRatioOutsideImageGroup(t *testing.T) {
	gin.SetMode(gin.TestMode)
	billing := &recordingBillingSettler{}
	priceData := hosttypes.PriceData{
		UsePrice:       true,
		ModelPrice:     0.2,
		GroupRatioInfo: hosttypes.GroupRatioInfo{GroupRatio: 1},
	}
	priceData.AddOtherRatio("image_resolution", 1.6)
	info := &relaycommon.RelayInfo{
		UsingGroup: "default",
		Request:    &dto.ImageRequest{Size: "2K"},
		Billing:    billing,
		PriceData:  priceData,
	}
	ctx, _ := gin.CreateTestContext(httptest.NewRecorder())

	apiErr := PrepareImageBillingForRequest(ctx, info, 1)

	require.Nil(t, apiErr)
	assert.Equal(t, []int{100000}, billing.reserveTargets)
	assert.False(t, info.PriceData.HasOtherRatio("image_resolution"))
}
