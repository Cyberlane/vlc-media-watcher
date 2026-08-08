package credentials

import (
	"context"
	"errors"
	"fmt"
	"net"
	"strings"
)

// HTTPStatusError is implemented by sanitized API clients. It permits safe
// provider-neutral classification without parsing endpoint, body, or token
// details from an error string.
type HTTPStatusError interface {
	HTTPStatusCode() int
}

// ClassifyAPIError maps authentication rejection separately from temporary
// network and service failures. Callers should retain their own detailed error
// for diagnostics only; incident surfaces use this state and IncidentDetail.
func ClassifyAPIError(err error) State {
	if err == nil {
		return StateReady
	}
	var status HTTPStatusError
	if errors.As(err, &status) {
		switch code := status.HTTPStatusCode(); {
		case code == 401 || code == 403:
			return StateAPIAuthRejected
		case code == 429 || code >= 500:
			return StateTemporaryUnavailable
		}
	}
	if errors.Is(err, context.DeadlineExceeded) {
		return StateTemporaryUnavailable
	}
	var networkError net.Error
	if errors.As(err, &networkError) && (networkError.Timeout() || networkError.Temporary()) {
		return StateTemporaryUnavailable
	}
	return StateTemporaryUnavailable
}

// Incident describes a credential state without carrying provider locators,
// environment names, values, or provider errors. It is suitable for local
// status storage and user-facing transports.
type Incident struct {
	Scope        string
	CredentialID ID
	State        State
	Detail       string
}

// IncidentEventKind describes the transition emitted by an incident tracker.
type IncidentEventKind string

const (
	IncidentOpened    IncidentEventKind = "opened"
	IncidentUpdated   IncidentEventKind = "updated"
	IncidentRecovered IncidentEventKind = "recovered"
)

// IncidentEvent is emitted once when a state first becomes actionable, when
// its repair guidance changes, and once when it recovers.
type IncidentEvent struct {
	Kind     IncidentEventKind
	Incident Incident
}

// Empty reports whether an event represents no externally visible transition.
func (e IncidentEvent) Empty() bool { return e.Kind == "" }

// NewIncident creates a redacted incident from stable internal identifiers.
// Callers must use it instead of passing provider messages through to logs or
// persistence.
func NewIncident(scope string, id ID, state State) (Incident, error) {
	if strings.TrimSpace(scope) == "" {
		return Incident{}, fmt.Errorf("incident scope is required")
	}
	if strings.TrimSpace(string(id)) == "" {
		return Incident{}, fmt.Errorf("incident credential ID is required")
	}
	if state == "" {
		return Incident{}, fmt.Errorf("incident state is required")
	}
	return Incident{Scope: scope, CredentialID: id, State: state, Detail: IncidentDetail(id, state)}, nil
}

// IncidentDetail returns generic repair guidance derived only from the stable
// credential ID and typed state. It must not accept provider error text.
func IncidentDetail(id ID, state State) string {
	label := incidentCredentialLabel(id)
	switch state {
	case StateReady:
		return label + " is ready."
	case StateNotConfigured:
		return label + " is not configured; repair its binding in the terminal UI."
	case StateCredentialMissing:
		return label + " is missing; repair its binding in the terminal UI."
	case StateNeedsUserAction:
		return label + " needs user action; repair its binding in the terminal UI."
	case StateProviderUnavailable:
		return label + " provider is unavailable; retry after repairing provider access."
	case StateCredentialDenied:
		return label + " access was denied; repair its binding in the terminal UI."
	case StateCredentialInvalid:
		return label + " is invalid; repair its binding in the terminal UI."
	case StateAPIAuthRejected:
		return label + " was rejected by its API; repair or re-link it in the terminal UI."
	case StateTemporaryUnavailable:
		return label + " is temporarily unavailable; retry later."
	default:
		return label + " needs repair in the terminal UI."
	}
}

func incidentCredentialLabel(id ID) string {
	switch id {
	case VLCPasswordID:
		return "VLC credential"
	case SonarrAPIKeyID:
		return "Sonarr API key"
	case RadarrAPIKeyID:
		return "Radarr API key"
	}
	parts := strings.Split(string(id), ".")
	if len(parts) >= 3 && parts[0] == "tracker" {
		name := parts[1]
		switch name {
		case "anilist":
			name = "AniList"
		case "myanimelist":
			name = "MyAnimeList"
		case "simkl":
			name = "SIMKL"
		default:
			name = "Trakt"
		}
		return name + " " + strings.ReplaceAll(parts[2], "-", " ")
	}
	return "Credential"
}

// IncidentTracker de-duplicates state transitions within a process. Durable
// stores can apply the same event model across process restarts.
type IncidentTracker struct {
	active map[string]Incident
}

// Observe reports a typed state. Ready emits exactly one recovery event after
// an active incident; repeated identical failures emit nothing.
func (t *IncidentTracker) Observe(scope string, id ID, state State) (IncidentEvent, error) {
	incident, err := NewIncident(scope, id, state)
	if err != nil {
		return IncidentEvent{}, err
	}
	if t.active == nil {
		t.active = make(map[string]Incident)
	}
	key := incident.Scope + "\x00" + string(incident.CredentialID)
	previous, active := t.active[key]
	if state == StateReady {
		if !active {
			return IncidentEvent{}, nil
		}
		delete(t.active, key)
		return IncidentEvent{Kind: IncidentRecovered, Incident: incident}, nil
	}
	if active && previous.State == state {
		return IncidentEvent{}, nil
	}
	t.active[key] = incident
	if active {
		return IncidentEvent{Kind: IncidentUpdated, Incident: incident}, nil
	}
	return IncidentEvent{Kind: IncidentOpened, Incident: incident}, nil
}
