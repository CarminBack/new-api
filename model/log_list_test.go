package model

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestAdminLogListDefersErrorOther(t *testing.T) {
	truncateTables(t)

	errorOther := `{"admin_info":{"request_diagnostic":{"body":"` + strings.Repeat("x", 1024*1024) + `"}}}`
	errorLog := &Log{CreatedAt: 2, Type: LogTypeError, Content: "upstream failed", Other: errorOther}
	consumeLog := &Log{CreatedAt: 1, Type: LogTypeConsume, Content: "ok", Other: `{"request_path":"/v1/responses"}`}
	require.NoError(t, LOG_DB.Create(errorLog).Error)
	require.NoError(t, LOG_DB.Create(consumeLog).Error)

	logs, total, err := GetAllLogs(LogTypeUnknown, 0, 0, "", "", "", 0, 20, 0, "", "", "")
	require.NoError(t, err)
	require.Equal(t, int64(2), total)
	require.Len(t, logs, 2)
	require.Equal(t, errorLog.Id, logs[0].Id)
	require.Empty(t, logs[0].Other)
	require.True(t, logs[0].OtherDeferred)
	require.Equal(t, consumeLog.Other, logs[1].Other)
	require.False(t, logs[1].OtherDeferred)

	other, err := GetLogOtherById(errorLog.Id)
	require.NoError(t, err)
	require.Equal(t, errorOther, other)
}

func TestAdminErrorLogFilterDefersEveryOtherField(t *testing.T) {
	truncateTables(t)

	first := &Log{CreatedAt: 2, Type: LogTypeError, Other: `{"error":"first"}`}
	second := &Log{CreatedAt: 1, Type: LogTypeError, Other: `{"error":"second"}`}
	require.NoError(t, LOG_DB.Create(first).Error)
	require.NoError(t, LOG_DB.Create(second).Error)

	logs, total, err := GetAllLogs(LogTypeError, 0, 0, "", "", "", 0, 20, 0, "", "", "")
	require.NoError(t, err)
	require.Equal(t, int64(2), total)
	require.Len(t, logs, 2)
	for _, log := range logs {
		require.Empty(t, log.Other)
		require.True(t, log.OtherDeferred)
	}
}
