package helper

import (
	"encoding/json"
	"net/http/httptest"
	"testing"

	"github.com/QuantumNous/new-api/common"
	relaycommon "github.com/QuantumNous/new-api/relay/common"
	"github.com/QuantumNous/new-api/relaykit/dto"
	"github.com/QuantumNous/new-api/relaykit/types"
	"github.com/QuantumNous/new-api/setting/ratio_setting"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestImageGroupResolutionRatioUsesModelPrice(t *testing.T) {
	gin.SetMode(gin.TestMode)
	savedPrices := ratio_setting.ModelPrice2JSONString()
	savedGroups := ratio_setting.GroupRatio2JSONString()
	savedResolutionRatios := ratio_setting.ImageGroupResolutionRatio2JSONString()
	t.Cleanup(func() {
		require.NoError(t, ratio_setting.UpdateModelPriceByJSONString(savedPrices))
		require.NoError(t, ratio_setting.UpdateGroupRatioByJSONString(savedGroups))
		require.NoError(t, ratio_setting.UpdateImageGroupResolutionRatioByJSONString(savedResolutionRatios))
	})

	require.NoError(t, ratio_setting.UpdateModelPriceByJSONString(`{"priced-image":0.1}`))
	require.NoError(t, ratio_setting.UpdateGroupRatioByJSONString(`{"Image":1,"default":1}`))
	require.NoError(t, ratio_setting.UpdateImageGroupResolutionRatioByJSONString(`{"1k":1,"2k":1.6,"4k":2}`))

	count := uint(2)
	tests := []struct {
		name      string
		group     string
		request   dto.Request
		wantQuota int
		wantRatio bool
	}{
		{
			name:      "Image group applies symbolic 2k multiplier and image count",
			group:     "Image",
			request:   &dto.ImageRequest{Model: "priced-image", Size: "2K", N: &count},
			wantQuota: 160000,
			wantRatio: true,
		},
		{
			name:  "Gemini native image config applies 2k multiplier",
			group: "Image",
			request: &dto.GeminiChatRequest{GenerationConfig: dto.GeminiChatGenerationConfig{
				ImageConfig: json.RawMessage(`{"imageSize":"2K"}`),
			}},
			wantQuota: 80000,
			wantRatio: true,
		},
		{
			name:  "OpenAI compatible Gemini image config applies 2k multiplier",
			group: "Image",
			request: &dto.GeneralOpenAIRequest{
				ExtraBody: json.RawMessage(`{"google":{"image_config":{"image_size":"2K"}}}`),
			},
			wantQuota: 80000,
			wantRatio: true,
		},
		{
			name:      "unknown dimensions use highest multiplier",
			group:     "Image",
			request:   &dto.ImageRequest{Model: "priced-image", Size: "8192x8192", N: &count},
			wantQuota: 200000,
			wantRatio: true,
		},
		{
			name:      "other group keeps model price",
			group:     "default",
			request:   &dto.ImageRequest{Model: "priced-image", Size: "2048x1152", N: &count},
			wantQuota: 100000,
		},
		{
			name:      "text request with size in Image group is unchanged",
			group:     "Image",
			request:   &dto.GeneralOpenAIRequest{Size: "2K"},
			wantQuota: 50000,
		},
		{
			name:      "non image request in Image group is unchanged",
			group:     "Image",
			request:   nil,
			wantQuota: 50000,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ctx, _ := gin.CreateTestContext(httptest.NewRecorder())
			info := &relaycommon.RelayInfo{
				OriginModelName: "priced-image",
				UserGroup:       tt.group,
				UsingGroup:      tt.group,
				Request:         tt.request,
			}
			meta := &types.TokenCountMeta{}
			if request, ok := tt.request.(*dto.ImageRequest); ok {
				meta = request.GetTokenCountMeta()
			}

			price, err := ModelPriceHelper(ctx, info, 0, meta)

			require.NoError(t, err)
			assert.Equal(t, tt.wantQuota, price.QuotaToPreConsume)
			assert.Equal(t, tt.wantRatio, price.HasOtherRatio("image_resolution"))
			assert.Equal(t, float64(tt.wantQuota), price.ApplyOtherRatiosToFloat(price.ModelPrice*common.QuotaPerUnit*price.GroupRatioInfo.GroupRatio))
		})
	}
}
