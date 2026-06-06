package mapping

import "strings"

// monbooru rating levels, ordered general < sensitive < questionable <
// explicit.
const (
	RatingGeneral      = "general"
	RatingSensitive    = "sensitive"
	RatingQuestionable = "questionable"
	RatingExplicit     = "explicit"
)

// ratingRanks orders the levels for the strictest-wins pool bundle rule.
var ratingRanks = map[string]int{
	RatingGeneral:      0,
	RatingSensitive:    1,
	RatingQuestionable: 2,
	RatingExplicit:     3,
}

// RatingRank returns the severity rank of a level, or -1 for the unset /
// unknown level. The cbz bundle takes the max rank across a pool's posts.
func RatingRank(level string) int {
	if r, ok := ratingRanks[level]; ok {
		return r
	}
	return -1
}

// Stricter returns whichever of a, b is the stricter (higher-rank) level. An
// unset level loses to any set one.
func Stricter(a, b string) string {
	if RatingRank(b) > RatingRank(a) {
		return b
	}
	return a
}

// mapRating normalizes a booru rating value to a monbooru level, tolerant of
// letter and full-word forms (case-insensitive). The single letter `s` is the
// overload the per-family branch resolves: sensitive on Danbooru, safe elsewhere.
// An unrecognized value returns "" (rating left unset, which monbooru treats as
// visible under every ceiling).
func mapRating(family, raw string) string {
	v := strings.ToLower(strings.TrimSpace(raw))
	if v == "" {
		return ""
	}
	// Full-word forms are unambiguous across every family.
	switch v {
	case "general":
		return RatingGeneral
	case "sensitive":
		return RatingSensitive
	case "questionable":
		return RatingQuestionable
	case "explicit":
		return RatingExplicit
	case "safe":
		return RatingGeneral
	case "suggestive":
		return RatingSensitive // philomena and mangadex term for the sensitive tier
	}
	// Single letters: q and e are stable; g is general; s is the overload.
	switch v {
	case "q":
		return RatingQuestionable
	case "e":
		return RatingExplicit
	case "g":
		return RatingGeneral
	case "s":
		if family == FamilyDanbooru {
			return RatingSensitive
		}
		return RatingGeneral // e621, moebooru, gelbooru_v02, generic: s = safe
	}
	return ""
}

// philomenaRatingTag reports whether tag is one of philomena's mutually
// exclusive rating tags. Philomena boorus (derpibooru, twibooru, ...) carry no
// rating field: the rating is a tag, lifted to the rating and dropped from the
// content tags.
func philomenaRatingTag(tag string) bool {
	switch strings.ToLower(tag) {
	case "safe", "suggestive", "questionable", "explicit":
		return true
	}
	return false
}

// philomenaRating returns the rating tag carried in a philomena booru's flat tag
// list, or "" when none is present. It feeds the normal rating resolution so
// user and profile overrides still apply.
func philomenaRating(meta map[string]any) string {
	for _, t := range parseTagField(meta["tags"]) {
		if philomenaRatingTag(t) {
			return strings.ToLower(t)
		}
	}
	return ""
}
