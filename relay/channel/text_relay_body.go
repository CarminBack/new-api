package channel

import (
	"io"
	"sync"
)

// Keep the request context alive through body consumption, not just headers.
type textRelayResponseBody struct {
	io.ReadCloser
	once      sync.Once
	release   func()
	firstByte func()
	firstOnce sync.Once
}

func (b *textRelayResponseBody) Read(p []byte) (int, error) {
	n, err := b.ReadCloser.Read(p)
	if n > 0 && b.firstByte != nil {
		b.firstOnce.Do(b.firstByte)
	}
	return n, err
}

func (b *textRelayResponseBody) Close() error {
	b.once.Do(b.release)
	return b.ReadCloser.Close()
}
