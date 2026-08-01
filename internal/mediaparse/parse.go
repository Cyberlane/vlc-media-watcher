// Package mediaparse extracts conservative, reviewable media evidence from a
// filename. It deliberately does not claim that a filename is an identity.
package mediaparse

import (
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"time"
	"unicode"
)

type Kind string

const (
	KindUnknown Kind = "unknown"
	KindMovie   Kind = "movie"
	KindSeries  Kind = "series"
)

// Confidence describes confidence in the filename syntax only. Even Strong
// results require a user to review and confirm a tracker candidate.
type Confidence string

const (
	ConfidenceNone      Confidence = "none"
	ConfidenceTentative Confidence = "tentative"
	ConfidenceStrong    Confidence = "strong"
)

// Result is the structured evidence available from a name. Pattern is kept
// for diagnostics and regression tests, not as an identity key.
type Result struct {
	Kind           Kind
	Confidence     Confidence
	Title          string
	Year           int
	Season         int
	Episodes       []int
	AbsoluteNumber int
	AirDate        string
	Pattern        string
}

func (r Result) Trackable() bool {
	return r.Kind != KindUnknown && r.Confidence != ConfidenceNone && r.Title != ""
}

// IdentityKey intentionally uses only stable parsed facts. It is used to
// group local provisional identities, never to assert a tracker match.
func (r Result) IdentityKey() string {
	if !r.Trackable() {
		return ""
	}
	return string(r.Kind) + "|" + normalizeKey(r.Title) + "|" + strconv.Itoa(r.Year)
}

var (
	seasonEpisodePattern = regexp.MustCompile(`(?i)^(?P<title>.+?)[ ._-]+s(?P<season>\d{1,2})[ ._-]*e(?P<episodes>\d{1,3}(?:(?:[ ._-]*e|[ ._-]*-)\d{1,3})*)(?:[ ._-].*|$)`)
	xEpisodePattern      = regexp.MustCompile(`(?i)^(?P<title>.+?)[ ._-]+(?P<season>\d{1,2})x(?P<episodes>\d{1,3}(?:(?:[ ._-]*x|[ ._-]*-)\d{1,3})*)(?:[ ._-].*|$)`)
	seasonPackPattern    = regexp.MustCompile(`(?i)^(?P<title>.+?)[ ._-]+s(?P<season>\d{1,2})(?:[ ._-].*|$)`)
	seasonWordPattern    = regexp.MustCompile(`(?i)^(?P<title>.+?)[ ._-]+season[ ._-]*(?P<season>\d{1,2})(?:[ ._-].*|$)`)
	dailyPattern         = regexp.MustCompile(`(?i)^(?P<title>.+?)[ ._-]+(?P<year>19\d{2}|20\d{2})[ ._-](?P<month>0[1-9]|1[0-2])[ ._-](?P<day>0[1-9]|[12]\d|3[01])\b`)
	animeNumberPattern   = regexp.MustCompile(`(?i)^(?:\[[^\]]+\][ ._-]*)?(?P<title>.+?)[ ._-]+(?:\[)?(?P<episode>\d{1,3})(?:v\d+)?(?:\])?(?:[ ._-].*|$)`)
	movieYearPattern     = regexp.MustCompile(`(?i)^(?P<title>.+)[ ._\-(\[]+(?P<year>18\d{2}|19\d{2}|20\d{2})(?:[\]) ._-]|$)`)
	numberPattern        = regexp.MustCompile(`\d+`)
	leadingGroupPattern  = regexp.MustCompile(`^\s*\[[^\]]+\]\s*`)
	trailingYearPattern  = regexp.MustCompile(`(?i)^(?P<title>.+?)[ ._\-(\[]+(?P<year>18\d{2}|19\d{2}|20\d{2})(?:[\]) ._-]|$)`)
)

// Parse recognizes the release-name forms that have enough structure to be
// useful as a tracker-search hint. Unknown is a successful, deliberate
// result for names that do not carry enough information.
func Parse(path string) Result {
	name := strings.TrimSpace(filepath.Base(path))
	if extension := filepath.Ext(name); extension != "" {
		name = strings.TrimSuffix(name, extension)
	}
	name = strings.ReplaceAll(strings.ReplaceAll(name, "【", "["), "】", "]")
	if name == "" {
		return Result{Kind: KindUnknown, Confidence: ConfidenceNone}
	}

	if match := seasonEpisodePattern.FindStringSubmatch(name); match != nil {
		return seriesResult(match, seasonEpisodePattern, "season_episode", ConfidenceStrong)
	}
	if match := xEpisodePattern.FindStringSubmatch(name); match != nil {
		return seriesResult(match, xEpisodePattern, "x_episode", ConfidenceStrong)
	}
	if match := seasonPackPattern.FindStringSubmatch(name); match != nil {
		return seasonPackResult(match, seasonPackPattern, "season_pack")
	}
	if match := seasonWordPattern.FindStringSubmatch(name); match != nil {
		return seasonPackResult(match, seasonWordPattern, "season_pack")
	}
	if match := dailyPattern.FindStringSubmatch(name); match != nil {
		return dailyResult(match)
	}
	if match := animeNumberPattern.FindStringSubmatch(name); match != nil {
		title := cleanTitle(group(match, animeNumberPattern, "title"))
		episode, _ := strconv.Atoi(group(match, animeNumberPattern, "episode"))
		if usableTitle(title) && episode > 0 {
			return Result{Kind: KindSeries, Confidence: ConfidenceTentative, Title: title, Season: 1, Episodes: []int{episode}, AbsoluteNumber: episode, Pattern: "absolute_episode"}
		}
	}
	if match := movieYearPattern.FindStringSubmatch(name); match != nil {
		title := cleanTitle(group(match, movieYearPattern, "title"))
		year, _ := strconv.Atoi(group(match, movieYearPattern, "year"))
		if usableTitle(title) {
			return Result{Kind: KindMovie, Confidence: ConfidenceTentative, Title: title, Year: year, Pattern: "movie_year"}
		}
	}

	return Result{Kind: KindUnknown, Confidence: ConfidenceNone}
}

