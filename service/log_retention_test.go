package service

import (
	"context"
	"testing"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/model"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestLogCleanupHandlerRetentionConfiguration(t *testing.T) {
	t.Setenv("LOG_RETENTION_ENABLED", "false")
	handler := logCleanupHandler{}
	assert.False(t, handler.Enabled())

	t.Setenv("LOG_RETENTION_ENABLED", "true")
	t.Setenv("LOG_RETENTION_CONSUME_DAYS", "0")
	t.Setenv("LOG_RETENTION_ERROR_DAYS", "45")
	require.True(t, handler.Enabled())

	payload, ok := handler.NewPayload().(LogCleanupPayload)
	require.True(t, ok)
	assert.Equal(t, logCleanupBatchSize, payload.BatchSize)
	assert.Equal(t, int(logRetentionBatchDelay.Milliseconds()), payload.BatchDelayMillis)

	retentionByType := make(map[int]int, len(payload.RetentionPolicies))
	for _, policy := range payload.RetentionPolicies {
		retentionByType[policy.LogType] = policy.RetentionDays
	}
	assert.NotContains(t, retentionByType, model.LogTypeConsume)
	assert.Equal(t, 45, retentionByType[model.LogTypeError])
	assert.Equal(t, 365, retentionByType[model.LogTypeTopup])
	assert.Equal(t, 365, retentionByType[model.LogTypeManage])
	assert.Equal(t, 365, retentionByType[model.LogTypeRefund])
	assert.Equal(t, 180, retentionByType[model.LogTypeLogin])
}

func TestRunLogRetentionCleanupAppliesPerTypeCutoffs(t *testing.T) {
	truncate(t)
	now := common.GetTimestamp()
	day := int64(24 * 60 * 60)
	logs := []model.Log{
		{Id: 1, Type: model.LogTypeTopup, CreatedAt: now - 366*day},
		{Id: 2, Type: model.LogTypeTopup, CreatedAt: now - 364*day},
		{Id: 3, Type: model.LogTypeConsume, CreatedAt: now - 181*day},
		{Id: 4, Type: model.LogTypeConsume, CreatedAt: now - 179*day},
		{Id: 5, Type: model.LogTypeManage, CreatedAt: now - 366*day},
		{Id: 6, Type: model.LogTypeManage, CreatedAt: now - 364*day},
		{Id: 7, Type: model.LogTypeError, CreatedAt: now - 31*day},
		{Id: 8, Type: model.LogTypeError, CreatedAt: now - 29*day},
		{Id: 9, Type: model.LogTypeRefund, CreatedAt: now - 366*day},
		{Id: 10, Type: model.LogTypeRefund, CreatedAt: now - 364*day},
		{Id: 11, Type: model.LogTypeLogin, CreatedAt: now - 181*day},
		{Id: 12, Type: model.LogTypeLogin, CreatedAt: now - 179*day},
		{Id: 13, Type: model.LogTypeUnknown, CreatedAt: now - 1000*day},
		{Id: 14, Type: model.LogTypeSystem, CreatedAt: now - 1000*day},
	}
	require.NoError(t, model.LOG_DB.Create(&logs).Error)

	payload := LogCleanupPayload{
		RetentionPolicies: []LogRetentionPolicy{
			{LogType: model.LogTypeTopup, RetentionDays: 365},
			{LogType: model.LogTypeConsume, RetentionDays: 180},
			{LogType: model.LogTypeManage, RetentionDays: 365},
			{LogType: model.LogTypeError, RetentionDays: 30},
			{LogType: model.LogTypeRefund, RetentionDays: 365},
			{LogType: model.LogTypeLogin, RetentionDays: 180},
		},
		BatchSize:        2,
		BatchDelayMillis: 0,
	}
	task, err := model.CreateSystemTask(model.SystemTaskTypeLogCleanup, payload, nil)
	require.NoError(t, err)
	claimedTask, claimed, err := model.ClaimSystemTask(task.ID, task.Type, "retention-runner", common.GetTimestamp()+60)
	require.NoError(t, err)
	require.True(t, claimed)

	runLogCleanupTask(context.Background(), claimedTask, "retention-runner")

	var remainingIDs []int
	require.NoError(t, model.LOG_DB.Model(&model.Log{}).Order("id asc").Pluck("id", &remainingIDs).Error)
	assert.Equal(t, []int{2, 4, 6, 8, 10, 12, 13, 14}, remainingIDs)

	reloaded, err := model.GetSystemTaskByTaskID(task.TaskID)
	require.NoError(t, err)
	require.NotNil(t, reloaded)
	assert.Equal(t, model.SystemTaskStatusSucceeded, reloaded.Status)
	var result LogRetentionResult
	require.NoError(t, common.UnmarshalJsonStr(reloaded.Result, &result))
	assert.Equal(t, int64(6), result.DeletedCount)
	require.Len(t, result.Policies, 6)
	for _, policy := range result.Policies {
		assert.Equal(t, int64(1), policy.Processed)
		assert.Zero(t, policy.Remaining)
	}
}

