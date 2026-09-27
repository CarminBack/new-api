package ratio_setting

import (
	"errors"
	"math"
	"strings"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/types"
)

var defaultImageGroupResolutionRatio = map[string]float64{
	"1k": 1,
	"2k": 1.6,
	"4k": 2,
}

var imageGroupResolutionRatioMap = types.NewRWMap[string, float64]()

func init() {
	imageGroupResolutionRatioMap.AddAll(defaultImageGroupResolutionRatio)
}

func ImageGroupResolutionRatio2JSONString() string {
	return imageGroupResolutionRatioMap.MarshalJSONString()
}

func GetImageGroupResolutionRatio(tier string) (float64, bool) {
	return imageGroupResolutionRatioMap.Get(strings.ToLower(strings.TrimSpace(tier)))
}

func GetImageGroupResolutionRatioCopy() map[string]float64 {
	return imageGroupResolutionRatioMap.ReadAll()
}

func CheckImageGroupResolutionRatio(jsonStr string) error {
	ratios := make(map[string]float64)
	if err := common.UnmarshalJsonStr(jsonStr, &ratios); err != nil {
		return err
	}
	for _, tier := range []string{"1k", "2k", "4k"} {
		ratio, ok := ratios[tier]
		if !ok {
			return errors.New("missing image resolution ratio: " + tier)
		}
		if ratio <= 0 || math.IsNaN(ratio) || math.IsInf(ratio, 0) {
			return errors.New("image resolution ratio must be a positive finite number: " + tier)
		}
	}
	if len(ratios) != 3 {
		return errors.New("image resolution ratios only support 1k, 2k and 4k")
	}
	return nil
}

func UpdateImageGroupResolutionRatioByJSONString(jsonStr string) error {
	if err := CheckImageGroupResolutionRatio(jsonStr); err != nil {
		return err
	}
	return types.LoadFromJsonString(imageGroupResolutionRatioMap, jsonStr)
}
