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

func TestBuildTokenVideoModelsReturnsSafePriceAndLimits(t *testing.T) {
	items := []model.Pricing{
		{ModelName: "seedance-720p-c49", Description: "Seedance video", ModelPrice: 0.08, QuotaType: 2, EnableGroup: []string{"Video"}, BillingExpr: `tier("base", u("seconds") * 0.08)`},
		{ModelName: "gpt-image-2", ModelPrice: 0.04, QuotaType: 1, EnableGroup: []string{"Image"}},
	}
	catalog := buildTokenVideoModels(items, "Video", 1.35)
	if len(catalog) != 1 {
		t.Fatalf("expected one video model, got %+v", catalog)
	}
	entry := catalog[0]
	if entry.ID != "seedance-720p-c49" || entry.PriceLabel != "$0.108/秒" {
		t.Fatalf("unexpected catalog entry: %+v", entry)
	}
	joined := strings.Join(entry.Limitations, " ")
	for _, expected := range []string{"720p", "4–15 秒", "首尾帧", "2000"} {
		if !strings.Contains(joined, expected) {
			t.Fatalf("missing limitation %q in %q", expected, joined)
		}
	}
	encoded, err := common.Marshal(catalog)
	if err != nil {
		t.Fatal(err)
	}
	for _, private := range []string{"billing_expr", "channel", "token", "upstream"} {
		if strings.Contains(strings.ToLower(string(encoded)), private) {
			t.Fatalf("catalog exposes private field %q: %s", private, encoded)
		}
	}
}

func TestTokenVideoPriceLabelSupportsPerVideoPricing(t *testing.T) {
	item := model.Pricing{ModelPrice: 1.2, QuotaType: 1}
	if got := tokenVideoPriceLabel(item, 1.5); got != "$1.8/条" {
		t.Fatalf("unexpected label %q", got)
	}
}
