package helper

import (
	"net/http/httptest"
	"testing"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/constant"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

func ratioLimitContext(maxRatio float64, setLimit bool) *gin.Context {
	gin.SetMode(gin.TestMode)
	c, _ := gin.CreateTestContext(httptest.NewRecorder())
	if setLimit {
		common.SetContextKey(c, constant.ContextKeyTokenMaxRatio, maxRatio)
	}
	return c
}

func TestCheckTokenGroupRatioLimit(t *testing.T) {
	tests := []struct {
		name      string
		actual    float64
		max       float64
		setLimit  bool
		wantError bool
	}{
		{name: "unconfigured", actual: 100, wantError: false},
		{name: "zero means unlimited", actual: 100, max: 0, setLimit: true, wantError: false},
		{name: "below limit", actual: 0.08, max: 0.1, setLimit: true, wantError: false},
		{name: "equal limit", actual: 0.1, max: 0.1, setLimit: true, wantError: false},
		{name: "above limit", actual: 0.11, max: 0.1, setLimit: true, wantError: true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := CheckTokenGroupRatioLimit(ratioLimitContext(tt.max, tt.setLimit), tt.actual)
			if tt.wantError {
				require.ErrorIs(t, err, ErrTokenRatioExceeded)
				return
			}
			require.NoError(t, err)
		})
	}
}
