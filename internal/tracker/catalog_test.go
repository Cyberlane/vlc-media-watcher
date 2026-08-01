package tracker

import "testing"

func TestCatalogHasSeasonAwareAnimeTrackers(t *testing.T) {
	for _, service := range []Service{AniList, AniDB, MyAnimeList} {
		definition, ok := Lookup(string(service))
		if !ok || definition.MappingScope != "season" {
			t.Fatalf("%s definition = %#v, found=%t", service, definition, ok)
		}
	}
}

func TestSeasonOnlyServicesRejectSeriesScopeAndAniDBUsesCanonicalURL(t *testing.T) {
	definition, ok := Lookup(string(AniDB))
	if !ok || definition.SupportsUnit("series", "series") || !definition.SupportsUnit("season", "series") {
		t.Fatalf("AniDB scope policy = %#v", definition)
	}
	link, err := URLForID(AniDB, "12345")
	if err != nil || link != "https://anidb.net/a12345" {
		t.Fatalf("AniDB URL = %q, %v", link, err)
	}
	if _, err := URLForID(AniDB, "not-an-aid"); err == nil {
		t.Fatal("expected invalid AniDB AID to be rejected")
	}
}
