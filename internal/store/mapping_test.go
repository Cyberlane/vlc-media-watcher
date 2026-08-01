package store

import (
	"path/filepath"
	"testing"

	"github.com/Cyberlane/vlc-media-watcher/internal/arr"
)

func TestIdentityCreatesSeparateAnimeSeasonMappingScopes(t *testing.T) {
	s, err := Open(filepath.Join(t.TempDir(), "watcher.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	units, err := s.UpsertIdentity(arr.MediaIdentity{Manager: arr.ManagerSonarr, SourceID: 42, Kind: "series", Title: "Example Anime", SeasonNumber: 2, TVDBID: 123})
	if err != nil {
		t.Fatal(err)
	}
	if len(units) != 2 || units[0].Scope != "series" || units[1].Scope != "season" || units[1].SeasonNumber != 2 {
		t.Fatalf("units = %#v", units)
	}
	if err := s.ConfirmMapping(TrackerMapping{MediaUnitID: units[1].ID, Tracker: "anilist", TrackerID: "999", TrackerTitle: "Example Anime Season 2"}); err != nil {
		t.Fatal(err)
	}
	mappings, err := s.MappingsForUnit(units[1].ID)
	if err != nil || len(mappings) != 1 || mappings[0].TrackerID != "999" {
		t.Fatalf("mappings = %#v, %v", mappings, err)
	}
	if mappings, err = s.MappingsForUnit(units[0].ID); err != nil || len(mappings) != 0 {
		t.Fatalf("series mappings = %#v, %v", mappings, err)
	}
	unit, err := s.MediaUnit("sonarr", 42, "season", 2)
	if err != nil || unit.ID != units[1].ID {
		t.Fatalf("season media unit = %#v, %v", unit, err)
	}
	if _, err := s.MediaUnit("sonarr", 42, "season", 1); err == nil {
		t.Fatal("expected missing season to be rejected")
	}
	if err := s.DeleteMapping(units[1].ID, "anilist"); err != nil {
		t.Fatal(err)
	}
	if mappings, err := s.MappingsForUnit(units[1].ID); err != nil || len(mappings) != 0 {
		t.Fatalf("mappings after deletion = %#v, %v", mappings, err)
	}
}
