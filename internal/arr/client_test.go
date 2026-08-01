package arr

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func TestConstructorsValidateEndpointAndSecret(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name     string
		endpoint string
		apiKey   string
	}{
		{name: "missing key", endpoint: "http://localhost:8989"},
		{name: "missing scheme", endpoint: "localhost:8989", apiKey: "key"},
		{name: "unsupported scheme", endpoint: "ftp://localhost", apiKey: "key"},
		{name: "missing host", endpoint: "http:///sonarr", apiKey: "key"},
		{name: "credentials in URL", endpoint: "http://user:pass@localhost", apiKey: "key"},
		{name: "query in URL", endpoint: "http://localhost?apikey=bad", apiKey: "key"},
		{name: "fragment in URL", endpoint: "http://localhost/#fragment", apiKey: "key"},
		{name: "API path in URL", endpoint: "http://localhost/sonarr/api/v3/", apiKey: "key"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			if _, err := NewSonarrClient(test.endpoint, test.apiKey); err == nil {
				t.Fatal("NewSonarrClient returned a nil error")
			}
			if _, err := NewRadarrClient(test.endpoint, test.apiKey); err == nil {
				t.Fatal("NewRadarrClient returned a nil error")
			}
		})
	}
}

func TestClientUsesTenSecondTimeout(t *testing.T) {
	t.Parallel()
	sonarr, err := NewSonarrClient("http://localhost:8989", "key")
	if err != nil {
		t.Fatal(err)
	}
	radarr, err := NewRadarrClient("http://localhost:7878", "key")
	if err != nil {
		t.Fatal(err)
	}
	if sonarr.api.httpClient.Timeout != 10*time.Second {
		t.Fatalf("Sonarr timeout = %s", sonarr.api.httpClient.Timeout)
	}
	if radarr.api.httpClient.Timeout != 10*time.Second {
		t.Fatalf("Radarr timeout = %s", radarr.api.httpClient.Timeout)
	}
}

func TestCheckPreservesURLBaseUsesHeaderAndCachesStatus(t *testing.T) {
	t.Parallel()
	requests := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests++
		assertRequest(t, r, http.MethodGet, "/proxy/sonarr/api/v3/system/status", "secret")
		if r.URL.RawQuery != "" {
			t.Errorf("unexpected query = %q", r.URL.RawQuery)
		}
		writeJSON(w, http.StatusOK, `{"appName":"Sonarr","instanceName":"Living Room","version":"4.0.15","osName":"Windows","urlBase":"/sonarr","isWindows":true}`)
	}))
	defer server.Close()

	client, err := NewSonarrClient(server.URL+"/proxy/sonarr/", "secret")
	if err != nil {
		t.Fatal(err)
	}
	first, err := client.Check(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	second, err := client.Check(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if requests != 1 {
		t.Fatalf("system/status requests = %d, want 1", requests)
	}
	if first != second {
		t.Fatalf("cached instance differs: %#v != %#v", first, second)
	}
	if first.AppName != "Sonarr" || first.InstanceName != "Living Room" || first.Version != "4.0.15" || !first.IsWindows {
		t.Fatalf("instance = %#v", first)
	}
}

func TestConcurrentCheckUsesOneStatusRequest(t *testing.T) {
	t.Parallel()
	var requests atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		requests.Add(1)
		time.Sleep(20 * time.Millisecond)
		writeJSON(w, http.StatusOK, `{"appName":"Radarr","version":"6.1.1","isLinux":true}`)
	}))
	defer server.Close()
	client, err := NewRadarrClient(server.URL, "key")
	if err != nil {
		t.Fatal(err)
	}

	const callers = 12
	errorsFound := make(chan error, callers)
	var wait sync.WaitGroup
	for range callers {
		wait.Add(1)
		go func() {
			defer wait.Done()
			_, checkErr := client.Check(context.Background())
			errorsFound <- checkErr
		}()
	}
	wait.Wait()
	close(errorsFound)
	for checkErr := range errorsFound {
		if checkErr != nil {
			t.Fatal(checkErr)
		}
	}
	if requests.Load() != 1 {
		t.Fatalf("status requests = %d, want 1", requests.Load())
	}
}

func TestCheckRetriesAfterFailure(t *testing.T) {
	t.Parallel()
	var requests atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		if requests.Add(1) == 1 {
			w.WriteHeader(http.StatusServiceUnavailable)
			return
		}
		writeJSON(w, http.StatusOK, `{"appName":"Sonarr","version":"4.0.15","isLinux":true}`)
	}))
	defer server.Close()
	client, err := NewSonarrClient(server.URL, "key")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := client.Check(context.Background()); err == nil {
		t.Fatal("first Check returned a nil error")
	}
	if _, err := client.Check(context.Background()); err != nil {
		t.Fatal(err)
	}
	if _, err := client.Check(context.Background()); err != nil {
		t.Fatal(err)
	}
	if requests.Load() != 2 {
		t.Fatalf("status requests = %d, want 2", requests.Load())
	}
}

