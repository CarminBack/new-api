package common

import (
	"encoding/json"
	"strings"

	"github.com/QuantumNous/new-api/relaykit/dto"
	"github.com/QuantumNous/new-api/setting/ratio_setting"
)

// ImageResolutionTier extracts an explicitly requested output image size from
// every image-capable ingress format used by the relay.
func ImageResolutionTier(request dto.Request) (string, bool) {
	var size string
	switch request := request.(type) {
	case *dto.ImageRequest:
		if request == nil {
			return "", false
		}
		size = request.Size
	case *dto.GeminiChatRequest:
		if request == nil || len(request.GenerationConfig.ImageConfig) == 0 {
			return "", false
		}
		var config struct {
			ImageSize      string `json:"imageSize"`
			ImageSizeSnake string `json:"image_size"`
		}
		if json.Unmarshal(request.GenerationConfig.ImageConfig, &config) != nil {
			return "", false
		}
		size = config.ImageSize
		if size == "" {
			size = config.ImageSizeSnake
		}
	case *dto.GeneralOpenAIRequest:
		if request == nil {
			return "", false
		}
		imageIntent := generalRequestHasImageModality(request)
		if len(request.ExtraBody) > 0 {
			var extra struct {
				Google struct {
					ImageConfig *struct {
						ImageSize      string `json:"imageSize"`
						ImageSizeSnake string `json:"image_size"`
					} `json:"image_config"`
				} `json:"google"`
			}
			if json.Unmarshal(request.ExtraBody, &extra) != nil {
				return "", false
			}
			if extra.Google.ImageConfig != nil {
				imageIntent = true
				size = extra.Google.ImageConfig.ImageSize
				if size == "" {
					size = extra.Google.ImageConfig.ImageSizeSnake
				}
			}
		}
		if strings.TrimSpace(size) == "" && imageIntent {
			size = request.Size
		}
		if !imageIntent {
			return "", false
		}
	default:
		return "", false
	}

	tier, valid := dto.ImageSizeTier(size)
	if !valid {
		// Explicit but unknown dimensions fail closed to the highest tier.
		tier = "4k"
	}
	return tier, true
}

func generalRequestHasImageModality(request *dto.GeneralOpenAIRequest) bool {
	if request == nil || len(request.Modalities) == 0 {
		return false
	}
	var modalities []string
	if json.Unmarshal(request.Modalities, &modalities) != nil {
		return false
	}
	for _, modality := range modalities {
		if strings.EqualFold(strings.TrimSpace(modality), "image") {
			return true
		}
	}
	return false
}

func ImageGroupResolutionRatio(request dto.Request, group string) (float64, bool) {
	if !strings.EqualFold(strings.TrimSpace(group), "image") {
		return 0, false
	}
	tier, ok := ImageResolutionTier(request)
	if !ok {
		return 0, false
	}
	return ratio_setting.GetImageGroupResolutionRatio(tier)
}
