package controller

import (
	"fmt"
	"net/http"
	"os"
	"testing"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/model"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestTokenUsageDateRangeDatabaseMatrix(t *testing.T) {
	for _, database := range []struct {
		name, env string
		typ       common.DatabaseType
	}{
		{"sqlite", "", common.DatabaseTypeSQLite},
		{"mysql", "AUDIT_MYSQL_DSN", common.DatabaseTypeMySQL},
		{"postgres", "AUDIT_POSTGRES_DSN", common.DatabaseTypePostgreSQL},
	} {
		t.Run(database.name, func(t *testing.T) {
			dsn := os.Getenv(database.env)
			if database.env != "" && dsn == "" {
				t.Skip(database.env + " is not configured")
			}
			previousDB, previousLogDB := model.DB, model.LOG_DB
			previousMain, previousLog := common.MainDatabaseType(), common.LogDatabaseType()
			previousRedis := common.RedisEnabled
			t.Cleanup(func() {
				model.DB, model.LOG_DB = previousDB, previousLogDB
				common.SetDatabaseTypes(previousMain, previousLog)
				common.RedisEnabled = previousRedis
			})
			gin.SetMode(gin.TestMode)
			common.RedisEnabled = false
			common.SetDatabaseTypes(database.typ, database.typ)
			db, _ := newAuditTestDatabase(t, database.name, dsn)
			logDB, _ := newAuditTestDatabase(t, database.name, dsn)
			model.DB, model.LOG_DB = db, logDB
			require.NoError(t, db.AutoMigrate(&model.Token{}))
			require.NoError(t, logDB.AutoMigrate(&model.Log{}))
			versionSQL := "SELECT version()"
			if database.name == "sqlite" {
				versionSQL = "SELECT sqlite_version()"
			}
			var version string
			require.NoError(t, db.Raw(versionSQL).Scan(&version).Error)
			t.Logf("database version: %s (separate log database)", version)
			first := seedToken(t, db, 1, "shared-name", "first-test-key-not-real")
			second := seedToken(t, db, 1, "shared-name", "second-test-key-not-real")
			empty := seedToken(t, db, 1, "unused", "empty-test-key-not-real")
			foreign := seedToken(t, db, 2, "shared-name", "foreign-test-key-not-real")
			require.NoError(t, db.Model(first).Update("used_quota", 98765).Error)
			require.NoError(t, db.Model(second).Update("status", common.TokenStatusDisabled).Error)
			logs := []model.Log{
				{UserId: 1, TokenId: first.Id, CreatedAt: 99, Type: model.LogTypeConsume, Quota: 999},
				{UserId: 1, TokenId: first.Id, TokenName: "old-name", CreatedAt: 100, Type: model.LogTypeConsume, Quota: 1000},
				{UserId: 1, TokenId: first.Id, CreatedAt: 199, Type: model.LogTypeConsume, Quota: 300},
				{UserId: 1, TokenId: first.Id, CreatedAt: 150, Type: model.LogTypeRefund, Quota: 200},
				{UserId: 1, TokenId: first.Id, CreatedAt: 150, Type: model.LogTypeConsume, Quota: 3_000_000_000},
				{UserId: 1, TokenId: first.Id, CreatedAt: 200, Type: model.LogTypeConsume, Quota: 999},
				{UserId: 1, TokenId: first.Id, CreatedAt: 150, Type: model.LogTypeError, Quota: 999},
				{UserId: 1, TokenId: first.Id, CreatedAt: 150, Type: model.LogTypeTopup, Quota: 999},
				{UserId: 2, TokenId: first.Id, CreatedAt: 150, Type: model.LogTypeConsume, Quota: 999},
				{UserId: 1, TokenId: second.Id, TokenName: "shared-name", CreatedAt: 150, Type: model.LogTypeConsume, Quota: 20},
				{UserId: 1, TokenId: second.Id, CreatedAt: 150, Type: model.LogTypeRefund, Quota: 70},
				{UserId: 2, TokenId: foreign.Id, CreatedAt: 150, Type: model.LogTypeConsume, Quota: 999},
			}
			require.NoError(t, logDB.Create(&logs).Error)
			expected := map[int]int64{first.Id: 3_000_001_100, second.Id: -50, empty.Id: 0}
			for _, tc := range []struct {
				path    string
				handler gin.HandlerFunc
				count   int
			}{
				{"/api/token/?p=1&size=10&start_timestamp=100&end_timestamp=200", GetAllTokens, 3},
				{"/api/token/search?keyword=shared-name&p=1&size=10&start_timestamp=100&end_timestamp=200", SearchTokens, 2},
				{"/api/token/?p=2&size=1&start_timestamp=100&end_timestamp=200", GetAllTokens, 1},
				{"/api/token/search?keyword=shared-name&p=2&size=1&start_timestamp=100&end_timestamp=200", SearchTokens, 1},
				{"/api/token/search?keyword=missing&start_timestamp=100&end_timestamp=200", SearchTokens, 0},
			} {
				t.Run(tc.path, func(t *testing.T) {
					ctx, recorder := newAuthenticatedContext(t, http.MethodGet, tc.path, nil, 1)
					tc.handler(ctx)
					response := decodeAPIResponse(t, recorder)
					require.True(t, response.Success, recorder.Body.String())
					var page struct {
						Items []struct {
							ID        int    `json:"id"`
							Period    *int64 `json:"period_used_quota"`
							Used      int    `json:"used_quota"`
							Remaining int    `json:"remain_quota"`
						} `json:"items"`
					}
					require.NoError(t, common.Unmarshal(response.Data, &page))
					require.Len(t, page.Items, tc.count)
					for _, item := range page.Items {
						require.NotNil(t, item.Period)
						assert.Equal(t, expected[item.ID], *item.Period)
						assert.NotEqual(t, foreign.Id, item.ID)
						assert.Equal(t, 100, item.Remaining)
						if item.ID == first.Id {
							assert.Equal(t, 98765, item.Used)
						}
					}
					for _, token := range []*model.Token{first, second, foreign} {
						assert.NotContains(t, recorder.Body.String(), token.Key)
					}
				})
			}
			for _, query := range []string{"start_timestamp=100", "end_timestamp=200", "start_timestamp=&end_timestamp=200", "start_timestamp=-1&end_timestamp=200", "start_timestamp=200&end_timestamp=100", "start_timestamp=100&end_timestamp=100", "start_timestamp=abc&end_timestamp=200", "start_timestamp=100&end_timestamp=9223372036854775808"} {
				for _, handler := range []gin.HandlerFunc{GetAllTokens, SearchTokens} {
					ctx, recorder := newAuthenticatedContext(t, http.MethodGet, "/api/token/?"+query, nil, 1)
					handler(ctx)
					assert.False(t, decodeAPIResponse(t, recorder).Success, query)
				}
			}
			ctx, recorder := newAuthenticatedContext(t, http.MethodGet, "/api/token/?start_timestamp=0&end_timestamp=100", nil, 1)
			GetAllTokens(ctx)
			assert.True(t, decodeAPIResponse(t, recorder).Success)
			logSQL, err := logDB.DB()
			require.NoError(t, err)
			require.NoError(t, logSQL.Close())
			for _, handler := range []gin.HandlerFunc{GetAllTokens, SearchTokens} {
				ctx, recorder := newAuthenticatedContext(t, http.MethodGet, "/api/token/", nil, 1)
				handler(ctx)
				assert.True(t, decodeAPIResponse(t, recorder).Success, "unfiltered list must not depend on log database")
				assert.NotContains(t, recorder.Body.String(), "period_used_quota")
				ctx, recorder = newAuthenticatedContext(t, http.MethodGet, "/api/token/?start_timestamp=100&end_timestamp=200", nil, 1)
				handler(ctx)
				response := decodeAPIResponse(t, recorder)
				assert.False(t, response.Success, "query failures must not become zero usage")
				assert.NotContains(t, fmt.Sprint(response), "database is closed")
			}
		})
	}
}