func TestCheckRejectsWrongApplicationAndMalformedResponse(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name string
		body string
	}{
		{name: "wrong app", body: `{"appName":"Radarr","version":"6.0.0"}`},
		{name: "missing version", body: `{"appName":"Sonarr"}`},
		{name: "malformed JSON", body: `{`},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				writeJSON(w, http.StatusOK, test.body)
			}))
			defer server.Close()
			client, err := NewSonarrClient(server.URL, "secret")
			if err != nil {
				t.Fatal(err)
			}
			if _, err := client.Check(context.Background()); err == nil {
				t.Fatal("Check returned a nil error")
			}
		})
	}
}

func TestHTTPErrorIsBoundedAndRedactsAPIKey(t *testing.T) {
	t.Parallel()
	const secret = "the-super-secret-api-key"
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusUnauthorized)
		_, _ = fmt.Fprintf(w, "rejected %s: %s", secret, strings.Repeat("x", maxErrorBody*2))
	}))
	defer server.Close()
	client, err := NewRadarrClient(server.URL, secret)
	if err != nil {
		t.Fatal(err)
	}
	_, err = client.Check(context.Background())
	if err == nil {
		t.Fatal("Check returned a nil error")
	}
	message := err.Error()
	if strings.Contains(message, secret) {
		t.Fatalf("error leaked API key: %q", message)
	}
	if !strings.Contains(message, "[redacted]") {
		t.Fatalf("error did not identify redaction: %q", message)
	}
	if len(message) > maxErrorBody+300 {
		t.Fatalf("error was not bounded: %d bytes", len(message))
	}
}

func TestHTTPErrorRedactsAPIKeyAcrossBodyLimit(t *testing.T) {
	t.Parallel()
	const secret = "BOUNDARY-SECRET-DO-NOT-LEAK"
	prefix := strings.Repeat("x", maxErrorBody-len(secret)/2)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusForbidden)
		_, _ = fmt.Fprint(w, prefix+secret+strings.Repeat("z", 100))
	}))
	defer server.Close()
	client, err := NewSonarrClient(server.URL, secret)
	if err != nil {
		t.Fatal(err)
	}
	_, err = client.Check(context.Background())
	if err == nil {
		t.Fatal("Check returned a nil error")
	}
	message := err.Error()
	if strings.Contains(message, secret) || strings.Contains(message, "BOUNDARY-SECRET") || strings.Contains(message, "DO-NOT-LEAK") {
		t.Fatalf("boundary error leaked API key material: %q", message)
	}
	if len(message) > maxErrorBody+300 {
		t.Fatalf("error was not bounded: %d bytes", len(message))
	}
}

func TestClientDoesNotForwardAPIKeyAcrossRedirect(t *testing.T) {
	t.Parallel()
	const secret = "redirect-secret"
	redirectedRequests := 0
	target := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {
		redirectedRequests++
	}))
	defer target.Close()
	source := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, target.URL, http.StatusTemporaryRedirect)
	}))
	defer source.Close()

	client, err := NewSonarrClient(source.URL, secret)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := client.Check(context.Background()); err == nil {
		t.Fatal("Check followed or accepted a redirect")
	}
	if redirectedRequests != 0 {
		t.Fatalf("redirect target received %d requests", redirectedRequests)
	}
}

func TestAllMonitored(t *testing.T) {
	t.Parallel()
	if (Match{}).AllMonitored(false) {
		t.Fatal("empty match reported all monitored")
	}
	match := Match{Targets: []Target{{ID: 1, Monitored: false}, {ID: 2, Monitored: false}}}
	if !match.AllMonitored(false) {
		t.Fatal("all false targets were not detected")
	}
	if match.AllMonitored(true) {
		t.Fatal("false targets reported all true")
	}
	match.Targets[1].Monitored = true
	if match.AllMonitored(false) || match.AllMonitored(true) {
		t.Fatal("mixed targets reported a uniform state")
	}
}

func assertRequest(t *testing.T, r *http.Request, method, requestPath, apiKey string) {
	t.Helper()
	if r.Method != method {
		t.Errorf("method = %q, want %q", r.Method, method)
	}
	if r.URL.Path != requestPath {
		t.Errorf("path = %q, want %q", r.URL.Path, requestPath)
	}
	if got := r.Header.Get("X-Api-Key"); got != apiKey {
		t.Errorf("X-Api-Key = %q, want expected key", got)
	}
	if got := r.URL.Query().Get("apikey"); got != "" {
		t.Errorf("API key appeared in query: %q", got)
	}
}

func writeJSON(w http.ResponseWriter, status int, body string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_, _ = w.Write([]byte(body))
}
