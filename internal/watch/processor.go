package watch

import (
	"path/filepath"
	"time"

	"github.com/Cyberlane/vlc-media-watcher/internal/mediaparse"
	"github.com/Cyberlane/vlc-media-watcher/internal/vlc"
)

type Event struct {
	MediaPath string
	Progress  float64
	WatchedAt time.Time
	Status    string
	// Resolution is populated only after deterministic reconciliation. It lets
	// the Tracking UI link a watch outcome to its inspected library identity.
	Manager        string
	SourceID       int
	SeasonNumber   int
	EpisodeNumbers []int
}
type Processor struct {
	episodeThreshold, movieThreshold float64
	completed                        map[string]bool
}

func NewProcessor(episodeThreshold, movieThreshold float64) *Processor {
	return &Processor{episodeThreshold: episodeThreshold, movieThreshold: movieThreshold, completed: make(map[string]bool)}
}

func (p *Processor) Process(status vlc.Status, now time.Time) (Event, bool) {
	if status.MediaPath == "" || status.LengthSeconds <= 0 || status.Position <= 0 {
		return Event{}, false
	}
	threshold := p.episodeThreshold
	if isMovie(status.MediaPath) {
		threshold = p.movieThreshold
	}
	if status.Position < threshold || p.completed[status.MediaPath] {
		return Event{}, false
	}
	p.completed[status.MediaPath] = true
	return Event{MediaPath: status.MediaPath, Progress: status.Position, WatchedAt: now.UTC(), Status: "pending"}, true
}

func isMovie(path string) bool {
	return mediaparse.Parse(filepath.Base(path)).Kind != mediaparse.KindSeries
}
