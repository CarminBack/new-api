package controller

import (
	"testing"

	"github.com/QuantumNous/new-api/setting/operation_setting"
	"github.com/stretchr/testify/require"
)

func TestGetPayMoneyUsesPaymentMethodQuotaRatio(t *testing.T) {
	originalPrice := operation_setting.Price
	originalMethods := operation_setting.PayMethods
	t.Cleanup(func() {
		operation_setting.Price = originalPrice
		operation_setting.PayMethods = originalMethods
	})

	operation_setting.Price = 1
	operation_setting.PayMethods = []map[string]string{
		{"type": "alipay", "quota_ratio": "1"},
		{"type": "usdt", "quota_ratio": "6.77"},
	}

	require.InDelta(t, 1, getPayMoney(1, "default", "alipay"), 0.000001)
	require.InDelta(t, 1, getPayMoney(6.77, "default", "usdt"), 0.000001)
}

func TestGetPayMoneyFallsBackToLegacyPrice(t *testing.T) {
	originalPrice := operation_setting.Price
	originalMethods := operation_setting.PayMethods
	t.Cleanup(func() {
		operation_setting.Price = originalPrice
		operation_setting.PayMethods = originalMethods
	})

	operation_setting.Price = 7.3
	operation_setting.PayMethods = []map[string]string{{"type": "alipay"}}

	require.InDelta(t, 7.3, getPayMoney(1, "default", "alipay"), 0.000001)
}
