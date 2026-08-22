package helper

import (
	"errors"
	"fmt"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/constant"
	"github.com/gin-gonic/gin"
)

// ErrTokenRatioExceeded identifies a request rejected before billing or an
// upstream connection because the API key's effective group ratio is too high.
var ErrTokenRatioExceeded = errors.New("倍率超出上限")

// CheckTokenGroupRatioLimit compares the already-resolved effective group
// ratio with the API key limit loaded by TokenAuth. A zero limit means that no
// limit is configured, preserving the behavior of existing tokens.
func CheckTokenGroupRatioLimit(c *gin.Context, actualRatio float64) error {
	maxRatio, ok := common.GetContextKeyType[float64](c, constant.ContextKeyTokenMaxRatio)
	if !ok || maxRatio <= 0 {
		return nil
	}
	if actualRatio <= maxRatio {
		return nil
	}
	return fmt.Errorf("%w：当前倍率 %.6g，高于允许上限 %.6g", ErrTokenRatioExceeded, actualRatio, maxRatio)
}
