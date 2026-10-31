package controller

import (
	"net/http"
	"strings"
	"testing"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/model"
	"github.com/QuantumNous/new-api/pkg/jsplugin"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
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
		{ModelName: "seedance-720p-c49", Description: "Seedance video", ModelPrice: 0.08, QuotaType: 1, EnableGroup: []string{"Video"}, BillingExpr: `tier("base", u("seconds") * 0.08)`},
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

func TestTokenVideoCatalogIncludesPublicCapabilities(t *testing.T) {
	original := jsplugin.DefaultRegistry
	jsplugin.DefaultRegistry = jsplugin.NewRegistry()
	t.Cleanup(func() { jsplugin.DefaultRegistry = original })
	_, err := jsplugin.DefaultRegistry.RegisterFactory(`
export const meta = {apiVersion:1,key:"catalog-caps",name:"Catalog",version:"1.0.0",author:{name:"Test"},models:["cinema-pro"],fetchMode:"per_task",protocols:["openai_video"],videoCapabilities:{"cinema-pro":{requestFormat:"unified_video_v1",resolutions:["768p"],ratios:["8:5"],minSeconds:2,maxSeconds:120,frames:false,maxVideos:0}}};
export function buildSubmitRequest(){return {}};
export function parseSubmitResponse(){return {}};
export function buildQueryRequest(){return {}};
export function parseTaskResult(){return {}};
export function listArtifacts(){return []};
export function buildContentRequest(){return {}};
export const protocols={openai_video:{decodeRequest:function(ctx){return ctx},render:function(ctx,task){return task}}};
`, jsplugin.Options{})
	require.NoError(t, err)
	entries := buildTokenVideoModels([]model.Pricing{{ModelName: "cinema-pro", EnableGroup: []string{"default"}, ModelPrice: 0.2, QuotaType: 1}}, "default", 1)
	require.Len(t, entries, 1)
	require.NotNil(t, entries[0].Capabilities)
	assert.Equal(t, 120, *entries[0].Capabilities.MaxSeconds)
	assert.Zero(t, *entries[0].Capabilities.MaxVideos)
	assert.Contains(t, strings.Join(entries[0].Limitations, " "), "768p")
	assert.Contains(t, strings.Join(entries[0].Limitations, " "), "不支持首尾帧")
	encoded, err := common.Marshal(entries)
	require.NoError(t, err)
	for _, private := range []string{"apiKey", "baseUrl", "plugin_key", "billing_expr"} {
		assert.NotContains(t, string(encoded), private)
	}
}

func TestTokenVideoPriceLabelUsesBillingExpression(t *testing.T) {
	// ModelPrice is stale; the label must follow the expression that actually bills.
	item := model.Pricing{ModelPrice: 0.35, QuotaType: 2, BillingExpr: `tier("base", u("seconds") * 0.25)`}
	if got := tokenVideoPriceLabel(item, 1); got != "$0.25/秒" {
		t.Fatalf("unexpected label %q", got)
	}
	if got := tokenVideoPriceLabel(item, 1.4); got != "$0.35/秒" {
		t.Fatalf("unexpected label with group ratio %q", got)
	}
	item = model.Pricing{ModelPrice: 0.5, QuotaType: 2, BillingExpr: `tier("base", u("seconds") * u("videos") * 2)`}
	if got := tokenVideoPriceLabel(item, 1); got != "$0.5/秒" {
		t.Fatalf("multi-fact expression should fall back to ModelPrice, got %q", got)
	}
}

func TestTokenVideoPriceLabelSupportsPerVideoPricing(t *testing.T) {
	item := model.Pricing{ModelPrice: 1.2, QuotaType: 2, BillingExpr: `tier("base", u("videos") * 1.2)`}
	if got := tokenVideoPriceLabel(item, 1.5); got != "$1.8/条" {
		t.Fatalf("unexpected label %q", got)
	}
}

func TestTokenVideoPriceLabelHandlesReferenceVideoBranches(t *testing.T) {
	// Per-second models branching on video_input used to render "按量计费".
	item := model.Pricing{QuotaType: 1, BillingExpr: `u("video_input") == "video" ? tier("with_video", u("seconds") * 1.5) : tier("base", u("seconds") * 1.2)`}
	assert.Equal(t, "$1.2/秒，参考视频 $1.5/秒", tokenVideoPriceLabel(item, 1))
	assert.Equal(t, "$2.4/秒，参考视频 $3/秒", tokenVideoPriceLabel(item, 2))

	// Equal rates need no second price.
	item.BillingExpr = `u("video_input") == "video" ? tier("with_video", u("seconds") * 0.3) : tier("base", u("seconds") * 0.3)`
	assert.Equal(t, "$0.3/秒", tokenVideoPriceLabel(item, 1))

	// Reference seconds are billed on top of the output seconds.
	item.BillingExpr = `u("video_input") == "video" ? tier("with_video", u("seconds") * 0.7 + u("reference_seconds") * 0.7) : tier("base", u("seconds") * 0.7)`
	assert.Equal(t, "$0.7/秒，参考视频 另加 $0.7/参考秒", tokenVideoPriceLabel(item, 1))
	item.BillingExpr = `u("video_input") == "video" ? tier("with_video", u("seconds") * 0.8 + u("reference_seconds") * 0.5) : tier("base", u("seconds") * 0.7)`
	assert.Equal(t, "$0.7/秒，参考视频 $0.8/秒 + $0.5/参考秒", tokenVideoPriceLabel(item, 1))

	// A missing unit price still falls back honestly.
	item = model.Pricing{QuotaType: 1, BillingExpr: `tier("base", u("frames") * u("steps") * 2)`}
	assert.Equal(t, "按量计费", tokenVideoPriceLabel(item, 1))
}
