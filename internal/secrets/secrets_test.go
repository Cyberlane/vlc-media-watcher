package secrets

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/Cyberlane/vlc-media-watcher/internal/config"
	"github.com/Cyberlane/vlc-media-watcher/internal/credentials"
	"github.com/zalando/go-keyring"
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

func TestResolverClassifiesProviderResultsWithoutLiveCredentialAccess(t *testing.T) {
	tests := []struct {
		name      string
		binding   credentials.Binding
		env       func(string) string
		keyring   Keyring
		command   CommandRunner
		wantState credentials.State
		wantValue string
	}{
		{
			name:      "environment ready",
			binding:   credentials.Binding{Provider: credentials.ProviderEnvironment, Environment: "TEST_SECRET"},
			env:       func(string) string { return "value" },
			wantState: credentials.StateReady,
			wantValue: "value",
		},
		{
			name:      "environment missing",
			binding:   credentials.Binding{Provider: credentials.ProviderEnvironment, Environment: "TEST_SECRET"},
			env:       func(string) string { return "" },
			wantState: credentials.StateCredentialMissing,
		},
		{
			name:      "keychain item missing",
			binding:   credentials.Binding{Provider: credentials.ProviderKeychain, Locator: "test/secret"},
			keyring:   fakeKeyring{getErr: keyring.ErrNotFound},
			wantState: credentials.StateCredentialMissing,
		},
		{
			name:      "1password needs user action",
			binding:   credentials.Binding{Provider: credentials.Provider1Password, Locator: "op://redacted"},
			command:   fakeCommand{err: errors.New("provider locked")},
			wantState: credentials.StateNeedsUserAction,
		},
		{
			name:      "1password timeout is unavailable",
			binding:   credentials.Binding{Provider: credentials.Provider1Password, Locator: "op://redacted"},
			command:   fakeCommand{err: context.DeadlineExceeded},
			wantState: credentials.StateProviderUnavailable,
		},
	}

	request := credentials.Requirement{ID: credentials.VLCPasswordID, Label: "VLC password", Kind: credentials.OpaqueSecret, Ownership: credentials.UserStored, Required: true}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			resolver := NewResolver(Dependencies{Environment: tt.env, Keyring: tt.keyring, Command: tt.command})
			result := resolver.Resolve(context.Background(), request, tt.binding)
			if result.State != tt.wantState {
				t.Fatalf("state = %q, want %q (error %v)", result.State, tt.wantState, result.Err)
			}
			if result.Value != tt.wantValue {
				t.Fatalf("value = %q, want %q", result.Value, tt.wantValue)
			}
			if tt.wantState == credentials.StateReady && !result.Ready() {
				t.Fatalf("Ready() = false for %#v", result)
			}
			if tt.binding.Locator != "" && strings.Contains(result.SafeMessage, tt.binding.Locator) {
				t.Fatalf("SafeMessage leaked locator: %q", result.SafeMessage)
			}
			if tt.binding.Environment != "" && strings.Contains(result.SafeMessage, tt.binding.Environment) {
				t.Fatalf("SafeMessage leaked environment name: %q", result.SafeMessage)
			}
		})
	}
}

func TestResolverBoundsAStalledKeychainRead(t *testing.T) {
	block := make(chan struct{})
	resolver := NewResolver(Dependencies{Keyring: blockingKeyring{block: block}})
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
	defer cancel()

	result := resolver.Resolve(ctx,
		credentials.Requirement{ID: credentials.VLCPasswordID, Label: "VLC password", Kind: credentials.OpaqueSecret, Ownership: credentials.UserStored, Required: true},
		credentials.Binding{Provider: credentials.ProviderKeychain, Locator: "test/secret"},
	)
	if result.State != credentials.StateProviderUnavailable {
		t.Fatalf("state = %q, want provider_unavailable (error %v)", result.State, result.Err)
	}
	close(block)
}

func TestResolverClassifiesAContextBound1PasswordCommandAsUnavailable(t *testing.T) {
	resolver := NewResolver(Dependencies{Command: commandRunnerFunc(func(ctx context.Context, _ string, _ ...string) ([]byte, error) {
		<-ctx.Done()
		return nil, ctx.Err()
	})})
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
	defer cancel()

	result := resolver.Resolve(ctx,
		credentials.Requirement{ID: credentials.VLCPasswordID, Label: "VLC password", Kind: credentials.OpaqueSecret, Ownership: credentials.UserStored, Required: true},
		credentials.Binding{Provider: credentials.Provider1Password, Locator: "op://test/secret"},
	)
	if result.State != credentials.StateProviderUnavailable {
		t.Fatalf("state = %q, want provider_unavailable (error %v)", result.State, result.Err)
	}
}

type fakeKeyring struct {
	value  string
	getErr error
}

func (f fakeKeyring) Get(_, _ string) (string, error) { return f.value, f.getErr }
func (fakeKeyring) Set(_, _, _ string) error          { return nil }

type fakeCommand struct {
	output []byte
	err    error
}

func (f fakeCommand) Output(_ context.Context, _ string, _ ...string) ([]byte, error) {
	return f.output, f.err
}

type commandRunnerFunc func(context.Context, string, ...string) ([]byte, error)

func (f commandRunnerFunc) Output(ctx context.Context, name string, args ...string) ([]byte, error) {
	return f(ctx, name, args...)
}

type blockingKeyring struct {
	block <-chan struct{}
}

func (f blockingKeyring) Get(_, _ string) (string, error) {
	<-f.block
	return "", nil
}

func (blockingKeyring) Set(_, _, _ string) error { return nil }
