package ratio_setting

import (
	"errors"
	"strings"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/types"
)

var defaultImageGroupPrice = map[string]float64{
	"1k": 0.10,
	"2k": 0.14,
	"4k": 0.20,
}

var imageGroupPriceMap = types.NewRWMap[string, float64]()

func init() {
	imageGroupPriceMap.AddAll(defaultImageGroupPrice)
}

func ImageGroupPrice2JSONString() string {
	return imageGroupPriceMap.MarshalJSONString()
}

func GetImageGroupPrice(tier string) (float64, bool) {
	return imageGroupPriceMap.Get(strings.ToLower(strings.TrimSpace(tier)))
}

func GetImageGroupPriceCopy() map[string]float64 {
	return imageGroupPriceMap.ReadAll()
}

func CheckImageGroupPrice(jsonStr string) error {
	prices := make(map[string]float64)
	if err := common.UnmarshalJsonStr(jsonStr, &prices); err != nil {
		return err
	}
	for _, tier := range []string{"1k", "2k", "4k"} {
		price, ok := prices[tier]
		if !ok {
			return errors.New("missing image resolution price: " + tier)
		}
		if price < 0 {
			return errors.New("image resolution price must not be negative: " + tier)
		}
	}
	if len(prices) != 3 {
		return errors.New("image resolution prices only support 1k, 2k and 4k")
	}
	return nil
}

func UpdateImageGroupPriceByJSONString(jsonStr string) error {
	if err := CheckImageGroupPrice(jsonStr); err != nil {
		return err
	}
	return types.LoadFromJsonString(imageGroupPriceMap, jsonStr)
}
