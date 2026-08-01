package vlc

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"time"
)

type Status struct {
	MediaPath     string
	Position      float64
	LengthSeconds int64
	State         string
}

type Client struct {
	endpoint, password string
	httpClient         *http.Client
}

func NewClient(endpoint, password string) *Client {
	return &Client{endpoint: strings.TrimRight(endpoint, "/"), password: password, httpClient: &http.Client{Timeout: 5 * time.Second}}
}

func (c *Client) Status(ctx context.Context) (Status, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.endpoint+"/requests/status.json", nil)
	if err != nil {
		return Status{}, err
	}
	if c.password != "" {
		req.SetBasicAuth("", c.password)
	}
	response, err := c.httpClient.Do(req)
	if err != nil {
		return Status{}, err
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		return Status{}, fmt.Errorf("unexpected VLC status: %s", response.Status)
	}
	var payload struct {
		State       string  `json:"state"`
		Position    float64 `json:"position"`
		Length      int64   `json:"length"`
		Information struct {
			Category struct {
				Meta struct {
					URI      string `json:"uri"`
					URL      string `json:"url"`
					Filename string `json:"filename"`
				} `json:"meta"`
			} `json:"category"`
		} `json:"information"`
	}
	if err := json.NewDecoder(response.Body).Decode(&payload); err != nil {
		return Status{}, fmt.Errorf("decode VLC status: %w", err)
	}
	meta := payload.Information.Category.Meta
	path, err := mediaPath(firstNonEmpty(meta.URI, meta.URL, meta.Filename))
	if err != nil {
		return Status{}, err
	}
	return Status{MediaPath: path, Position: payload.Position, LengthSeconds: payload.Length, State: payload.State}, nil
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if value != "" {
			return value
		}
	}
	return ""
}

func mediaPath(uri string) (string, error) {
	if uri == "" {
		return "", nil
	}
	u, err := url.Parse(uri)
	if err != nil {
		return "", fmt.Errorf("parse VLC media URI: %w", err)
	}
	if u.Scheme == "file" {
		if u.Host != "" && !strings.EqualFold(u.Host, "localhost") {
			return "//" + u.Host + u.Path, nil
		}
		return u.Path, nil
	}
	return uri, nil
}
