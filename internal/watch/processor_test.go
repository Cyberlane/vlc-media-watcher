package watch

import (
	"testing"
	"time"

	"github.com/Cyberlane/vlc-media-watcher/internal/vlc"
)

func TestProcessorUsesEpisodeThresholdAndDeduplicates(t *testing.T) {
	p := NewProcessor(.9, .85)
	status := vlc.Status{MediaPath: "/media/Show.S01E01.mkv", Position: .90, LengthSeconds: 1200}
	event, ok := p.Process(status, time.Date(2026, 7, 19, 0, 0, 0, 0, time.UTC))
	if !ok || event.Status != "pending" {
		t.Fatalf("event = %#v, ok = %t", event, ok)
	}
	if _, ok := p.Process(status, time.Now()); ok {
		t.Fatal("duplicate event was accepted")
	}
}

func TestProcessorUsesMovieThreshold(t *testing.T) {
	p := NewProcessor(.9, .85)
	if _, ok := p.Process(vlc.Status{MediaPath: "/media/Movie.mkv", Position: .86, LengthSeconds: 7200}, time.Now()); !ok {
		t.Fatal("movie should meet movie threshold")
	}
}

func TestProcessorUsesEpisodeThresholdForSeasonPacks(t *testing.T) {
	p := NewProcessor(.9, .85)
	if _, ok := p.Process(vlc.Status{MediaPath: "/media/Show.S01.Complete.mkv", Position: .86, LengthSeconds: 7200}, time.Now()); ok {
		t.Fatal("season pack should require the episode threshold")
	}
}
