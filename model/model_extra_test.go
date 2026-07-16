package model

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestVideoGroupDetection(t *testing.T) {
	assert.True(t, hasVideoGroup([]string{"default", "Video"}))
	assert.True(t, hasVideoGroup([]string{" video "}))
	assert.False(t, hasVideoGroup([]string{"default", "Image"}))
}
