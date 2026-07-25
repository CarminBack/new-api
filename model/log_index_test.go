package model

import (
	"testing"

	"github.com/glebarez/sqlite"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

func TestLogQueryIndexesUseFilterOrder(t *testing.T) {
	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{})
	require.NoError(t, err)
	require.NoError(t, db.AutoMigrate(&Log{}))

	assertIndexColumns := func(index string, want []string) {
		t.Helper()
		var columns []struct {
			Seq  int    `gorm:"column:seqno"`
			Name string `gorm:"column:name"`
		}
		require.NoError(t, db.Raw("PRAGMA index_info('"+index+"')").Scan(&columns).Error)
		require.Len(t, columns, len(want))
		for i := range want {
			require.Equal(t, i, columns[i].Seq)
			require.Equal(t, want[i], columns[i].Name)
		}
	}

	assertIndexColumns("idx_log_time_id", []string{"created_at", "id"})
	assertIndexColumns("idx_log_type_time", []string{"type", "created_at"})
}
