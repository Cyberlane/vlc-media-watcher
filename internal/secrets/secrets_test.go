package secrets

import (
	"context"
	"testing"

	"github.com/Cyberlane/vlc-media-watcher/internal/config"
)

func TestResolveEnvironment(t *testing.T) {
	t.Setenv("VLC_MEDIA_WATCHER_TEST_PASSWORD", "test-password")
	secret, err := Resolve(context.Background(), config.VLCConfig{SecretSource: "environment", PasswordEnv: "VLC_MEDIA_WATCHER_TEST_PASSWORD"})
	if err != nil || secret != "test-password" {
		t.Fatalf("Resolve() = %q, %v", secret, err)
	}
}

func TestResolveRejectsUnknownSource(t *testing.T) {
	if _, err := Resolve(context.Background(), config.VLCConfig{SecretSource: "unknown"}); err == nil {
		t.Fatal("expected unsupported source error")
	}
}

func TestResolveValueFromEnvironment(t *testing.T) {
	t.Setenv("VLC_MEDIA_WATCHER_TEST_API_KEY", "api-key")
	secret, err := ResolveValue(context.Background(), "Sonarr API key", "environment", "", "VLC_MEDIA_WATCHER_TEST_API_KEY")
	if err != nil || secret != "api-key" {
		t.Fatalf("ResolveValue() = %q, %v", secret, err)
	}
}
