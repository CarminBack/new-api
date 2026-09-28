package controller

import (
	"net/http"
	"strings"
	"testing"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/model"
)

func TestCanvasAccountBalanceUsesAuthenticatedOwnerQuota(t *testing.T) {
	db := setupTokenControllerTestDB(t)
	if err := db.AutoMigrate(&model.User{}); err != nil {
		t.Fatal(err)
	}
	user := &model.User{Username: "canvas-balance", Status: common.UserStatusEnabled, Quota: 1250000, AffCode: "canvas-balance-aff"}
	if err := db.Create(user).Error; err != nil {
		t.Fatal(err)
	}

	ctx, recorder := newAuthenticatedContext(t, http.MethodGet, "/api/usage/token/balance?user_id=999", nil, user.Id)
	GetTokenUserBalance(ctx)

	response := decodeAPIResponse(t, recorder)
	var data struct {
		Quota        int     `json:"quota"`
		QuotaPerUnit float64 `json:"quota_per_unit"`
	}
	if err := common.Unmarshal(response.Data, &data); err != nil {
		t.Fatal(err)
	}
	if !response.Success || data.Quota != user.Quota || data.QuotaPerUnit != common.QuotaPerUnit {
		t.Fatalf("wrong account balance: %+v", data)
	}
	if recorder.Header().Get("Cache-Control") != "no-store" {
		t.Fatal("balance must not be cached")
	}
	for _, private := range []string{"username", "password", "access_token"} {
		if strings.Contains(recorder.Body.String(), private) {
			t.Fatalf("response exposes private field %q", private)
		}
	}
}
