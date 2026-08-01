package tracker

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"os/exec"
	"runtime"
	"strings"
	"time"

	"github.com/Cyberlane/vlc-media-watcher/internal/config"
	"github.com/Cyberlane/vlc-media-watcher/internal/secrets"
)

// CallbackURL is intentionally fixed so a user can register it once in each
// personal OAuth application. The listener accepts only loopback traffic and
// runs only while the account-link operation is active.
const CallbackURL = "http://127.0.0.1:8789/callback"

type LinkResult struct {
	AccessToken string
}

// Link opens the provider consent page, receives the browser callback on the
// local loopback interface, exchanges the authorization code, and returns the
// access token. The caller is responsible for writing that token to its secret
// store; it is never placed in a URL, config file, log, or database.
func Link(ctx context.Context, service Service, cfg config.TrackerConfig, openURL func(string) error) (LinkResult, error) {
	if strings.TrimSpace(cfg.ClientID) == "" {
		return LinkResult{}, fmt.Errorf("set the %s application client ID before linking", service)
	}
	if openURL == nil {
		openURL = OpenBrowser
	}

	state, err := randomURLValue(32)
	if err != nil {
		return LinkResult{}, err
	}
	pkce := service == MyAnimeList || service == SIMKL
	verifier := ""
	if pkce {
		verifier, err = randomURLValue(48)
		if err != nil {
			return LinkResult{}, err
		}
	}
	clientSecret := ""
	if service == AniList || service == Trakt {
		clientSecret, err = secrets.ResolveValue(ctx, fmt.Sprintf("%s OAuth client secret", title(service)), cfg.ClientSecretSource, cfg.ClientSecretReference, cfg.ClientSecretEnv)
		if err != nil {
			return LinkResult{}, err
		}
	}

	authorizeURL, err := authorizationURL(service, cfg.ClientID, state, verifier)
	if err != nil {
		return LinkResult{}, err
	}
	code, err := waitForCode(ctx, state, authorizeURL, openURL)
	if err != nil {
		return LinkResult{}, err
	}
	token, err := exchangeCode(ctx, service, cfg.ClientID, clientSecret, code, verifier)
	if err != nil {
		return LinkResult{}, err
	}
	if token == "" {
		return LinkResult{}, fmt.Errorf("%s did not return an access token", title(service))
	}
	return LinkResult{AccessToken: token}, nil
}

func authorizationURL(service Service, clientID, state, verifier string) (string, error) {
	endpoint := ""
	values := url.Values{"client_id": {clientID}, "redirect_uri": {CallbackURL}, "response_type": {"code"}, "state": {state}}
	switch service {
	case AniList:
		endpoint = "https://anilist.co/api/v2/oauth/authorize"
	case MyAnimeList:
		endpoint = "https://myanimelist.net/v1/oauth2/authorize"
		values.Set("code_challenge", pkceChallenge(verifier))
		values.Set("code_challenge_method", "S256")
	case Trakt:
		endpoint = "https://trakt.tv/oauth/authorize"
	case SIMKL:
		endpoint = "https://api.simkl.com/oauth/authorize"
		values.Set("code_challenge", pkceChallenge(verifier))
		values.Set("code_challenge_method", "S256")
	default:
		return "", fmt.Errorf("unsupported tracker %q", service)
	}
	return endpoint + "?" + values.Encode(), nil
}

func waitForCode(ctx context.Context, state, authorizeURL string, openURL func(string) error) (string, error) {
	listener, err := net.Listen("tcp", "127.0.0.1:8789")
	if err != nil {
		return "", fmt.Errorf("start OAuth callback listener on 127.0.0.1:8789: %w", err)
	}
	defer listener.Close()
	type callback struct{ code, err string }
	result := make(chan callback, 1)
	server := &http.Server{ReadHeaderTimeout: 10 * time.Second}
	server.Handler = http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		if request.URL.Path != "/callback" {
			http.NotFound(writer, request)
			return
		}
		if request.Method != http.MethodGet {
			writer.WriteHeader(http.StatusMethodNotAllowed)
			return
		}
		if request.URL.Query().Get("state") != state {
			result <- callback{err: "OAuth callback state did not match"}
			writer.WriteHeader(http.StatusBadRequest)
			_, _ = io.WriteString(writer, "Authorization could not be verified. You can close this window.")
			return
		}
		if providerError := request.URL.Query().Get("error"); providerError != "" {
			result <- callback{err: "authorization was declined or failed: " + providerError}
			_, _ = io.WriteString(writer, "Authorization was not completed. You can close this window.")
			return
		}
		code := request.URL.Query().Get("code")
		if code == "" {
			result <- callback{err: "OAuth callback did not include an authorization code"}
			writer.WriteHeader(http.StatusBadRequest)
			_, _ = io.WriteString(writer, "Authorization response was incomplete. You can close this window.")
			return
		}
		result <- callback{code: code}
		_, _ = io.WriteString(writer, "Linked successfully. You can close this window and return to VLC Media Watcher.")
	})
	go func() { _ = server.Serve(listener) }()
	defer server.Close()

	if err := openURL(authorizeURL); err != nil {
		return "", fmt.Errorf("open authorization URL: %w", err)
	}
	select {
	case response := <-result:
		if response.err != "" {
			return "", fmt.Errorf("OAuth link: %s", response.err)
		}
		return response.code, nil
	case <-ctx.Done():
		return "", ctx.Err()
	}
}

func exchangeCode(ctx context.Context, service Service, clientID, clientSecret, code, verifier string) (string, error) {
	endpoint := ""
	values := url.Values{"grant_type": {"authorization_code"}, "client_id": {clientID}, "code": {code}, "redirect_uri": {CallbackURL}}
	switch service {
	case AniList:
		endpoint = "https://anilist.co/api/v2/oauth/token"
		values.Set("client_secret", clientSecret)
	case MyAnimeList:
		endpoint = "https://myanimelist.net/v1/oauth2/token"
		values.Set("code_verifier", verifier)
	case Trakt:
		endpoint = "https://api.trakt.tv/oauth/token"
		values.Set("client_secret", clientSecret)
	case SIMKL:
		endpoint = "https://api.simkl.com/oauth/token"
		values.Set("code_verifier", verifier)
	default:
		return "", fmt.Errorf("unsupported tracker %q", service)
	}
	request, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, strings.NewReader(values.Encode()))
	if err != nil {
		return "", err
	}
	request.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	var response struct {
		AccessToken string `json:"access_token"`
	}
	if err := doJSON(request, &response); err != nil {
		return "", fmt.Errorf("exchange %s authorization code: %w", title(service), err)
	}
	return response.AccessToken, nil
}

func OpenBrowser(link string) error {
	var command *exec.Cmd
	switch runtime.GOOS {
	case "darwin":
		command = exec.Command("open", link)
	case "windows":
		command = exec.Command("rundll32", "url.dll,FileProtocolHandler", link)
	default:
		command = exec.Command("xdg-open", link)
	}
	return command.Start()
}

func randomURLValue(size int) (string, error) {
	bytes := make([]byte, size)
	if _, err := rand.Read(bytes); err != nil {
		return "", fmt.Errorf("generate OAuth state: %w", err)
	}
	return base64.RawURLEncoding.EncodeToString(bytes), nil
}

func pkceChallenge(verifier string) string {
	sum := sha256.Sum256([]byte(verifier))
	return base64.RawURLEncoding.EncodeToString(sum[:])
}

func title(service Service) string {
	definition, ok := Lookup(string(service))
	if ok {
		return definition.Name
	}
	return string(service)
}
