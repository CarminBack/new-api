package model

import (
	"os"
	"testing"

	"github.com/stretchr/testify/require"
	"gorm.io/driver/mysql"
	"gorm.io/driver/postgres"
	"gorm.io/gorm"
)

func TestChannelHealthStateCrossDatabase(t *testing.T) {
	tests := []struct {
		name      string
		envName   string
		dialector func(string) gorm.Dialector
	}{
		{name: "mysql", envName: "NEW_API_TEST_MYSQL_DSN", dialector: func(dsn string) gorm.Dialector { return mysql.Open(dsn) }},
		{name: "postgres", envName: "NEW_API_TEST_POSTGRES_DSN", dialector: func(dsn string) gorm.Dialector { return postgres.Open(dsn) }},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			dsn := os.Getenv(test.envName)
			if dsn == "" {
				t.Skipf("%s is not configured", test.envName)
			}
			db, err := gorm.Open(test.dialector(dsn), &gorm.Config{})
			require.NoError(t, err)
			require.NoError(t, db.AutoMigrate(&ChannelHealthState{}))

			state := &ChannelHealthState{
				ScopeKey:               "cross-database-health-state",
				ChannelID:              29,
				Fingerprint:            "cross-database-fingerprint",
				Scope:                  "route",
				ModelName:              "gpt-test",
				RequestPath:            "/v1/responses",
				State:                  "probing",
				NextProbeAt:            100,
				Revision:               2,
				ProbeID:                7,
				ProbeType:              "initial",
				ProbeFailures:          4,
				RecoveryTargetCapacity: 128,
				RecoveryCapacity:       2,
				RecoverySuccesses:      3,
				RecoveryStartedAt:      90,
			}
			require.NoError(t, db.Save(state).Error)
			require.NoError(t, db.Model(&ChannelHealthState{}).
				Where("scope_key = ?", state.ScopeKey).
				Updates(map[string]any{"state": "open", "next_probe_at": int64(200), "revision": uint64(3)}).Error)

			var saved ChannelHealthState
			require.NoError(t, db.First(&saved, "scope_key = ?", state.ScopeKey).Error)
			require.Equal(t, "open", saved.State)
			require.Equal(t, int64(200), saved.NextProbeAt)
			require.Equal(t, uint64(3), saved.Revision)
			require.Equal(t, 4, saved.ProbeFailures)
			require.Equal(t, 128, saved.RecoveryTargetCapacity)
			require.Equal(t, 2, saved.RecoveryCapacity)
			require.Equal(t, 3, saved.RecoverySuccesses)
			require.Equal(t, int64(90), saved.RecoveryStartedAt)
			require.NoError(t, db.Where("scope_key = ?", state.ScopeKey).Delete(&ChannelHealthState{}).Error)
		})
	}
}
