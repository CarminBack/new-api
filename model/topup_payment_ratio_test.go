package model

import (
	"testing"

	"github.com/QuantumNous/new-api/common"
	"github.com/stretchr/testify/require"
)

func TestRechargeEpayCreditsExactQuotaAmountOnce(t *testing.T) {
	truncateTables(t)
	originalQuotaPerUnit := common.QuotaPerUnit
	common.QuotaPerUnit = 100
	t.Cleanup(func() { common.QuotaPerUnit = originalQuotaPerUnit })

	user := &User{Id: 801, Username: "payment-ratio-user", Status: common.UserStatusEnabled, Quota: 0}
	require.NoError(t, DB.Create(user).Error)
	order := &TopUp{
		UserId: user.Id, Amount: 7, QuotaAmount: 6.77, Money: 1,
		TradeNo: "payment-ratio-exact", PaymentMethod: "usdt", PaymentProvider: PaymentProviderEpay,
		Status: common.TopUpStatusPending, CreateTime: common.GetTimestamp(),
	}
	require.NoError(t, order.Insert())

	alreadyDone, err := RechargeEpay(order.TradeNo, order.PaymentMethod, "127.0.0.1")
	require.NoError(t, err)
	require.False(t, alreadyDone)
	var reloaded User
	require.NoError(t, DB.First(&reloaded, user.Id).Error)
	require.Equal(t, 677, reloaded.Quota)

	alreadyDone, err = RechargeEpay(order.TradeNo, order.PaymentMethod, "127.0.0.1")
	require.NoError(t, err)
	require.True(t, alreadyDone)
	require.NoError(t, DB.First(&reloaded, user.Id).Error)
	require.Equal(t, 677, reloaded.Quota)
}

func TestRechargeEpayLegacyOrderFallsBackToIntegerAmount(t *testing.T) {
	truncateTables(t)
	originalQuotaPerUnit := common.QuotaPerUnit
	common.QuotaPerUnit = 100
	t.Cleanup(func() { common.QuotaPerUnit = originalQuotaPerUnit })

	user := &User{Id: 802, Username: "legacy-payment-user", Status: common.UserStatusEnabled, Quota: 0}
	require.NoError(t, DB.Create(user).Error)
	order := &TopUp{
		UserId: user.Id, Amount: 7, Money: 7,
		TradeNo: "payment-ratio-legacy", PaymentMethod: "alipay", PaymentProvider: PaymentProviderEpay,
		Status: common.TopUpStatusPending, CreateTime: common.GetTimestamp(),
	}
	require.NoError(t, order.Insert())

	_, err := RechargeEpay(order.TradeNo, order.PaymentMethod, "127.0.0.1")
	require.NoError(t, err)
	var reloaded User
	require.NoError(t, DB.First(&reloaded, user.Id).Error)
	require.Equal(t, 700, reloaded.Quota)
}
