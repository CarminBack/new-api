package common

import "testing"

func TestImageGenerationModelRecognition(t *testing.T) {
	for _, modelName := range []string{"gpt-image-1", "gpt-image-2", "dall-e-3", "imagen-4"} {
		if !IsImageGenerationModel(modelName) {
			t.Fatalf("expected %q to be recognized as an image generation model", modelName)
		}
	}
	if IsImageGenerationModel("gpt-5.6") {
		t.Fatal("text model was recognized as an image generation model")
	}
}
