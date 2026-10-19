// Package provider turns an app definition plus its host into observations and
// actions. Every remote interaction in wmdep goes through this interface so the
// UI never constructs a shell command itself, and so tests can substitute a
// fake for the real SSH-backed implementations.
package provider

import (
	"context"
	"errors"
	"fmt"
	"io"
	"sync"
	"time"

	"github.com/fakihariefnoto/wmdep/internal/app"
)

// ErrNotSupported is returned by a provider that cannot perform an operation,
// e.g. metrics for a command provider. Callers show it as "not supported"
// rather than as a failure.
var ErrNotSupported = errors.New("not supported by this provider")

// LogOpts controls a log stream.
type LogOpts struct {
	Tail   int       // lines of history before following
	Follow bool      // keep the stream open
	Since  time.Time // only entries after this time
}

// Provider observes and controls one kind of app.
//
// Implementations must be safe for concurrent use: the connection pool shares
// one per host across panes and background polls.
type Provider interface {
	// Status reports the current state. It must not block longer than the
	// context allows, and must return a Status (possibly with Err set) rather
	// than a bare error when the host is reachable but the app is not.
	Status(ctx context.Context, app app.App, host app.Host) (app.Status, error)

	Start(ctx context.Context, app app.App, host app.Host) error
	Stop(ctx context.Context, app app.App, host app.Host) error
	Restart(ctx context.Context, app app.App, host app.Host) error
	Reload(ctx context.Context, app app.App, host app.Host) error

	// Logs returns a stream the caller must close. Cancelling ctx must
	// terminate the remote process, not merely abandon the reader.
	Logs(ctx context.Context, app app.App, host app.Host, opts LogOpts) (io.ReadCloser, error)

	// Metrics samples resource usage. Providers without numbers return
	// ErrNotSupported.
	Metrics(ctx context.Context, app app.App, host app.Host) (app.Metrics, error)
}

// Registry maps provider kinds to implementations and hands out the right one
// for an app.
type Registry struct {
	mu     sync.RWMutex
	byKind map[app.ProviderKind]Provider
}

// NewRegistry returns an empty registry. Register a fallback for kinds before
// use; See Fallback.
func NewRegistry() *Registry {
	return &Registry{byKind: map[app.ProviderKind]Provider{}}
}

// Register binds a provider to a kind, replacing any previous binding.
func (r *Registry) Register(kind app.ProviderKind, p Provider) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.byKind[kind] = p
}

// RegisterAll binds one provider to every known kind. It is how the fake
// provider covers a config that uses any provider type.
func (r *Registry) RegisterAll(p Provider) {
	for _, kind := range app.KindNames {
		r.Register(kind, p)
	}
}

// For returns the provider for an app.
func (r *Registry) For(a app.App) (Provider, error) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	if p, ok := r.byKind[a.Kind]; ok && p != nil {
		return p, nil
	}
	return nil, fmt.Errorf("no provider registered for %q", a.Kind)
}
