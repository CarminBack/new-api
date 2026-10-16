package codec

import (
	"io/fs"
	"os"
	"path/filepath"
	"testing"
)

func (c *Codec) encodeReference(t testing.TB, text string) []uint {
	var ids []uint
	match, err := c.splitRegexp.FindStringMatch(text)
	if err != nil {
		t.Fatal(err)
	}
	for match != nil {
		piece := match.String()
		if id, ok := c.vocabulary[piece]; ok {
			ids = append(ids, id)
		} else {
			parts := c.mergePairsReference(piece)
			for i := range len(parts) - 1 {
				ids = append(ids, c.vocabulary[piece[parts[i].offset:parts[i+1].offset]])
			}
		}
		if match, err = c.splitRegexp.FindNextMatch(match); err != nil {
			t.Fatal(err)
		}
	}
	return ids
}

// TOKENIZER_CORPUS=<dir> compares whole-file token IDs with the original merge.
func TestCorpusMatchesOriginal(t *testing.T) {
	root := os.Getenv("TOKENIZER_CORPUS")
	if root == "" {
		t.Skip("TOKENIZER_CORPUS not set")
	}
	codecs := []*Codec{NewCl100kBase(), NewO200kBase()}
	files, bytes := 0, 0
	filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return nil
		}
		info, _ := d.Info()
		if info == nil || info.Size() < 1024 || info.Size() > 2<<20 {
			return nil
		}
		data, err := os.ReadFile(path)
		if err != nil {
			return nil
		}
		text := string(data)
		for _, c := range codecs {
			got, _, err := c.Encode(text)
			if err != nil {
				t.Fatal(err)
			}
			want := c.encodeReference(t, text)
			if len(got) != len(want) {
				t.Fatalf("%s %s: %d tokens, want %d", c.name, path, len(got), len(want))
			}
			for i := range got {
				if got[i] != want[i] {
					t.Fatalf("%s %s: token %d = %d, want %d", c.name, path, i, got[i], want[i])
				}
			}
		}
		files++
		bytes += len(data)
		return nil
	})
	t.Logf("compared %d files, %.1f MiB", files, float64(bytes)/(1<<20))
}
