package controller

import (
	"encoding/base64"
	"testing"

	"github.com/QuantumNous/new-api/model"
	"github.com/QuantumNous/new-api/pkg/billingexpr"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestTaskBillingInfoPublishesSafeTieredSnapshot(t *testing.T) {
	expression := `tier("base", u("seconds") * 0.7)`
	task := &model.Task{PrivateData: model.TaskPrivateData{BillingContext: &model.TaskBillingContext{
		ModelPrice:      0.7,
		GroupRatio:      0.4,
		OriginModelName: "seedance-720p-c47",
		TieredSnapshot: &billingexpr.BillingSnapshot{
			ExprString:    expression,
			EstimatedTier: "base",
			UsageFacts:    map[string]any{"seconds": float64(10), "videos": float64(1)},
		},
	}}}

	billing := taskBillingInfo(task)
	require.NotNil(t, billing)
	assert.Equal(t, "tiered_expr", billing.Mode)
	assert.Equal(t, 0.7, billing.ModelPrice)
	assert.Equal(t, 0.4, billing.GroupRatio)
	assert.Equal(t, base64.StdEncoding.EncodeToString([]byte(expression)), billing.ExprB64)
	assert.Equal(t, "base", billing.MatchedTier)
	assert.Equal(t, float64(10), billing.UsageFacts["seconds"])
}

func TestTaskBillingInfoOmitsMissingContext(t *testing.T) {
	assert.Nil(t, taskBillingInfo(&model.Task{}))
}
