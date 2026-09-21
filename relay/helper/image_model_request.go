package helper

import (
	"fmt"
	"strings"

	relayconstant "github.com/QuantumNous/new-api/relay/constant"
	"github.com/QuantumNous/new-api/relaykit/dto"
	"github.com/tidwall/gjson"
)

var governedGPTImageModels = map[string]struct{}{
	"gpt-image-2":            {},
	"gpt-image-2.5":          {},
	"gpt-image-2-exact":      {},
	"gpt-image-2.5-flare":    {},
	"gpt-image-2.5-sunburst": {},
}

var governedGeminiImageModels = map[string]struct{}{
	"gemini-3-pro-image":             {},
	"gemini-3-pro-image-preview":     {},
	"gemini-3.1-flash-image":         {},
	"gemini-3.1-flash-image-preview": {},
}

func IsGovernedImageModel(model string) bool {
	model = strings.ToLower(strings.TrimSpace(model))
	if _, ok := governedGPTImageModels[model]; ok {
		return true
	}
	_, ok := governedGeminiImageModels[model]
	return ok
}

// ValidateImageModelRequest keeps fixed-price image models on request paths
// where image intent and billing parameters are explicit.
func ValidateImageModelRequest(model string, relayMode int, body []byte) error {
	model = strings.ToLower(strings.TrimSpace(model))
	if _, ok := governedGPTImageModels[model]; ok {
		if relayMode != relayconstant.RelayModeImagesGenerations && relayMode != relayconstant.RelayModeImagesEdits {
			return fmt.Errorf("model %s must be called through /v1/images/generations or /v1/images/edits so generated images can be counted, billed, and archived", model)
		}
		return nil
	}

	if _, ok := governedGeminiImageModels[model]; !ok {
		return nil
	}
	if relayMode != relayconstant.RelayModeChatCompletions && relayMode != relayconstant.RelayModeGemini {
		return fmt.Errorf("model %s is only supported through an explicit image-generation request", model)
	}
	if !requestsImageOutput(body) {
		return fmt.Errorf("model %s requires an explicit image output modality", model)
	}
	if !hasExplicitSupportedImageResolution(body) {
		return fmt.Errorf("model %s requires an explicit image resolution of 1K, 2K, or 4K", model)
	}
	return nil
}

func requestsImageOutput(body []byte) bool {
	paths := []string{
		"modalities",
		"response_modalities",
		"generationConfig.responseModalities",
		"generationConfig.response_modalities",
		"generation_config.responseModalities",
		"generation_config.response_modalities",
		"extra_body.google.response_modalities",
	}
	for _, path := range paths {
		result := gjson.GetBytes(body, path)
		for _, modality := range result.Array() {
			if strings.EqualFold(strings.TrimSpace(modality.String()), "image") {
				return true
			}
		}
	}
	return false
}

func hasExplicitSupportedImageResolution(body []byte) bool {
	paths := []string{
		"size",
		"image_size",
		"imageSize",
		"generationConfig.imageConfig.imageSize",
		"generationConfig.imageConfig.image_size",
		"generation_config.image_config.imageSize",
		"generation_config.image_config.image_size",
		"extra_body.google.image_config.image_size",
		"extra_body.google.image_config.imageSize",
	}
	for _, path := range paths {
		value := strings.ToLower(strings.TrimSpace(gjson.GetBytes(body, path).String()))
		if value == "1k" || value == "2k" || value == "4k" {
			return true
		}
		if value != "" && value != "auto" {
			if _, ok := dto.ImageSizeTier(value); ok {
				return true
			}
		}
	}
	return false
}
