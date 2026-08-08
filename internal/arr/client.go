// Package arr provides the small subset of the Sonarr and Radarr APIs needed
// to reconcile watched media with their monitored state.
package arr

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"path"
	"runtime"
	"strings"
	"sync"
	"time"
)

const (
	requestTimeout = 10 * time.Second
	maxErrorBody   = 4 << 10
)

// Manager identifies the media manager that owns a match.
type Manager string

const (
	ManagerSonarr Manager = "sonarr"
	ManagerRadarr Manager = "radarr"
	// ManagerLocal identifies a provisional identity parsed locally from a
	// filename. It is never accepted by a Sonarr/Radarr API operation.
	ManagerLocal Manager = "local"
)

// ErrAmbiguousMatch indicates that more than one remote item had the same
// normalized full path. Callers must not mutate any of the candidates.
var ErrAmbiguousMatch = errors.New("ambiguous media-manager match")

// HTTPStatusError retains only a status code and an already-redacted message.
// It lets higher layers classify authentication rejection without inspecting
// endpoint or response-body text.
type HTTPStatusError struct {
	StatusCode int
	Message    string
}

func (e HTTPStatusError) Error() string       { return e.Message }
func (e HTTPStatusError) HTTPStatusCode() int { return e.StatusCode }

// Instance describes the remote application returned by system/status.
type Instance struct {
	AppName      string
	InstanceName string
	Version      string
	OSName       string
	URLBase      string
	IsWindows    bool
	IsLinux      bool
	IsOSX        bool
}

// PathMapping translates a path prefix as seen by VLC to the corresponding
// prefix as seen by Sonarr or Radarr. Both fields must be set when used.
type PathMapping struct {
	LocalPrefix  string
	RemotePrefix string
}

// Target is a manager-neutral item whose monitored state can be changed. A
// Radarr match has one movie target; a Sonarr match can contain several
// episode targets when one file contains multiple episodes.
type Target struct {
	ID        int
	Monitored bool
}

// Match is an exact, unique media-file match returned by a manager.
type Match struct {
	Manager   Manager
	Title     string
	MediaPath string
	// Resolution records the deterministic rule that selected MediaPath. It is
	// deliberately diagnostic only: callers must still reject ambiguous
	// matches and never use a title as an identity key.
	Resolution string
	Targets    []Target
	Identity   MediaIdentity
}

// MediaIdentity is stable metadata supplied by Sonarr or Radarr after an
// exact file-path match. It is deliberately separate from a tracker mapping:
// a local library item can have one confirmed ID for each linked tracker.
type MediaIdentity struct {
	Manager        Manager
	SourceID       int
	Kind           string // movie or series
	Title          string
	Year           int
	SeasonNumber   int // 0 when not applicable or a file crosses seasons
	EpisodeNumbers []int
	TVDBID         int
	TMDBID         int
	IMDbID         string
}

// AllMonitored reports whether the match has at least one target and every
// target is already in the desired monitored state.
func (m Match) AllMonitored(desired bool) bool {
	if len(m.Targets) == 0 {
		return false
	}
	for _, target := range m.Targets {
		if target.Monitored != desired {
			return false
		}
	}
	return true
}

type apiClient struct {
	manager      Manager
	baseURL      *url.URL
	apiKey       string
	httpClient   *http.Client
	localWindows bool

	instanceMu sync.Mutex
	instance   *Instance
}

type systemStatus struct {
	AppName      string `json:"appName"`
	InstanceName string `json:"instanceName"`
	Version      string `json:"version"`
	OSName       string `json:"osName"`
	URLBase      string `json:"urlBase"`
	IsWindows    bool   `json:"isWindows"`
	IsLinux      bool   `json:"isLinux"`
	IsOSX        bool   `json:"isOsx"`
}

func newAPIClient(manager Manager, endpoint, apiKey string) (*apiClient, error) {
	if strings.TrimSpace(apiKey) == "" {
		return nil, fmt.Errorf("%s API key is required", manager)
	}

	baseURL, err := url.Parse(strings.TrimSpace(endpoint))
	if err != nil {
		return nil, fmt.Errorf("parse %s endpoint: %w", manager, err)
	}
	if baseURL.Scheme != "http" && baseURL.Scheme != "https" {
		return nil, fmt.Errorf("%s endpoint must use http or https", manager)
	}
	if baseURL.Host == "" {
		return nil, fmt.Errorf("%s endpoint must include a host", manager)
	}
	if baseURL.User != nil {
		return nil, fmt.Errorf("%s endpoint must not include credentials", manager)
	}
	if baseURL.RawQuery != "" || baseURL.Fragment != "" {
		return nil, fmt.Errorf("%s endpoint must not include a query or fragment", manager)
	}
	cleanedEndpointPath := path.Clean(baseURL.Path)
	if cleanedEndpointPath == "." {
		cleanedEndpointPath = ""
	}
	if strings.EqualFold(cleanedEndpointPath, "/api/v3") || strings.HasSuffix(strings.ToLower(cleanedEndpointPath), "/api/v3") {
		return nil, fmt.Errorf("%s endpoint must not include /api/v3", manager)
	}
	baseURL.Path = strings.TrimRight(cleanedEndpointPath, "/")
	baseURL.RawPath = ""

	return &apiClient{
		manager:      manager,
		baseURL:      baseURL,
		apiKey:       apiKey,
		localWindows: runtime.GOOS == "windows",
		httpClient: &http.Client{
			Timeout: requestTimeout,
			// Do not forward the privileged API key to a redirect target. URL
			// bases are explicit configuration and should be corrected instead.
			CheckRedirect: func(*http.Request, []*http.Request) error {
				return http.ErrUseLastResponse
			},
		},
	}, nil
}

