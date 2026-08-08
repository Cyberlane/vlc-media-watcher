package secrets

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"strings"

	"github.com/zalando/go-keyring"

	"github.com/Cyberlane/vlc-media-watcher/internal/config"
	"github.com/Cyberlane/vlc-media-watcher/internal/credentials"
)

const KeyringService = "vlc-media-watcher"

// Keyring provides the small read/write seam needed by credential resolution.
// Its implementation is deliberately injected so tests never contact a real
// user keychain.
type Keyring interface {
	Get(service, reference string) (string, error)
	Set(service, reference, value string) error
}

type systemKeyring struct{}

func (systemKeyring) Get(service, reference string) (string, error) {
	return keyring.Get(service, reference)
}

func (systemKeyring) Set(service, reference, value string) error {
	return keyring.Set(service, reference, value)
}

// CommandRunner isolates provider subprocess execution from resolver policy.
// It is intentionally narrower than os/exec so fake providers can prove
// bounded and classified failures without invoking 1Password.
type CommandRunner interface {
	Output(ctx context.Context, name string, args ...string) ([]byte, error)
}

type systemCommandRunner struct{}

func (systemCommandRunner) Output(ctx context.Context, name string, args ...string) ([]byte, error) {
	return exec.CommandContext(ctx, name, args...).Output()
}

// Dependencies controls the provider adapters used by a Resolver. Nil fields
// select the production implementation.
type Dependencies struct {
	Environment func(string) string
	Keyring     Keyring
	Command     CommandRunner
}

// Resolver resolves provider-neutral credential bindings. It keeps values only
// in the returned Resolution, so callers decide their process-lifetime cache.
type Resolver struct {
	environment func(string) string
	keyring     Keyring
	command     CommandRunner
}

// NewResolver constructs a resolver with injectable provider adapters.
func NewResolver(dependencies Dependencies) Resolver {
	if dependencies.Environment == nil {
		dependencies.Environment = os.Getenv
	}
	if dependencies.Keyring == nil {
		dependencies.Keyring = systemKeyring{}
	}
	if dependencies.Command == nil {
		dependencies.Command = systemCommandRunner{}
	}
	return Resolver{
		environment: dependencies.Environment,
		keyring:     dependencies.Keyring,
		command:     dependencies.Command,
	}
}

// ResolveVLC resolves the core VLC credential through the provider-neutral
// contract. Callers that run in the background should supply a bounded
// context; providers must never hold the watcher startup indefinitely.
func ResolveVLC(ctx context.Context, cfg config.VLCConfig) credentials.Resolution {
	return NewResolver(Dependencies{}).Resolve(ctx,
		credentials.Requirement{ID: credentials.VLCPasswordID, Label: "VLC password", Kind: credentials.OpaqueSecret, Ownership: credentials.UserStored, Required: true},
		credentials.Binding{Provider: credentials.Provider(cfg.SecretSource), Locator: cfg.SecretReference, Environment: cfg.PasswordEnv},
	)
}

func Resolve(ctx context.Context, cfg config.VLCConfig) (string, error) {
	resolution := ResolveVLC(ctx, cfg)
	return resolution.Value, resolution.Err
}

// ResolveValue resolves a named secret without ever placing the secret itself in
// configuration, logs, or error messages.
func ResolveValue(ctx context.Context, label, source, reference, environment string) (string, error) {
	resolution := NewResolver(Dependencies{}).Resolve(ctx,
		credentials.Requirement{ID: credentials.ID("legacy.value"), Label: label, Kind: credentials.OpaqueSecret, Ownership: credentials.UserStored},
		credentials.Binding{Provider: credentials.Provider(source), Locator: reference, Environment: environment},
	)
	return resolution.Value, resolution.Err
}

