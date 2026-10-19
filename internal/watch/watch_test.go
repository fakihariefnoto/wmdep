package watch

import (
	"context"
	"errors"
	"io"
	"sync/atomic"
	"testing"
	"time"

	"github.com/fakihariefnoto/wmdep/internal/app"
	"github.com/fakihariefnoto/wmdep/internal/provider"
)

// stub is a provider whose per-host behaviour the test chooses. Only the
// methods the poller uses do anything interesting. calls is atomic because the
// poller polls concurrently.
type stub struct {
	failHosts map[string]bool
	calls     atomic.Int64
}

func (s *stub) Status(_ context.Context, _ app.App, h app.Host) (app.Status, error) {
	s.calls.Add(1)
	if s.failHosts[h.ID] {
		return app.Status{State: app.StateUnknown, At: time.Now()},
			errors.New("dial tcp " + h.Addr() + ": connection refused")
	}
	return app.Status{State: app.StateRunning, At: time.Now()}, nil
}

func (s *stub) Start(context.Context, app.App, app.Host) error   { return nil }
func (s *stub) Stop(context.Context, app.App, app.Host) error    { return nil }
func (s *stub) Restart(context.Context, app.App, app.Host) error { return nil }
func (s *stub) Logs(context.Context, app.App, app.Host, provider.LogOpts) (io.ReadCloser, error) {
	return nil, provider.ErrNotSupported
}
func (s *stub) Metrics(context.Context, app.App, app.Host) (app.Metrics, error) {
	return app.Metrics{}, provider.ErrNotSupported
}

func fixture() ([]app.App, map[string]app.Host) {
	hosts := map[string]app.Host{
		"web-1": {ID: "web-1", Address: "10.0.0.11", User: "deploy", Auth: app.AuthAgent},
		"web-2": {ID: "web-2", Address: "10.0.0.12", User: "deploy", Auth: app.AuthAgent},
	}
	apps := []app.App{
		{ID: "api", Host: "web-1", Environment: "prod", Kind: app.KindSystemd, Unit: "api.service"},
		{ID: "worker", Host: "web-1", Environment: "prod", Kind: app.KindSystemd, Unit: "worker.service"},
		{ID: "redis", Host: "web-2", Environment: "prod", Kind: app.KindDocker, Container: "redis"},
		{ID: "web", Host: "web-2", Environment: "staging", Kind: app.KindSystemd, Unit: "web.service"},
	}
	return apps, hosts
}

func TestBuildFilters(t *testing.T) {
	apps, hosts := fixture()

	t.Run("no filter matches everything", func(t *testing.T) {
		if got := len(Build(apps, hosts, "", "", "")); got != 4 {
			t.Errorf("got %d targets, want 4", got)
		}
	})
	t.Run("environment", func(t *testing.T) {
		got := Build(apps, hosts, "staging", "", "")
		if len(got) != 1 || got[0].App.ID != "web" {
			t.Errorf("staging = %v, want just web", got)
		}
	})
	t.Run("host", func(t *testing.T) {
		got := Build(apps, hosts, "", "web-1", "")
		if len(got) != 2 {
			t.Errorf("web-1 = %d targets, want 2", len(got))
		}
	})
	t.Run("query is case insensitive", func(t *testing.T) {
		got := Build(apps, hosts, "", "", "REDIS")
		if len(got) != 1 || got[0].App.ID != "redis" {
			t.Errorf("query REDIS = %v, want redis", got)
		}
	})
	t.Run("query matches provider kind", func(t *testing.T) {
		got := Build(apps, hosts, "", "", "docker")
		if len(got) != 1 || got[0].App.ID != "redis" {
			t.Errorf("query docker = %v, want redis", got)
		}
	})
	t.Run("query matches target", func(t *testing.T) {
		got := Build(apps, hosts, "", "", "api.service")
		if len(got) != 1 {
			t.Errorf("query api.service = %d targets, want 1", len(got))
		}
	})
	t.Run("unknown host is dropped", func(t *testing.T) {
		orphan := []app.App{{ID: "ghost", Host: "nowhere", Environment: "prod"}}
		if got := Build(orphan, hosts, "", "", ""); len(got) != 0 {
			t.Errorf("orphan = %v, want none", got)
		}
	})
	t.Run("combined filters", func(t *testing.T) {
		if got := Build(apps, hosts, "prod", "web-2", ""); len(got) != 1 {
			t.Errorf("prod/web-2 = %d targets, want 1", len(got))
		}
	})
}

func TestPollStreamsResults(t *testing.T) {
	apps, hosts := fixture()
	st := &stub{}
	reg := provider.NewRegistry()
	reg.RegisterAll(st)
	p := New(reg, time.Second, 4)

	results := p.Poll(context.Background(), Build(apps, hosts, "", "", ""), time.Second)
	count := 0
	for res := range results {
		count++
		if res.Err != nil {
			t.Errorf("unexpected error: %v", res.Err)
		}
		if res.Status.State != app.StateRunning {
			t.Errorf("state = %v, want running", res.Status.State)
		}
	}
	if count != 4 {
		t.Errorf("results = %d, want 4", count)
	}
}

func TestPollBacksOffAfterFailure(t *testing.T) {
	apps, hosts := fixture()
	st := &stub{failHosts: map[string]bool{"web-1": true}}
	reg := provider.NewRegistry()
	reg.RegisterAll(st)
	p := New(reg, 200*time.Millisecond, 4)

	targets := Build(apps, hosts, "", "web-1", "")
	interval := 10 * time.Millisecond

	for range p.Poll(context.Background(), targets, interval) {
	}

	key := app.Key{Env: "prod", Host: "web-1", ID: "api"}
	if n := p.FailureCount(key); n != 1 {
		t.Fatalf("failure count = %d, want 1", n)
	}
	// A failed app is not due again immediately, even though the interval is
	// tiny: that is the whole point of the backoff.
	if p.Due(key, time.Now(), interval) {
		t.Error("app should back off after a failure")
	}
	if !p.Due(key, time.Now().Add(5*time.Second), interval) {
		t.Error("app should become due again after the backoff window")
	}
}

func TestPollOnlyPollsDueTargets(t *testing.T) {
	apps, hosts := fixture()
	st := &stub{failHosts: map[string]bool{"web-1": true}}
	reg := provider.NewRegistry()
	reg.RegisterAll(st)
	p := New(reg, 200*time.Millisecond, 4)

	targets := Build(apps, hosts, "", "", "")
	interval := 10 * time.Millisecond

	for range p.Poll(context.Background(), targets, interval) {
	}
	first := st.calls.Load()

	for range p.Poll(context.Background(), targets, interval) {
	}
	// Second round should only re-poll the two healthy apps on web-2.
	if second := st.calls.Load() - first; second != 2 {
		t.Errorf("second round polled %d apps, want 2 (the healthy ones)", second)
	}
}

func TestPollEmptyTargetsClosesChannel(t *testing.T) {
	reg := provider.NewRegistry()
	reg.RegisterAll(&stub{})
	p := New(reg, time.Second, 2)

	results := p.Poll(context.Background(), nil, time.Second)
	if _, ok := <-results; ok {
		t.Error("channel should be closed for an empty target list")
	}
}

func TestNewAppliesSaneDefaults(t *testing.T) {
	p := New(provider.NewRegistry(), 0, 0)
	if p.timeout <= 0 {
		t.Error("timeout should default")
	}
	if p.limit <= 0 {
		t.Error("limit should default")
	}
}
