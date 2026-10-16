package codec

import (
	"fmt"
	"math"
	"strings"
	"sync"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestMergePairsPreservesTokens(t *testing.T) {
	for _, c := range []*Codec{NewCl100kBase(), NewO200kBase(), NewP50kBase(), NewR50kBase()} {
		t.Run(c.name, func(t *testing.T) {
			for _, text := range []string{"", "hello", "hello hello world", strings.Repeat("a", 257), strings.Repeat("a", 4096), strings.Repeat("abcABCxyzXYZ", 100), strings.Repeat("这是中文测试", 80), strings.Repeat("\x00\xff\x80", 100), strings.Repeat("0123456789", 100), strings.Repeat("😀⚡", 100), strings.Repeat("a", 255) + "b" + strings.Repeat("a", 257)} {
				got := c.mergePairs(text)
				want := c.mergePairsReference(text)
				gotOffsets := make([]int, len(got))
				wantOffsets := make([]int, len(want))
				for i := range got {
					gotOffsets[i] = got[i].offset
				}
				for i := range want {
					wantOffsets[i] = want[i].offset
				}
				require.Equal(t, wantOffsets, gotOffsets)
			}
		})
	}
}

func TestEncodeMatchesOriginalBPE(t *testing.T) {
	for _, c := range []*Codec{NewCl100kBase(), NewO200kBase(), NewP50kBase(), NewR50kBase(), NewP50kEdit()} {
		t.Run(c.name, func(t *testing.T) {
			texts := []string{
				"hello 世界 😀\n12345",
				strings.Repeat("a", 4096),
				strings.Repeat("这是一段连续中文", 100),
				strings.Repeat("abcdefghABCDEFGHIJKLMNOPQRSTUVWXYZ", 40),
				strings.Repeat(" ", 1024) + "\n" + strings.Repeat("!", 1024),
			}
			for _, text := range texts {
				var wantIDs []uint
				var wantTokens []string
				match, err := c.splitRegexp.FindStringMatch(text)
				require.NoError(t, err)
				for match != nil {
					piece := match.String()
					if id, ok := c.vocabulary[piece]; ok {
						wantIDs = append(wantIDs, id)
						wantTokens = append(wantTokens, piece)
					} else {
						parts := c.mergePairsReference(piece)
						for i := range len(parts) - 1 {
							token := piece[parts[i].offset:parts[i+1].offset]
							wantIDs = append(wantIDs, c.vocabulary[token])
							wantTokens = append(wantTokens, token)
						}
					}
					match, err = c.splitRegexp.FindNextMatch(match)
					require.NoError(t, err)
				}
				ids, tokens, err := c.Encode(text)
				require.NoError(t, err)
				require.Equal(t, wantIDs, ids)
				require.Equal(t, wantTokens, tokens)
				count, err := c.Count(text)
				require.NoError(t, err)
				require.Equal(t, len(wantIDs), count)
			}
		})
	}
}

func TestSharedCodecConcurrentLongText(t *testing.T) {
	c := NewCl100kBase()
	text := strings.Repeat("a", 4096)
	ids, _, err := c.Encode(text)
	require.NoError(t, err)
	var workers sync.WaitGroup
	errors := make(chan error, 4)
	for range 4 {
		workers.Add(1)
		go func() {
			defer workers.Done()
			count, err := c.Count(text)
			if err != nil {
				errors <- err
				return
			}
			if count != len(ids) {
				errors <- fmt.Errorf("got %d tokens, want %d", count, len(ids))
			}
		}()
	}
	workers.Wait()
	close(errors)
	for err := range errors {
		require.NoError(t, err)
	}
}

func (c *Codec) mergePairsReference(piece string) []part {
	parts := make([]part, len(piece)+1)
	for i := range len(parts) {
		parts[i] = part{i, math.MaxUint}
	}

	getRank := func(index, skip int) uint {
		if index+skip+2 < len(parts) {
			start := parts[index].offset
			end := parts[index+skip+2].offset
			if rank, ok := c.vocabulary[piece[start:end]]; ok {
				return rank
			}
		}
		return math.MaxUint
	}

	for i := 0; i < len(parts)-2; i++ {
		parts[i].rank = getRank(i, 0)
	}

	for {
		if len(parts) == 1 {
			break
		}

		minRank := uint(math.MaxUint)
		minIndex := 0
		for i, p := range parts[:len(parts)-1] {
			if p.rank < minRank {
				minRank = p.rank
				minIndex = i
			}
		}

		if minRank == math.MaxUint {
			break
		}

		parts[minIndex].rank = getRank(minIndex, 1)

		if minIndex > 0 {
			parts[minIndex-1].rank = getRank(minIndex-1, 1)
		}

		parts = append(parts[:minIndex+1], parts[minIndex+2:]...)
	}

	return parts
}

func BenchmarkLongPiece(b *testing.B) {
	c := NewCl100kBase()
	for _, n := range []int{16384, 65536, 262144} {
		s := strings.Repeat("a", n)
		b.Run(stringSize(n), func(b *testing.B) {
			b.SetBytes(int64(n))
			b.ReportAllocs()
			for i := 0; i < b.N; i++ {
				_, err := c.Count(s)
				if err != nil {
					b.Fatal(err)
				}
			}
		})
	}
}
func stringSize(n int) string {
	if n == 16384 {
		return "16KiB"
	}
	if n == 65536 {
		return "64KiB"
	}
	return "256KiB"
}
