package mediaparse

import (
	"reflect"
	"testing"
)

func TestParseCorpus(t *testing.T) {
	tests := []struct {
		name string
		path string
		want Result
	}{
		{"standard episode", "/tv/The.Show/Season 02/The.Show.S02E03.1080p.WEB-DL.mkv", Result{Kind: KindSeries, Confidence: ConfidenceStrong, Title: "The Show", Season: 2, Episodes: []int{3}, Pattern: "season_episode"}},
		{"multi episode", "The.Show.S01E01-E02.mkv", Result{Kind: KindSeries, Confidence: ConfidenceStrong, Title: "The Show", Season: 1, Episodes: []int{1, 2}, Pattern: "season_episode"}},
		{"compact multi episode", "The.Show.S01E01E02.mkv", Result{Kind: KindSeries, Confidence: ConfidenceStrong, Title: "The Show", Season: 1, Episodes: []int{1, 2}, Pattern: "season_episode"}},
		{"x style", "The_Show_3x07_720p.mkv", Result{Kind: KindSeries, Confidence: ConfidenceStrong, Title: "The Show", Season: 3, Episodes: []int{7}, Pattern: "x_episode"}},
		{"season pack", "The.Show.S02.Complete.1080p.mkv", Result{Kind: KindSeries, Confidence: ConfidenceStrong, Title: "The Show", Season: 2, Pattern: "season_pack"}},
		{"season word pack", "The Show - Season 02 - Complete.mkv", Result{Kind: KindSeries, Confidence: ConfidenceStrong, Title: "The Show", Season: 2, Pattern: "season_pack"}},
		{"show year disambiguator", "The.Show.(2024).S01E01.mkv", Result{Kind: KindSeries, Confidence: ConfidenceStrong, Title: "The Show", Year: 2024, Season: 1, Episodes: []int{1}, Pattern: "season_episode"}},
		{"specials", "The.Show.S00E03.Behind.the.Scenes.mkv", Result{Kind: KindSeries, Confidence: ConfidenceStrong, Title: "The Show", Season: 0, Episodes: []int{3}, Pattern: "season_episode"}},
		{"daily show", "The.Daily.Show.2026.07.23.1080p.mkv", Result{Kind: KindSeries, Confidence: ConfidenceStrong, Title: "The Daily Show", AirDate: "2026-07-23", Pattern: "daily_date"}},
		{"anime absolute", "[SubsPlease] Frieren - 12 (1080p) [ABC12345].mkv", Result{Kind: KindSeries, Confidence: ConfidenceTentative, Title: "Frieren", Season: 1, Episodes: []int{12}, AbsoluteNumber: 12, Pattern: "absolute_episode"}},
		{"anime plain absolute", "Delicious in Dungeon - 01.mkv", Result{Kind: KindSeries, Confidence: ConfidenceTentative, Title: "Delicious in Dungeon", Season: 1, Episodes: []int{1}, AbsoluteNumber: 1, Pattern: "absolute_episode"}},
		{"movie", "Dune.Part.Two.2024.2160p.UHD.BluRay.mkv", Result{Kind: KindMovie, Confidence: ConfidenceTentative, Title: "Dune Part Two", Year: 2024, Pattern: "movie_year"}},
		{"movie with brackets", "The.Matrix.(1999).mkv", Result{Kind: KindMovie, Confidence: ConfidenceTentative, Title: "The Matrix", Year: 1999, Pattern: "movie_year"}},
		{"movie with a long numeric suffix", "Blade.Runner.2049.2017.1080p.mkv", Result{Kind: KindMovie, Confidence: ConfidenceTentative, Title: "Blade Runner 2049", Year: 2017, Pattern: "movie_year"}},
		{"ambiguous number", "Episode 01.mkv", Result{Kind: KindUnknown, Confidence: ConfidenceNone}},
		{"bare number", "01.mkv", Result{Kind: KindUnknown, Confidence: ConfidenceNone}},
		{"invalid daily date", "The.Daily.Show.2026.02.30.mkv", Result{Kind: KindUnknown, Confidence: ConfidenceNone}},
		{"quality only", "1080p.WEB-DL.mkv", Result{Kind: KindUnknown, Confidence: ConfidenceNone}},
		{"non media", "password.yenc", Result{Kind: KindUnknown, Confidence: ConfidenceNone}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := Parse(tt.path)
			if got.Kind != tt.want.Kind || got.Confidence != tt.want.Confidence || got.Title != tt.want.Title || got.Year != tt.want.Year || got.Season != tt.want.Season || got.AbsoluteNumber != tt.want.AbsoluteNumber || got.AirDate != tt.want.AirDate || got.Pattern != tt.want.Pattern || !reflect.DeepEqual(got.Episodes, tt.want.Episodes) {
				t.Fatalf("Parse(%q) = %#v, want %#v", tt.path, got, tt.want)
			}
		})
	}
}

func TestIdentityKeyGroupsEpisodesButNotMovieYears(t *testing.T) {
	first := Parse("Example.Show.S01E01.mkv")
	second := Parse("Example.Show.S02E04.mkv")
	if first.IdentityKey() != second.IdentityKey() {
		t.Fatalf("series keys differ: %q and %q", first.IdentityKey(), second.IdentityKey())
	}
	firstMovie := Parse("Example.Movie.1999.mkv")
	secondMovie := Parse("Example.Movie.2024.mkv")
	if firstMovie.IdentityKey() == secondMovie.IdentityKey() {
		t.Fatalf("movie keys unexpectedly match: %q", firstMovie.IdentityKey())
	}
}
