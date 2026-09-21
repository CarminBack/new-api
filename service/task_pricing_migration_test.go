package service

import (
	"os"
	"testing"

	"github.com/QuantumNous/new-api/common"
	_ "github.com/QuantumNous/new-api/plugins"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestBuildTaskPricingExpression(t *testing.T) {
	expression, err := buildTaskPricingExpression(TaskPricingUnitPerSecond, 0.35)
	require.NoError(t, err)
	assert.Equal(t, `tier("base", u("seconds") * 0.35)`, expression)

	expression, err = buildTaskPricingExpression(TaskPricingUnitPerItem, 6.08)
	require.NoError(t, err)
	assert.Equal(t, `tier("base", u("videos") * 6.08)`, expression)

	_, err = buildTaskPricingExpression("request", 1)
	require.Error(t, err)
}

func TestVideoPricingMigrationManifest(t *testing.T) {
	data, err := os.ReadFile("../docs/migrations/video-pricing-20260918.json")
	require.NoError(t, err)
	var request TaskPricingMigrationRequest
	require.NoError(t, common.Unmarshal(data, &request))
	request.DryRun = true

	perSecond, perItem := 0, 0
	for _, entry := range request.Models {
		switch entry.Unit {
		case TaskPricingUnitPerSecond:
			perSecond++
		case TaskPricingUnitPerItem:
			perItem++
		}
	}
	assert.Len(t, request.Models, 34)
	assert.Equal(t, 29, perSecond)
	assert.Equal(t, 5, perItem)

	result, err := MigrateTaskPricing(request)
	require.NoError(t, err)
	assert.True(t, result.DryRun)
	assert.Equal(t, 34, result.Total)
}