// Resolve resolves one provider-neutral request and returns a typed state for
// future service/TUI incident handling. Existing callers can keep using
// ResolveValue while later slices adopt the state directly.
func (r Resolver) Resolve(ctx context.Context, requirement credentials.Requirement, binding credentials.Binding) credentials.Resolution {
	switch binding.Provider {
	case "":
		return failed(credentials.StateNotConfigured, safeMessage(requirement.Label, "is not configured"), fmt.Errorf("unsupported %s secret source %q", requirement.Label, binding.Provider))
	case credentials.ProviderEnvironment:
		value := r.environment(binding.Environment)
		if value == "" {
			return failed(credentials.StateCredentialMissing, safeMessage(requirement.Label, "is not available"), fmt.Errorf("%s is not set in $%s", requirement.Label, binding.Environment))
		}
		return ready(value)
	case credentials.ProviderKeychain:
		value, err := r.getKeyringValue(ctx, binding.Locator)
		if err != nil {
			state := classifyKeyringError(ctx, err)
			return failed(state, safeMessage(requirement.Label, safeMessageSuffix(state)), fmt.Errorf("read %s from the system keyring: %w", requirement.Label, err))
		}
		return ready(value)
	case credentials.Provider1Password:
		output, err := r.command.Output(ctx, "op", "read", binding.Locator)
		if err != nil {
			state := classify1PasswordError(ctx, err)
			return failed(state, safeMessage(requirement.Label, safeMessageSuffix(state)), fmt.Errorf("read %s from 1Password: %w", requirement.Label, err))
		}
		value := strings.TrimSpace(string(output))
		if value == "" {
			return failed(credentials.StateCredentialInvalid, safeMessage(requirement.Label, "is invalid"), fmt.Errorf("1Password returned an empty %s", requirement.Label))
		}
		return ready(value)
	default:
		return failed(credentials.StateCredentialInvalid, safeMessage(requirement.Label, "uses an unsupported provider"), fmt.Errorf("unsupported %s secret source %q", requirement.Label, binding.Provider))
	}
}

type keyringResult struct {
	value string
	err   error
}

// getKeyringValue bounds a keychain operation even though the third-party
// keyring package does not accept a context. A stalled platform call may
// finish in the background, but it cannot stall credential resolution or the
// watcher process.
func (r Resolver) getKeyringValue(ctx context.Context, locator string) (string, error) {
	results := make(chan keyringResult, 1)
	go func() {
		value, err := r.keyring.Get(KeyringService, locator)
		results <- keyringResult{value: value, err: err}
	}()
	select {
	case result := <-results:
		return result.value, result.err
	case <-ctx.Done():
		return "", ctx.Err()
	}
}

func ready(value string) credentials.Resolution {
	return credentials.Resolution{State: credentials.StateReady, Value: value, SafeMessage: "Ready"}
}

func failed(state credentials.State, message string, err error) credentials.Resolution {
	return credentials.Resolution{State: state, SafeMessage: message, Err: err}
}

func classifyKeyringError(ctx context.Context, err error) credentials.State {
	if errors.Is(ctx.Err(), context.DeadlineExceeded) || errors.Is(err, context.DeadlineExceeded) {
		return credentials.StateProviderUnavailable
	}
	if errors.Is(err, keyring.ErrNotFound) {
		return credentials.StateCredentialMissing
	}
	return credentials.StateNeedsUserAction
}

func safeMessage(label, suffix string) string {
	return label + " " + suffix + "."
}

func safeMessageSuffix(state credentials.State) string {
	switch state {
	case credentials.StateCredentialMissing:
		return "is not available"
	case credentials.StateProviderUnavailable:
		return "provider is unavailable"
	case credentials.StateCredentialDenied:
		return "access was denied"
	case credentials.StateNeedsUserAction:
		return "needs user action"
	default:
		return "could not be resolved"
	}
}

func classify1PasswordError(ctx context.Context, err error) credentials.State {
	if errors.Is(ctx.Err(), context.DeadlineExceeded) || errors.Is(err, context.DeadlineExceeded) {
		return credentials.StateProviderUnavailable
	}
	return credentials.StateNeedsUserAction
}

func StoreInKeyring(reference, secret string) error {
	if secret == "" {
		return fmt.Errorf("secret must not be empty")
	}
	if reference == "" {
		return fmt.Errorf("keyring reference must not be empty")
	}
	return systemKeyring{}.Set(KeyringService, reference, secret)
}