func (c *apiClient) check(ctx context.Context) (Instance, error) {
	c.instanceMu.Lock()
	defer c.instanceMu.Unlock()
	if c.instance != nil {
		return *c.instance, nil
	}

	var status systemStatus
	if err := c.getJSON(ctx, "/api/v3/system/status", nil, &status); err != nil {
		return Instance{}, fmt.Errorf("check %s: %w", c.manager, err)
	}
	if !strings.EqualFold(status.AppName, string(c.manager)) {
		name := status.AppName
		if name == "" {
			name = "unknown application"
		}
		return Instance{}, fmt.Errorf("check %s: endpoint identifies itself as %s", c.manager, name)
	}
	if strings.TrimSpace(status.Version) == "" {
		return Instance{}, fmt.Errorf("check %s: system status did not include a version", c.manager)
	}

	instance := Instance{
		AppName:      status.AppName,
		InstanceName: status.InstanceName,
		Version:      status.Version,
		OSName:       status.OSName,
		URLBase:      status.URLBase,
		IsWindows:    status.IsWindows || strings.Contains(strings.ToLower(status.OSName), "windows"),
		IsLinux:      status.IsLinux,
		IsOSX:        status.IsOSX,
	}
	c.instance = &instance
	return instance, nil
}

func (c *apiClient) getJSON(ctx context.Context, apiPath string, query url.Values, destination any) error {
	requestURL := c.resolve(apiPath, query)
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, requestURL, nil)
	if err != nil {
		return fmt.Errorf("create GET %s request: %w", apiPath, err)
	}
	req.Header.Set("X-Api-Key", c.apiKey)
	req.Header.Set("Accept", "application/json")

	response, err := c.httpClient.Do(req)
	if err != nil {
		return fmt.Errorf("GET %s: %w", apiPath, err)
	}
	defer response.Body.Close()
	if !successful(response.StatusCode) {
		return responseError(http.MethodGet, apiPath, response, c.apiKey)
	}
	if err := json.NewDecoder(response.Body).Decode(destination); err != nil {
		return fmt.Errorf("decode GET %s response: %w", apiPath, err)
	}
	return nil
}

func (c *apiClient) putJSON(ctx context.Context, apiPath string, payload any) error {
	body, err := json.Marshal(payload)
	if err != nil {
		return fmt.Errorf("encode PUT %s request: %w", apiPath, err)
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPut, c.resolve(apiPath, nil), bytes.NewReader(body))
	if err != nil {
		return fmt.Errorf("create PUT %s request: %w", apiPath, err)
	}
	req.Header.Set("X-Api-Key", c.apiKey)
	req.Header.Set("Accept", "application/json")
	req.Header.Set("Content-Type", "application/json")

	response, err := c.httpClient.Do(req)
	if err != nil {
		return fmt.Errorf("PUT %s: %w", apiPath, err)
	}
	defer response.Body.Close()
	if !successful(response.StatusCode) {
		return responseError(http.MethodPut, apiPath, response, c.apiKey)
	}
	_, _ = io.Copy(io.Discard, io.LimitReader(response.Body, 64<<10))
	return nil
}

func (c *apiClient) resolve(apiPath string, query url.Values) string {
	requestURL := *c.baseURL
	requestURL.Path = strings.TrimRight(c.baseURL.Path, "/") + "/" + strings.TrimLeft(apiPath, "/")
	requestURL.RawPath = ""
	requestURL.RawQuery = query.Encode()
	return requestURL.String()
}

func successful(statusCode int) bool {
	return statusCode >= http.StatusOK && statusCode < http.StatusMultipleChoices
}

func responseError(method, apiPath string, response *http.Response, secrets ...string) error {
	readLimit := maxErrorBody + 1
	for _, secret := range secrets {
		if len(secret) > readLimit-maxErrorBody {
			readLimit = maxErrorBody + len(secret)
		}
	}
	body, err := io.ReadAll(io.LimitReader(response.Body, int64(readLimit)))
	if err != nil {
		return HTTPStatusError{StatusCode: response.StatusCode, Message: fmt.Sprintf("%s %s returned %s", method, apiPath, response.Status)}
	}
	detail := strings.TrimSpace(string(body))
	for _, secret := range secrets {
		if secret != "" {
			detail = strings.ReplaceAll(detail, secret, "[redacted]")
		}
	}
	truncated := len(detail) > maxErrorBody
	if truncated {
		detail = detail[:maxErrorBody]
	}
	if detail == "" {
		return HTTPStatusError{StatusCode: response.StatusCode, Message: fmt.Sprintf("%s %s returned %s", method, apiPath, response.Status)}
	}
	if truncated {
		detail += "..."
	}
	return HTTPStatusError{StatusCode: response.StatusCode, Message: fmt.Sprintf("%s %s returned %s: %s", method, apiPath, response.Status, detail)}
}
