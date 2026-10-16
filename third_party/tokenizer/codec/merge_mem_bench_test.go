package codec

import (
	"strings"
	"testing"
)

func BenchmarkMergeOnly64KiB(b *testing.B) {
	c := NewCl100kBase()
	s := strings.Repeat("a", 64<<10)
	b.Run("new", func(b *testing.B) {
		b.ReportAllocs()
		for i := 0; i < b.N; i++ {
			c.mergePairs(s)
		}
	})
	b.Run("reference", func(b *testing.B) {
		b.ReportAllocs()
		for i := 0; i < b.N; i++ {
			c.mergePairsReference(s)
		}
	})
	b.Run("countNew", func(b *testing.B) {
		b.ReportAllocs()
		for i := 0; i < b.N; i++ {
			c.Count(s)
		}
	})
}
