package codec

import (
	"math/rand"
	"runtime"
	"strings"
	"sync"
	"testing"
)

func offsetsOf(parts []part) []int {
	out := make([]int, len(parts))
	for i, p := range parts {
		out[i] = p.offset
	}
	return out
}

func requireSameMerge(t testing.TB, c *Codec, piece string) {
	got, want := offsetsOf(c.mergePairs(piece)), offsetsOf(c.mergePairsReference(piece))
	if len(got) != len(want) {
		t.Fatalf("%s: len %d != %d for %q", c.name, len(got), len(want), piece)
	}
	for i := range got {
		if got[i] != want[i] {
			t.Fatalf("%s: offset[%d] %d != %d for %q", c.name, i, got[i], want[i], piece)
		}
	}
}

var fuzzAlphabets = []string{
	"ab", "abc ", "aAbB01", "the quick brown fox ", "这是中文测试一二三", "😀⚡🎉", "\x00\x80\xff\n\t",
	"{\"key\":[1,2]}", "-_=+/\\", "ثقافة", "日本語テキスト",
}

func randomPiece(r *rand.Rand, n int) string {
	var b strings.Builder
	for b.Len() < n {
		alpha := fuzzAlphabets[r.Intn(len(fuzzAlphabets))]
		runes := []rune(alpha)
		for k := r.Intn(64) + 1; k > 0 && b.Len() < n; k-- {
			if r.Intn(8) == 0 {
				b.WriteByte(byte(r.Intn(256)))
			} else {
				b.WriteRune(runes[r.Intn(len(runes))])
			}
		}
	}
	return b.String()
}

func allCodecs() []*Codec {
	return []*Codec{NewCl100kBase(), NewO200kBase(), NewP50kBase(), NewR50kBase(), NewP50kEdit()}
}

// Randomized differential test against the original O(n^2) merge.
func TestMergePairsRandomDifferential(t *testing.T) {
	r := rand.New(rand.NewSource(20261016))
	iters := 300
	if testing.Short() {
		iters = 30
	}
	for _, c := range allCodecs() {
		for i := 0; i < iters; i++ {
			n := 200 + r.Intn(3000) // straddles the 256-byte threshold
			requireSameMerge(t, c, randomPiece(r, n))
		}
	}
}

func FuzzMergePairs(f *testing.F) {
	for _, s := range []string{strings.Repeat("a", 300), strings.Repeat("ab", 200), strings.Repeat("中文", 100), strings.Repeat("😀", 80)} {
		f.Add(s)
	}
	codecs := allCodecs()
	f.Fuzz(func(t *testing.T, s string) {
		if len(s) < 257 {
			s = strings.Repeat(s+"x", 257/(len(s)+1)+1)
		}
		if len(s) > 1<<14 {
			s = s[:1<<14]
		}
		for _, c := range codecs {
			requireSameMerge(t, c, s)
		}
	})
}

// Measures peak heap under concurrent long-piece tokenization.
func TestConcurrentLongPieceMemory(t *testing.T) {
	if testing.Short() {
		t.Skip()
	}
	c := NewCl100kBase()
	text := strings.Repeat("a", 256<<10)
	for _, workers := range []int{1, 4, 16} {
		runtime.GC()
		var before runtime.MemStats
		runtime.ReadMemStats(&before)
		var peak uint64
		stop := make(chan struct{})
		var sampler sync.WaitGroup
		sampler.Add(1)
		go func() {
			defer sampler.Done()
			var m runtime.MemStats
			for {
				select {
				case <-stop:
					return
				default:
					runtime.ReadMemStats(&m)
					if m.HeapInuse > peak {
						peak = m.HeapInuse
					}
					runtime.Gosched()
				}
			}
		}()
		var wg sync.WaitGroup
		for range workers {
			wg.Add(1)
			go func() {
				defer wg.Done()
				for range 4 {
					if _, err := c.Count(text); err != nil {
						t.Error(err)
					}
				}
			}()
		}
		wg.Wait()
		close(stop)
		sampler.Wait()
		var after runtime.MemStats
		runtime.ReadMemStats(&after)
		t.Logf("workers=%d peakHeapInuse=+%.1fMiB numGC=%d", workers, float64(peak-before.HeapInuse)/(1<<20), after.NumGC-before.NumGC)
	}
}
