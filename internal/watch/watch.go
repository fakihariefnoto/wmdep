// Package watch schedules status polls against configured apps. The UI polls
// only what is on screen, per host jitter keeps a fleet from stampeding, and
// repeated failures back off so one dead box does not dominate the event loop.
package watch

import (
	"context"
	"math/rand/v2"
	"strings"
	"sync"
	"time"

	"github.com/fakihariefnoto/wmdep/internal/app"
	"github.com/fakihariefnoto/wmdep/internal/provider"
)

// Target is one app together with the host it runs on: everything a provider
// needs, resolved up front so a poll never depends on a lookup succeeding.
type Target struct {
	App  app.App
	Host app.Host
}

// Result is one poll outcome. Err is set for transport-level problems; the
// Status carries app-level ones, so the UI can distinguish "host unreachable"
// from "unit failed".
type Result struct {
	Target Target
	Status app.Status
	Err    error
}

// Poller polls targets through a registry, tracking per-app failures so that
// unreachable hosts are retried less and less often.
type Poller struct {
	reg     *provider.Registry
	timeout time.Duration
	limit   int

	mu    sync.Mutex
	fails map[app.Key]int
	next  map[app.Key]time.Time
}

// New returns a Poller. timeout bounds each individual status call; limit caps
// concurrent remote calls per round.
func New(reg *provider.Registry, timeout time.Duration, limit int) *Poller {
	if timeout <= 0 {
		timeout = 10 * time.Second
	}
	if limit <= 0 {
		limit = 8
	}
	return &Poller{
		reg:     reg,
		timeout: timeout,
		limit:   limit,
		fails:   map[app.Key]int{},
		next:    map[app.Key]time.Time{},
	}
}

// Due reports whether an app should be polled at now, given its failure
// history. The first failure backs off ~2x the poll interval, then 4x, 8x, and
// so on, capped at five minutes, with jitter to spread retries.
func (p *Poller) Due(key app.Key, now time.Time, interval time.Duration) bool {
	p.mu.Lock()
	defer p.mu.Unlock()
	n, ok := p.next[key]
	if !ok {
		return true
	}
	return !now.Before(n)
}

// FailureCount returns how many consecutive polls have failed for an app.
func (p *Poller) FailureCount(key app.Key) int {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.fails[key]
}

func (p *Poller) note(key app.Key, ok bool, interval time.Duration) {
	p.mu.Lock()
	defer p.mu.Unlock()
	if ok {
		delete(p.fails, key)
		delete(p.next, key)
		return
	}
	p.fails[key]++
	shift := min(p.fails[key], 8)
	wait := interval * time.Duration(1<<shift)
	if wait > 5*time.Minute {
		wait = 5 * time.Minute
	}
	p.next[key] = time.Now().Add(jitter(wait))
}

// jitter spreads a duration by ±20%.
func jitter(d time.Duration) time.Duration {
	if d <= 0 {
		return 0
	}
	delta := float64(d) * 0.2
	return d + time.Duration((rand.Float64()*2-1)*delta)
}

// Poll polls every due target concurrently and streams results on the returned
// channel, which is closed when all polls finish or ctx is cancelled. Callers
// must drain the channel; a buffered channel is used so a slow UI cannot wedge
// a poll goroutine.
func (p *Poller) Poll(ctx context.Context, targets []Target, interval time.Duration) <-chan Result {
	out := make(chan Result, len(targets))
	now := time.Now()

	due := make([]Target, 0, len(targets))
	for _, t := range targets {
		if p.Due(t.App.Key(), now, interval) {
			due = append(due, t)
		}
	}
	if len(due) == 0 {
		close(out)
		return out
	}

	sem := make(chan struct{}, p.limit)
	var wg sync.WaitGroup
	for _, t := range due {
		wg.Add(1)
		go func(t Target) {
			defer wg.Done()
			sem <- struct{}{}
			defer func() { <-sem }()

			cctx, cancel := context.WithTimeout(ctx, p.timeout)
			defer cancel()

			res := Result{Target: t}
			prov, err := p.reg.For(t.App)
			if err != nil {
				res.Err = err
			} else {
				st, err := prov.Status(cctx, t.App, t.Host)
				res.Status = st
				res.Err = err
			}
			p.note(t.App.Key(), res.Err == nil && res.Status.Err == nil, interval)
			out <- res
		}(t)
	}

	go func() {
		wg.Wait()
		close(out)
	}()
	return out
}

// Build resolves targets from apps and hosts, keeping only those matching the
// filters. An empty env matches everything; an empty hostID likewise. query is
// a case-insensitive substring match against the app id, host, environment, and
// provider, and is ignored when empty.
func Build(apps []app.App, hosts map[string]app.Host, env, hostID, query string) []Target {
	q := strings.ToLower(strings.TrimSpace(query))
	out := make([]Target, 0, len(apps))
	for _, a := range apps {
		if env != "" && a.Environment != env {
			continue
		}
		if hostID != "" && a.Host != hostID {
			continue
		}
		if q != "" && !matches(a, q) {
			continue
		}
		h, ok := hosts[a.Host]
		if !ok {
			continue
		}
		out = append(out, Target{App: a, Host: h})
	}
	return out
}

func matches(a app.App, q string) bool {
	haystack := strings.ToLower(strings.Join([]string{
		a.ID, a.Host, a.Environment, string(a.Kind), a.Target(),
	}, " "))
	return strings.Contains(haystack, q)
}
