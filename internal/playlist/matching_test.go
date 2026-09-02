package playlist

// Tests for SameMusic: identical playlists match (by ISRC when present, by
// title/artist when not), a near-identical playlist matches, a small subset of
// a large library does not, a re-master with different ISRCs does not (the ISRC
// verdict is final), unrelated playlists do not, and an empty side never
// matches. Also checks symmetry and that a malformed ISRC is ignored.

import (
	"fmt"
	"testing"
)

func ptr(s string) *string { return &s }

// trk builds a track. isrc "" means no code.
func trk(title, artist, isrc string) Track {
	t := Track{Title: title, Artists: []string{artist}}
	if isrc != "" {
		t.ISRC = ptr(isrc)
	}
	return t
}

// seq builds n tracks with sequential titles/artists and, when withISRC, a
// unique valid ISRC per track: 2-char country, 3-char registrant, 2-char year,
// 5-digit designation = 12 alphanumerics.
func seq(prefix string, n int, withISRC bool) []Track {
	registrant := (prefix + "00")[:3]
	out := make([]Track, n)
	for i := 0; i < n; i++ {
		code := ""
		if withISRC {
			code = fmt.Sprintf("US%s24%05d", registrant, i)
		}
		out[i] = trk(fmt.Sprintf("%s song %d", prefix, i), prefix+" artist", code)
	}
	return out
}

func TestSameMusic(t *testing.T) {
	base := seq("A", 20, true)
	baseNoISRC := seq("A", 20, false)

	t.Run("identical by ISRC", func(t *testing.T) {
		clone := seq("A", 20, true)
		got := SameMusic(base, clone)
		if !got.Matched || got.Method != MatchByISRC {
			t.Fatalf("got %+v", got)
		}
	})

	t.Run("identical music, one side without ISRC", func(t *testing.T) {
		got := SameMusic(base, baseNoISRC)
		if !got.Matched || got.Method != MatchByMetadata {
			t.Fatalf("got %+v", got)
		}
	})

	t.Run("near identical still matches", func(t *testing.T) {
		near := seq("A", 20, true)
		near[19] = trk("A different song", "A artist", "USA9924_ZZZZZ")
		if got := SameMusic(base, near); !got.Matched {
			t.Fatalf("got %+v", got)
		}
	})

	t.Run("small subset of a large library does not match", func(t *testing.T) {
		big := seq("A", 60, true)
		small := big[:12]
		if got := SameMusic(small, big); got.Matched {
			t.Fatalf("subset matched: %+v", got)
		}
	})

	t.Run("re-master with different ISRCs does not match", func(t *testing.T) {
		remaster := make([]Track, 20)
		for i := range base {
			remaster[i] = trk(base[i].Title, base[i].Artists[0], fmt.Sprintf("GBRMS24%05d", i))
		}
		got := SameMusic(base, remaster)
		if got.Matched {
			t.Fatalf("re-master matched despite disjoint ISRCs: %+v", got)
		}
	})

	t.Run("unrelated playlists do not match", func(t *testing.T) {
		if got := SameMusic(base, seq("Z", 18, true)); got.Matched {
			t.Fatalf("got %+v", got)
		}
	})

	t.Run("empty side never matches", func(t *testing.T) {
		if SameMusic(nil, base).Matched || SameMusic(base, nil).Matched {
			t.Fatal("empty matched")
		}
	})

	t.Run("malformed ISRCs fall back to metadata", func(t *testing.T) {
		bad := seq("A", 20, false)
		for i := range bad {
			bad[i].ISRC = ptr("not-an-isrc")
		}
		got := SameMusic(baseNoISRC, bad)
		if !got.Matched || got.Method != MatchByMetadata {
			t.Fatalf("got %+v", got)
		}
	})

	t.Run("symmetric", func(t *testing.T) {
		other := seq("A", 22, true)
		if SameMusic(base, other).Matched != SameMusic(other, base).Matched {
			t.Fatal("asymmetric result")
		}
	})
}
