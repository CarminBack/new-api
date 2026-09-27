package relay

import (
	"testing"

	"github.com/QuantumNous/new-api/relaykit/dto"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestResolveOutboundImageBillingUsesFinalSizeAndCount(t *testing.T) {
	originalCount := uint(1)
	request := &dto.ImageRequest{Model: "gpt-image", Size: "1K", N: &originalCount}

	resolved, count, err := resolveOutboundImageBilling(request, 1, []byte(`{"size":"4K","n":3}`))

	require.NoError(t, err)
	assert.Equal(t, "4K", resolved.Size)
	assert.Equal(t, 3, count)
	assert.Equal(t, "1K", request.Size, "the frozen ingress request must not be mutated")
}

func TestResolveOutboundImageBillingKeepsOriginalSizeWhenOutboundOmitsIt(t *testing.T) {
	request := &dto.ImageRequest{Model: "gpt-image", Size: "2K"}

	resolved, count, err := resolveOutboundImageBilling(request, 2, []byte(`{"prompt":"draw"}`))

	require.NoError(t, err)
	assert.Equal(t, "2K", resolved.Size)
	assert.Equal(t, 2, count)
}
