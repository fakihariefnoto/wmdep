// Package app holds the wmdep domain model: hosts, apps, and their observed
// state. It deliberately depends on nothing else in the project (no SSH, no
// UI, no config formats) so every other package can import it freely.
package app

import (
	"fmt"
	"strings"
	"time"
)

// ProviderKind identifies how an app is managed on its host.
type ProviderKind string

const (
	KindSystemd ProviderKind = "systemd"
	KindDocker  ProviderKind = "docker"
	KindCompose ProviderKind = "compose"
	KindPM2     ProviderKind = "pm2"
	KindCommand ProviderKind = "command"
)

// KindNames is the list of providers wmdep understands today. v0.1 ships
// systemd and docker; the rest exist so config validates, and so the escape
// hatch is always available.
var KindNames = []ProviderKind{
	KindSystemd,
	KindDocker,
	KindCompose,
	KindPM2,
	KindCommand,
}

// Valid reports whether k is a known provider.
func (k ProviderKind) Valid() bool {
	for _, n := range KindNames {
		if n == k {
			return true
		}
	}
	return false
}

// AuthMethod describes how wmdep authenticates to a host.
type AuthMethod string

const (
	AuthAgent    AuthMethod = "agent"    // ssh-agent, the recommended path
	AuthKey      AuthMethod = "key"      // private key file on disk
	AuthPassword AuthMethod = "password" // OS keyring; never stored in config
)

// Valid reports whether a is a known auth method.
func (a AuthMethod) Valid() bool {
	switch a {
	case AuthAgent, AuthKey, AuthPassword:
		return true
	}
	return false
}

// State is the observed lifecycle state of an app.
type State string

const (
	StateUnknown  State = "unknown"
	StateRunning  State = "running"
	StateStopped  State = "stopped"
	StateDegraded State = "degraded"
	StateFailed   State = "failed"
)

// Healthy reports whether the state means "serving traffic".
func (s State) Healthy() bool { return s == StateRunning }

// Status is one observation of an app at a point in time.
type Status struct {
	State    State
	Detail   string    // free-form provider detail, e.g. "active (running)"
	Since    time.Time // when the current state began, if known
	CPU      float64   // percent, 0 when unknown
	MemBytes int64     // RSS bytes, 0 when unknown
	Err      error     // last error talking to the host, nil when fine
	At       time.Time // when this observation was taken
}

// Uptime returns how long the app has been in its current state. Zero when the
// provider does not report a start time.
func (s Status) Uptime(now time.Time) time.Duration {
	if s.Since.IsZero() {
		return 0
	}
	if d := now.Sub(s.Since); d > 0 {
		return d
	}
	return 0
}

// ErrText renders the status error, or "" when there is none.
func (s Status) ErrText() string {
	if s.Err == nil {
		return ""
	}
	return s.Err.Error()
}

// Metrics is a lightweight resource sample, separate from Status because the
// UI polls it less often on busy hosts.
type Metrics struct {
	CPU      float64
	MemBytes int64
	At       time.Time
}

// HealthKind identifies the kind of health probe for an app.
type HealthKind string

const (
	HealthNone    HealthKind = "none"
	HealthHTTP    HealthKind = "http"
	HealthCommand HealthKind = "command"
	HealthTCP     HealthKind = "tcp"
)

// Valid reports whether k is a known health probe kind.
func (k HealthKind) Valid() bool {
	switch k {
	case HealthNone, HealthHTTP, HealthCommand, HealthTCP:
		return true
	}
	return false
}

// Health describes how to decide whether an app is actually serving traffic,
// as opposed to merely being "started" from the supervisor's point of view.
type Health struct {
	Kind    HealthKind
	URL     string // http
	Cmd     string // command
	Address string // tcp, host:port
	Expect  string // http: status code, command: expected stdout
	Timeout time.Duration
}

// Host is an SSH endpoint. Credentials are never stored here: a password lives
// in the OS keyring under "wmdep:<host id>".
type Host struct {
	ID        string
	Address   string
	Port      int
	User      string
	Auth      AuthMethod
	Key       string
	ProxyJump string
	Tags      []string
}

// Addr is the dial target, host:port.
func (h Host) Addr() string {
	port := h.Port
	if port == 0 {
		port = 22
	}
	return fmt.Sprintf("%s:%d", h.Address, port)
}

// App is one manageable unit on one host.
type App struct {
	ID          string
	Host        string
	Environment string
	Kind        ProviderKind
	Unit        string // systemd
	Container   string // docker
	Project     string // compose
	Service     string // compose
	Process     string // pm2
	Command     string // command provider: the status command
	Workdir     string
	Health      *Health
	Deploy      string
	Tags        []string
}

// Key is the stable identity of an app across environments and hosts.
type Key struct {
	Env  string
	Host string
	ID   string
}

// String renders the key for logs and maps, "env/host/id".
func (k Key) String() string {
	return strings.Join([]string{k.Env, k.Host, k.ID}, "/")
}

// Key returns the app's identity.
func (a App) Key() Key {
	return Key{Env: a.Environment, Host: a.Host, ID: a.ID}
}

// DisplayName is what the UI shows in a table cell.
func (a App) DisplayName() string {
	return a.ID
}

// Target names the concrete thing the provider manages, e.g. "api.service" or
// "docker:redis". Used in the detail pane.
func (a App) Target() string {
	switch a.Kind {
	case KindSystemd:
		return a.Unit
	case KindDocker:
		return a.Container
	case KindCompose:
		if a.Project != "" {
			return a.Project + "/" + a.Service
		}
		return a.Service
	case KindPM2:
		return a.Process
	case KindCommand:
		return "sh -c"
	}
	return ""
}
