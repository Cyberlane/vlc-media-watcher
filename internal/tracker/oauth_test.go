package tracker

import (
	"context"
	"net/http"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/Cyberlane/vlc-media-watcher/internal/config"
)

func TestAuthorizationURLUsesStateAndPKCEForPublicDesktopTrackers(t *testing.T) {
	for _, service := range []Service{MyAnimeList, SIMKL} {
		link, err := authorizationURL(service, "client", "state", "verifier")
		if err != nil {
			t.Fatalf("authorizationURL(%s): %v", service, err)
		}
		parsed, err := url.Parse(link)
		if err != nil {
			t.Fatal(err)
		}
		values := parsed.Query()
		if values.Get("state") != "state" || values.Get("redirect_uri") != CallbackURL || values.Get("code_challenge") != pkceChallenge("verifier") || values.Get("code_challenge_method") != "S256" {
			t.Fatalf("%s authorization values = %v", service, values)
		}
	}
}

func TestWaitForCodeAcceptsOnlyTheMatchingState(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	code, err := waitForCode(ctx, "expected", "https://provider.example/authorize", func(string) error {
		go func() {
			response, requestErr := http.Get(CallbackURL + "?state=expected&code=authorization-code")
			if requestErr == nil {
				response.Body.Close()
			}
		}()
		return nil
	})
	if err != nil || code != "authorization-code" {
		t.Fatalf("waitForCode = %q, %v", code, err)
	}
}

func TestWaitForCodeRejectsMismatchedState(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	_, err := waitForCode(ctx, "expected", "https://provider.example/authorize", func(string) error {
		go func() {
			response, requestErr := http.Get(CallbackURL + "?state=wrong&code=authorization-code")
			if requestErr == nil {
				response.Body.Close()
			}
		}()
		return nil
	})
	if err == nil || !strings.Contains(err.Error(), "state did not match") {
		t.Fatalf("waitForCode error = %v", err)
	}
}

func TestLinkFailsSafelyBeforeOpeningBrowserWhenClientSecretIsUnavailable(t *testing.T) {
	const missingEnvironment = "VLC_MEDIA_WATCHER_TEST_MISSING_ANILIST_CLIENT_SECRET"
	opened := false
	_, err := Link(context.Background(), AniList, config.TrackerConfig{
		ClientID:           "test-client-id",
		ClientSecretSource: "environment",
		ClientSecretEnv:    missingEnvironment,
	}, func(string) error {
		opened = true
		return nil
	})
	if err == nil {
		t.Fatal("Link() error = nil, want unavailable client-secret error")
	}
	if opened {
		t.Fatal("Link() opened the browser after a credential failure")
	}
	if !strings.Contains(err.Error(), "AniList OAuth client secret is not available") {
		t.Fatalf("Link() error = %q", err)
	}
	if strings.Contains(err.Error(), missingEnvironment) {
		t.Fatalf("Link() error leaked environment name: %q", err)
	}
}
