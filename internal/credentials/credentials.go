// Package credentials defines provider-neutral credential requirements and
// their safe resolution lifecycle. It contains no provider-specific I/O and
// never persists raw credential values.
package credentials

import (
	"fmt"
	"sort"
	"strings"
)

// ID is a stable integration-facing identifier. Provider adapters and user
// configuration bind an ID to a provider without teaching an integration how
// that provider stores credentials.
type ID string

const (
	VLCPasswordID  ID = "vlc.password"
	SonarrAPIKeyID ID = "sonarr.api-key"
	RadarrAPIKeyID ID = "radarr.api-key"
)

// TrackerAccessTokenID returns the stable ID for an app-owned tracker token.
func TrackerAccessTokenID(name string) ID {
	return ID("tracker." + normalizedName(name) + ".access-token")
}

// TrackerClientSecretID returns the stable ID for a user-stored OAuth client
// secret.
func TrackerClientSecretID(name string) ID {
	return ID("tracker." + normalizedName(name) + ".client-secret")
}

func normalizedName(name string) string {
	return strings.ToLower(strings.TrimSpace(name))
}

// Ownership distinguishes values supplied by the user from values acquired
// and written by the application, such as OAuth access tokens.
type Ownership string

const (
	UserStored Ownership = "user_stored"
	AppWritten Ownership = "app_written"
)

// Kind describes the lifecycle a credential value may have. RenewableToken
// makes future refresh support expressible even while automated refresh is out
// of scope for the first resilience implementation.
type Kind string

const (
	OpaqueSecret   Kind = "opaque_secret"
	RenewableToken Kind = "renewable_token"
)

// Requirement is what an integration needs, independent of where the value
// comes from. Values are intentionally not part of this type.
type Requirement struct {
	ID        ID
	Label     string
	Kind      Kind
	Ownership Ownership
	Required  bool
}

// Provider identifies the selected credential provider. Environment remains
// a compatibility source only; new onboarding will offer the two interactive
// providers first.
type Provider string

const (
	ProviderEnvironment Provider = "environment"
	ProviderKeychain    Provider = "keyring"
	Provider1Password   Provider = "1password"
)

// Capabilities describes a provider's supported operations. It lets future
// onboarding distinguish a read-only user-stored provider from a provider
// approved to receive app-written credentials.
type Capabilities struct {
	ResolveInCurrentProcess bool
	ForegroundAuthorization bool
	NativeProviderPrompt    bool
	SelectExistingItem      bool
	CreateOrUpdateItem      bool
	Headless                bool
}

// Binding selects a provider and gives it only its provider-specific locator.
// Locator and Environment are sensitive metadata and must never be emitted in
// incidents or logs.
type Binding struct {
	Provider    Provider
	Locator     string
	Environment string
}

// Entry joins an integration-facing requirement to its existing configuration
// binding during compatibility normalization.
type Entry struct {
	Requirement Requirement
	Binding     Binding
}

// Registry contains one provider-neutral entry per stable credential ID.
type Registry struct {
	entries map[ID]Entry
}

// NewRegistry validates and copies entries so callers cannot accidentally
// introduce duplicate or anonymous credential requirements.
func NewRegistry(entries ...Entry) (Registry, error) {
	registry := Registry{entries: make(map[ID]Entry, len(entries))}
	for _, entry := range entries {
		if strings.TrimSpace(string(entry.Requirement.ID)) == "" {
			return Registry{}, fmt.Errorf("credential requirement ID is required")
		}
		if strings.TrimSpace(entry.Requirement.Label) == "" {
			return Registry{}, fmt.Errorf("credential %q label is required", entry.Requirement.ID)
		}
		if entry.Requirement.Kind != OpaqueSecret && entry.Requirement.Kind != RenewableToken {
			return Registry{}, fmt.Errorf("credential %q has unsupported kind %q", entry.Requirement.ID, entry.Requirement.Kind)
		}
		if entry.Requirement.Ownership != UserStored && entry.Requirement.Ownership != AppWritten {
			return Registry{}, fmt.Errorf("credential %q has unsupported ownership %q", entry.Requirement.ID, entry.Requirement.Ownership)
		}
		if _, exists := registry.entries[entry.Requirement.ID]; exists {
			return Registry{}, fmt.Errorf("credential %q is registered more than once", entry.Requirement.ID)
		}
		registry.entries[entry.Requirement.ID] = entry
	}
	return registry, nil
}

// Entry returns one normalized binding by ID.
func (r Registry) Entry(id ID) (Entry, bool) {
	entry, ok := r.entries[id]
	return entry, ok
}

// Entries returns the registry in stable ID order for deterministic TUI and
// test consumers.
func (r Registry) Entries() []Entry {
	ids := make([]string, 0, len(r.entries))
	for id := range r.entries {
		ids = append(ids, string(id))
	}
	sort.Strings(ids)
	entries := make([]Entry, 0, len(ids))
	for _, id := range ids {
		entries = append(entries, r.entries[ID(id)])
	}
	return entries
}

// State is a provider-neutral, user-safe result classification. Recovered is
// deliberately a transition to Ready, not a second terminal state.
type State string

const (
	StateNotConfigured        State = "not_configured"
	StateReady                State = "ready"
	StateCredentialMissing    State = "credential_missing"
	StateNeedsUserAction      State = "needs_user_action"
	StateProviderUnavailable  State = "provider_unavailable"
	StateCredentialDenied     State = "credential_denied"
	StateCredentialInvalid    State = "credential_invalid"
	StateAPIAuthRejected      State = "api_auth_rejected"
	StateTemporaryUnavailable State = "temporary_unavailable"
)

// Resolution retains an ephemeral credential value only on a Ready result.
// SafeMessage is a user-facing string that never includes the provider
// locator, environment name, or provider error. Error is retained only for
// compatibility callers; incident surfaces must use SafeMessage instead.
type Resolution struct {
	State       State
	Value       string
	SafeMessage string
	Err         error
}

// Ready reports whether a resolution produced a usable in-memory value.
func (r Resolution) Ready() bool {
	return r.State == StateReady && r.Err == nil && r.Value != ""
}
