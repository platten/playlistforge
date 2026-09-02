package playlist

import "strings"

// Same-music matching thresholds. Two playlists are "the same music" when their
// recordings overlap enough by ISRC or, when ISRC coverage is too thin to
// trust, by normalized title and artist. These are deliberately conservative;
// tune them against real libraries.
const (
	// SameMusicJaccard is |A∩B| / |A∪B|; at or above this the two are the same
	// regardless of length.
	SameMusicJaccard = 0.90
	// SameMusicCoverage and SameMusicCountRatio together catch a shorter list
	// that is almost entirely contained in a longer one of comparable size,
	// while still rejecting a small curated subset of a large library.
	SameMusicCoverage   = 0.90
	SameMusicCountRatio = 0.70
	// isrcMinCoverage is the fraction of tracks that must carry an ISRC on BOTH
	// sides before the ISRC comparison is trusted as the final verdict.
	isrcMinCoverage = 0.60
)

// MatchMethod records which signal decided a MatchResult.
type MatchMethod string

const (
	MatchNone       MatchMethod = ""
	MatchByISRC     MatchMethod = "isrc"
	MatchByMetadata MatchMethod = "title-artist"
)

// MatchResult is the outcome of comparing two tracklists.
type MatchResult struct {
	Matched bool
	// Score is the Jaccard similarity of the compared key sets (0..1).
	Score float64
	// Method is the signal that produced a positive match, or MatchNone.
	Method MatchMethod
}

// SameMusic reports whether two tracklists represent the same playlist.
//
// ISRC is preferred: when enough tracks on both sides carry one, the ISRC
// verdict is final — a re-master with entirely different ISRCs is intentionally
// not a match. When ISRC coverage is thin (a generated playlist rarely has
// any), it compares normalized title + artist keys instead. The result is
// symmetric in a and b.
func SameMusic(a, b []Track) MatchResult {
	if len(a) == 0 || len(b) == 0 {
		return MatchResult{}
	}
	if isrcCoverage(a) >= isrcMinCoverage && isrcCoverage(b) >= isrcMinCoverage {
		return compareKeySets(isrcSet(a), isrcSet(b), MatchByISRC)
	}
	return compareKeySets(metadataSet(a), metadataSet(b), MatchByMetadata)
}

// compareKeySets scores two non-empty key sets. SameMusic guarantees non-empty
// tracklists, and both set builders yield at least one key on the paths that
// reach here, so union and the ratio denominators are always positive.
func compareKeySets(a, b map[string]struct{}, method MatchMethod) MatchResult {
	intersection := 0
	for key := range a {
		if _, ok := b[key]; ok {
			intersection++
		}
	}
	union := len(a) + len(b) - intersection
	jaccard := float64(intersection) / float64(union)

	smaller, larger := len(a), len(b)
	if smaller > larger {
		smaller, larger = larger, smaller
	}
	coverage := float64(intersection) / float64(smaller)
	countRatio := float64(smaller) / float64(larger)

	matched := jaccard >= SameMusicJaccard ||
		(coverage >= SameMusicCoverage && countRatio >= SameMusicCountRatio)
	result := MatchResult{Matched: matched, Score: jaccard}
	if matched {
		result.Method = method
	}
	return result
}

func isrcCoverage(tracks []Track) float64 {
	withCode := 0
	for i := range tracks {
		if normalizeISRC(tracks[i].ISRC) != "" {
			withCode++
		}
	}
	return float64(withCode) / float64(len(tracks))
}

func isrcSet(tracks []Track) map[string]struct{} {
	set := make(map[string]struct{}, len(tracks))
	for i := range tracks {
		if code := normalizeISRC(tracks[i].ISRC); code != "" {
			set[code] = struct{}{}
		}
	}
	return set
}

func metadataSet(tracks []Track) map[string]struct{} {
	set := make(map[string]struct{}, len(tracks))
	for i := range tracks {
		set[NormalizedTrackKey(tracks[i])] = struct{}{}
	}
	return set
}

// normalizeISRC upper-cases and strips separators, returning "" for a missing
// or implausible code. A valid ISRC is 12 alphanumerics (CC-XXX-YY-NNNNN).
func normalizeISRC(raw *string) string {
	if raw == nil {
		return ""
	}
	var b strings.Builder
	for _, r := range strings.ToUpper(*raw) {
		if (r >= 'A' && r <= 'Z') || (r >= '0' && r <= '9') {
			b.WriteRune(r)
		}
	}
	code := b.String()
	if len(code) != 12 {
		return ""
	}
	return code
}
