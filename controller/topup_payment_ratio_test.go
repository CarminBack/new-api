package controller

import (
	"math"
	"testing"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/setting/operation_setting"
	"github.com/stretchr/testify/require"
)

func TestGetPayMoneyUsesPaymentMethodQuotaRatio(t *testing.T) {
	originalPrice := operation_setting.Price
	originalMethods := operation_setting.PayMethods
	originalDisplayType := operation_setting.GetQuotaDisplayType()
	t.Cleanup(func() {
		operation_setting.Price = originalPrice
		operation_setting.PayMethods = originalMethods
		operation_setting.GetGeneralSetting().QuotaDisplayType = originalDisplayType
	})

	operation_setting.GetGeneralSetting().QuotaDisplayType = operation_setting.QuotaDisplayTypeUSD
	operation_setting.Price = 1
	operation_setting.PayMethods = []map[string]string{
		{"type": "alipay", "quota_ratio": "1"},
		{"type": "usdt", "quota_ratio": "6.77"},
	}

	require.InDelta(t, 1, getPayMoney(1, "default", "alipay"), 0.000001)
	require.InDelta(t, 1, getPayMoney(6.77, "default", "usdt"), 0.000001)
}

func TestGetPayMoneyFallsBackToLegacyPriceForInvalidRatio(t *testing.T) {
	originalPrice := operation_setting.Price
	originalMethods := operation_setting.PayMethods
	originalDisplayType := operation_setting.GetQuotaDisplayType()
	t.Cleanup(func() {
		operation_setting.Price = originalPrice
		operation_setting.PayMethods = originalMethods
		operation_setting.GetGeneralSetting().QuotaDisplayType = originalDisplayType
	})

	operation_setting.GetGeneralSetting().QuotaDisplayType = operation_setting.QuotaDisplayTypeUSD
	operation_setting.Price = 7.3
	for _, ratio := range []string{"", "0", "-1", "NaN", "+Inf"} {
		operation_setting.PayMethods = []map[string]string{{"type": "alipay", "quota_ratio": ratio}}
		require.InDelta(t, 7.3, getPayMoney(1, "default", "alipay"), 0.000001, ratio)
	}
}

func TestValidateTopUpQuotaSupportsFractionalAmountAndRejectsNonFinite(t *testing.T) {
	originalQuotaPerUnit := common.QuotaPerUnit
	originalDisplayType := operation_setting.GetQuotaDisplayType()
	t.Cleanup(func() {
		common.QuotaPerUnit = originalQuotaPerUnit
		operation_setting.GetGeneralSetting().QuotaDisplayType = originalDisplayType
	})
	common.QuotaPerUnit = 100
	operation_setting.GetGeneralSetting().QuotaDisplayType = operation_setting.QuotaDisplayTypeUSD

	quota, err := validateTopUpQuota(6.77)
	require.NoError(t, err)
	require.Equal(t, 677, quota)
	for _, amount := range []float64{0, -1, math.NaN(), math.Inf(1), math.Inf(-1)} {
		_, err := validateTopUpQuota(amount)
		require.Error(t, err)
	}
}
