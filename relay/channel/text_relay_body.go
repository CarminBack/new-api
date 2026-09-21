package channel

import (
	"io"
	"sync"
)

// Keep the request context alive through body consumption, not just headers.
type textRelayResponseBody struct {
	io.ReadCloser
	once    sync.Once
	release func()
}

func (b *textRelayResponseBody) Close() error {
	b.once.Do(b.release)
	return b.ReadCloser.Close()
}
