package credentials

import "testing"

func TestRegistryReturnsEntriesInStableOrder(t *testing.T) {
	registry, err := NewRegistry(
		Entry{Requirement: Requirement{ID: SonarrAPIKeyID, Label: "Sonarr API key", Kind: OpaqueSecret, Ownership: UserStored}},
		Entry{Requirement: Requirement{ID: VLCPasswordID, Label: "VLC password", Kind: OpaqueSecret, Ownership: UserStored, Required: true}},
	)
	if err != nil {
		t.Fatal(err)
	}

	entries := registry.Entries()
	if len(entries) != 2 || entries[0].Requirement.ID != SonarrAPIKeyID || entries[1].Requirement.ID != VLCPasswordID {
		t.Fatalf("Entries() = %#v", entries)
	}
}

func TestRegistryRejectsDuplicateOrInvalidRequirements(t *testing.T) {
	valid := Entry{Requirement: Requirement{ID: VLCPasswordID, Label: "VLC password", Kind: OpaqueSecret, Ownership: UserStored}}
	if _, err := NewRegistry(valid, valid); err == nil {
		t.Fatal("expected duplicate requirement error")
	}
	if _, err := NewRegistry(Entry{Requirement: Requirement{Label: "VLC password", Kind: OpaqueSecret, Ownership: UserStored}}); err == nil {
		t.Fatal("expected missing ID error")
	}
	if _, err := NewRegistry(Entry{Requirement: Requirement{ID: VLCPasswordID, Label: "VLC password", Ownership: UserStored}}); err == nil {
		t.Fatal("expected missing kind error")
	}
}

func TestTrackerCredentialIDsAreStable(t *testing.T) {
	if got, want := TrackerAccessTokenID("AniList"), ID("tracker.anilist.access-token"); got != want {
		t.Fatalf("TrackerAccessTokenID() = %q, want %q", got, want)
	}
	if got, want := TrackerClientSecretID("AniList"), ID("tracker.anilist.client-secret"); got != want {
		t.Fatalf("TrackerClientSecretID() = %q, want %q", got, want)
	}
}
