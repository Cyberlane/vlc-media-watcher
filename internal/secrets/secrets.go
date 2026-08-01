package secrets

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"strings"

	"github.com/zalando/go-keyring"

	"github.com/Cyberlane/vlc-media-watcher/internal/config"
)

const KeyringService = "vlc-media-watcher"

func Resolve(ctx context.Context, cfg config.VLCConfig) (string, error) {
	return ResolveValue(ctx, "VLC password", cfg.SecretSource, cfg.SecretReference, cfg.PasswordEnv)
}

// ResolveValue resolves a named secret without ever placing the secret itself in
// configuration, logs, or error messages.
func ResolveValue(ctx context.Context, label, source, reference, environment string) (string, error) {
	switch source {
	case "environment":
		value := os.Getenv(environment)
		if value == "" {
			return "", fmt.Errorf("%s is not set in $%s", label, environment)
		}
		return value, nil
	case "keyring":
		value, err := keyring.Get(KeyringService, reference)
		if err != nil {
			return "", fmt.Errorf("read %s from the system keyring: %w", label, err)
		}
		return value, nil
	case "1password":
		output, err := exec.CommandContext(ctx, "op", "read", reference).Output()
		if err != nil {
			return "", fmt.Errorf("read %s from 1Password: %w", label, err)
		}
		value := strings.TrimSpace(string(output))
		if value == "" {
			return "", fmt.Errorf("1Password returned an empty %s", label)
		}
		return value, nil
	default:
		return "", fmt.Errorf("unsupported %s secret source %q", label, source)
	}
}

func StoreInKeyring(reference, secret string) error {
	if secret == "" {
		return fmt.Errorf("secret must not be empty")
	}
	if reference == "" {
		return fmt.Errorf("keyring reference must not be empty")
	}
	return keyring.Set(KeyringService, reference, secret)
}
