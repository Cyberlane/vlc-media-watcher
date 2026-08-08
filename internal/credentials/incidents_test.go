package credentials

import (
	"fmt"
	"strings"
	"testing"
)

type testHTTPStatusError int

func (e testHTTPStatusError) Error() string       { return fmt.Sprintf("HTTP %d", e) }
func (e testHTTPStatusError) HTTPStatusCode() int { return int(e) }

func TestClassifyAPIErrorSeparatesAuthenticationAndTemporaryFailures(t *testing.T) {
	if state := ClassifyAPIError(testHTTPStatusError(401)); state != StateAPIAuthRejected {
		t.Fatalf("401 state = %q", state)
	}
	if state := ClassifyAPIError(testHTTPStatusError(403)); state != StateAPIAuthRejected {
		t.Fatalf("403 state = %q", state)
	}
	if state := ClassifyAPIError(testHTTPStatusError(429)); state != StateTemporaryUnavailable {
		t.Fatalf("429 state = %q", state)
	}
}

func TestIncidentTrackerDeduplicatesAndRecordsOneRecovery(t *testing.T) {
	var tracker IncidentTracker
	first, err := tracker.Observe("watcher", VLCPasswordID, StateProviderUnavailable)
	if err != nil || first.Kind != IncidentOpened {
		t.Fatalf("first event = %#v, err=%v", first, err)
	}
	duplicate, err := tracker.Observe("watcher", VLCPasswordID, StateProviderUnavailable)
	if err != nil || !duplicate.Empty() {
		t.Fatalf("duplicate event = %#v, err=%v", duplicate, err)
	}
	changed, err := tracker.Observe("watcher", VLCPasswordID, StateNeedsUserAction)
	if err != nil || changed.Kind != IncidentUpdated {
		t.Fatalf("changed event = %#v, err=%v", changed, err)
	}
	recovered, err := tracker.Observe("watcher", VLCPasswordID, StateReady)
	if err != nil || recovered.Kind != IncidentRecovered {
		t.Fatalf("recovery event = %#v, err=%v", recovered, err)
	}
	again, err := tracker.Observe("watcher", VLCPasswordID, StateReady)
	if err != nil || !again.Empty() {
		t.Fatalf("duplicate recovery = %#v, err=%v", again, err)
	}
}

func TestIncidentDetailNeverIncludesProviderMetadata(t *testing.T) {
	detail := IncidentDetail(TrackerClientSecretID("anilist"), StateCredentialDenied)
	for _, forbidden := range []string{"op://", "Private", "VLC_MEDIA_WATCHER", "token"} {
		if strings.Contains(detail, forbidden) {
			t.Fatalf("incident detail leaked %q: %q", forbidden, detail)
		}
	}
	if !strings.Contains(detail, "AniList") || !strings.Contains(detail, "denied") {
		t.Fatalf("incident detail = %q", detail)
	}
}