func TestRunLogRetentionCleanupRejectsUnmanagedType(t *testing.T) {
	truncate(t)
	now := common.GetTimestamp()
	log := model.Log{Id: 1, Type: model.LogTypeSystem, CreatedAt: now - 400*24*60*60}
	require.NoError(t, model.LOG_DB.Create(&log).Error)

	payload := LogCleanupPayload{
		RetentionPolicies: []LogRetentionPolicy{{LogType: model.LogTypeSystem, RetentionDays: 30}},
		BatchSize:         1,
	}
	task, err := model.CreateSystemTask(model.SystemTaskTypeLogCleanup, payload, nil)
	require.NoError(t, err)
	claimedTask, claimed, err := model.ClaimSystemTask(task.ID, task.Type, "retention-runner", common.GetTimestamp()+60)
	require.NoError(t, err)
	require.True(t, claimed)

	runLogCleanupTask(context.Background(), claimedTask, "retention-runner")

	reloaded, err := model.GetSystemTaskByTaskID(task.TaskID)
	require.NoError(t, err)
	require.NotNil(t, reloaded)
	assert.Equal(t, model.SystemTaskStatusFailed, reloaded.Status)
	assert.Contains(t, reloaded.Error, "unsupported log retention type")
	var count int64
	require.NoError(t, model.LOG_DB.Model(&model.Log{}).Where("id = ?", log.Id).Count(&count).Error)
	assert.Equal(t, int64(1), count)
}

func TestRunManualLogCleanupStillDeletesAllTypes(t *testing.T) {
	truncate(t)
	now := common.GetTimestamp()
	logs := []model.Log{
		{Id: 1, Type: model.LogTypeSystem, CreatedAt: now - 2},
		{Id: 2, Type: model.LogTypeUnknown, CreatedAt: now - 2},
		{Id: 3, Type: model.LogTypeConsume, CreatedAt: now},
	}
	require.NoError(t, model.LOG_DB.Create(&logs).Error)

	payload := LogCleanupPayload{TargetTimestamp: now - 1, BatchSize: 1}
	task, err := model.CreateSystemTask(model.SystemTaskTypeLogCleanup, payload, nil)
	require.NoError(t, err)
	claimedTask, claimed, err := model.ClaimSystemTask(task.ID, task.Type, "manual-runner", common.GetTimestamp()+60)
	require.NoError(t, err)
	require.True(t, claimed)

	runLogCleanupTask(context.Background(), claimedTask, "manual-runner")

	var remainingIDs []int
	require.NoError(t, model.LOG_DB.Model(&model.Log{}).Order("id asc").Pluck("id", &remainingIDs).Error)
	assert.Equal(t, []int{3}, remainingIDs)
}