func seriesResult(match []string, pattern *regexp.Regexp, name string, confidence Confidence) Result {
	title := cleanTitle(group(match, pattern, "title"))
	season, _ := strconv.Atoi(group(match, pattern, "season"))
	episodes := parseNumbers(group(match, pattern, "episodes"))
	if !usableTitle(title) || season < 0 || len(episodes) == 0 {
		return Result{Kind: KindUnknown, Confidence: ConfidenceNone}
	}
	title, year := splitTrailingYear(title)
	return Result{Kind: KindSeries, Confidence: confidence, Title: title, Year: year, Season: season, Episodes: episodes, Pattern: name}
}

func seasonPackResult(match []string, pattern *regexp.Regexp, name string) Result {
	title := cleanTitle(group(match, pattern, "title"))
	season, _ := strconv.Atoi(group(match, pattern, "season"))
	if !usableTitle(title) || season <= 0 {
		return Result{Kind: KindUnknown, Confidence: ConfidenceNone}
	}
	title, year := splitTrailingYear(title)
	return Result{Kind: KindSeries, Confidence: ConfidenceStrong, Title: title, Year: year, Season: season, Pattern: name}
}

func dailyResult(match []string) Result {
	title := cleanTitle(group(match, dailyPattern, "title"))
	year, _ := strconv.Atoi(group(match, dailyPattern, "year"))
	month, _ := strconv.Atoi(group(match, dailyPattern, "month"))
	day, _ := strconv.Atoi(group(match, dailyPattern, "day"))
	if !usableTitle(title) || time.Date(year, time.Month(month), day, 0, 0, 0, 0, time.UTC).Day() != day {
		return Result{Kind: KindUnknown, Confidence: ConfidenceNone}
	}
	return Result{Kind: KindSeries, Confidence: ConfidenceStrong, Title: title, AirDate: time.Date(year, time.Month(month), day, 0, 0, 0, 0, time.UTC).Format("2006-01-02"), Pattern: "daily_date"}
}

func group(match []string, pattern *regexp.Regexp, name string) string {
	index := pattern.SubexpIndex(name)
	if index < 0 || index >= len(match) {
		return ""
	}
	return match[index]
}

func parseNumbers(value string) []int {
	values := numberPattern.FindAllString(value, -1)
	result := make([]int, 0, len(values))
	seen := make(map[int]struct{}, len(values))
	for _, value := range values {
		number, _ := strconv.Atoi(value)
		if number <= 0 {
			continue
		}
		if _, exists := seen[number]; !exists {
			seen[number] = struct{}{}
			result = append(result, number)
		}
	}
	return result
}

func cleanTitle(value string) string {
	value = leadingGroupPattern.ReplaceAllString(strings.TrimSpace(value), "")
	value = strings.ReplaceAll(value, ".", " ")
	value = strings.ReplaceAll(value, "_", " ")
	value = strings.Trim(value, " -[]()")
	return strings.Join(strings.Fields(value), " ")
}

func splitTrailingYear(title string) (string, int) {
	match := trailingYearPattern.FindStringSubmatch(title)
	if match == nil {
		return title, 0
	}
	trimmed := cleanTitle(group(match, trailingYearPattern, "title"))
	year, _ := strconv.Atoi(group(match, trailingYearPattern, "year"))
	if !usableTitle(trimmed) {
		return title, 0
	}
	return trimmed, year
}

func usableTitle(title string) bool {
	switch strings.ToLower(strings.TrimSpace(title)) {
	case "episode", "ep", "video", "movie", "unknown", "untitled":
		return false
	}
	for _, r := range title {
		if unicode.IsLetter(r) {
			return true
		}
	}
	return false
}

func normalizeKey(value string) string {
	var b strings.Builder
	for _, r := range strings.ToLower(value) {
		if unicode.IsLetter(r) || unicode.IsDigit(r) {
			b.WriteRune(r)
		}
	}
	return b.String()
}
