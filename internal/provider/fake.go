package provider

import (
	"context"
	"fmt"
	"hash/fnv"
	"io"
	"math"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/fakihariefnoto/wmdep/internal/app"
)

// Fake is an in-memory provider used for M1 development: it lets the whole UI
// be built, resized, and exercised before any SSH code exists, and it stays
// useful afterwards as the provider behind `wmdep --demo` and UI tests.
//
// Behaviour is deterministic per app key, with a slow drift over time so the
// UI shows movement (uptime, CPU, memory) rather than a frozen screen.
type Fake struct {
	mu      sync.Mutex
	started map[app.Key]time.Time
	// offline lists host IDs that should behave as unreachable, so the error
	// paths are visible during development.
	offline map[string]bool
}

// NewFake returns a fake provider. Hosts named in offline behave as down.
func NewFake(offline ...string) *Fake {
	f := &Fake{
		started: map[app.Key]time.Time{},
		offline: map[string]bool{},
	}
	for _, h := range offline {
		f.offline[h] = true
	}
	return f
}

func hash(key app.Key) uint32 {
	h := fnv.New32a()
	_, _ = h.Write([]byte(key.String()))
	return h.Sum32()
}

// startedAt returns a stable, plausible start time for an app.
func (f *Fake) startedAt(key app.Key) time.Time {
	f.mu.Lock()
	defer f.mu.Unlock()
	if t, ok := f.started[key]; ok {
		return t
	}
	// Between 1 hour and 9 days ago, deterministically per app.
	age := time.Duration(1+hash(key)%216) * time.Hour
	t := time.Now().Add(-age).Truncate(time.Second)
	f.started[key] = t
	return t
}

// Status implements Provider.
func (f *Fake) Status(_ context.Context, a app.App, host app.Host) (app.Status, error) {
	now := time.Now()

	if f.offline[host.ID] {
		return app.Status{
			State: app.StateUnknown,
			Err:   fmt.Errorf("dial tcp %s: connection refused", host.Addr()),
			At:    now,
		}, nil
	}

	h := hash(a.Key())
	state := app.StateRunning
	switch h % 12 {
	case 0:
		state = app.StateStopped
	case 1:
		state = app.StateDegraded
	case 2:
		state = app.StateFailed
	}

	since := f.startedAt(a.Key())
	if state != app.StateRunning {
		since = now.Add(-time.Duration(1+h%90) * time.Minute)
	}

	// Gentle sine drift so numbers move between polls.
	phase := float64(now.Unix()%600) / 600.0
	cpu := 2 + float64(h%20) + 6*math.Sin(phase*2*math.Pi)
	mem := int64(40_000_000) + int64(h%400)*1_000_000 +
		int64(3_000_000*math.Sin(phase*2*math.Pi))

	detail := "active (running)"
	switch a.Kind {
	case app.KindDocker:
		detail = "Up (healthy)"
	case app.KindCompose:
		detail = "running"
	case app.KindPM2:
		detail = "online"
	case app.KindCommand:
		detail = "exit 0"
	}
	switch state {
	case app.StateStopped:
		detail = "inactive (dead)"
	case app.StateDegraded:
		detail = "active (running), health check failing"
	case app.StateFailed:
		detail = "failed"
	}

	return app.Status{
		State:    state,
		Detail:   detail,
		Since:    since,
		CPU:      math.Round(cpu*10) / 10,
		MemBytes: mem,
		At:       now,
	}, nil
}

// Start implements Provider.
func (f *Fake) Start(_ context.Context, a app.App, host app.Host) error {
	return f.mutate(a, host, func() { f.setStarted(a.Key(), time.Now()) })
}

// Stop implements Provider.
func (f *Fake) Stop(_ context.Context, a app.App, host app.Host) error {
	if f.isOffline(host) {
		return fmt.Errorf("dial tcp %s: connection refused", host.Addr())
	}
	f.setStarted(a.Key(), time.Time{})
	return nil
}

// Reload implements Provider; the fake treats it like a restart.
func (f *Fake) Reload(ctx context.Context, a app.App, host app.Host) error {
	return f.Restart(ctx, a, host)
}

// Restart implements Provider.
func (f *Fake) Restart(_ context.Context, a app.App, host app.Host) error {
	return f.mutate(a, host, func() { f.setStarted(a.Key(), time.Now()) })
}

func (f *Fake) mutate(a app.App, host app.Host, fn func()) error {
	if f.isOffline(host) {
		return fmt.Errorf("dial tcp %s: connection refused", host.Addr())
	}
	fn()
	return nil
}

func (f *Fake) isOffline(host app.Host) bool {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.offline[host.ID]
}

func (f *Fake) setStarted(key app.Key, t time.Time) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.started[key] = t
}

// Metrics implements Provider.
func (f *Fake) Metrics(_ context.Context, a app.App, host app.Host) (app.Metrics, error) {
	st, err := f.Status(context.Background(), a, host)
	if err != nil {
		return app.Metrics{}, err
	}
	if st.Err != nil {
		return app.Metrics{}, st.Err
	}
	return app.Metrics{CPU: st.CPU, MemBytes: st.MemBytes, At: st.At}, nil
}

// Logs implements Provider. The returned reader emits a plausible log tail and
// then, when Follow is set, keeps emitting until ctx is cancelled.
func (f *Fake) Logs(ctx context.Context, a app.App, host app.Host, opts LogOpts) (io.ReadCloser, error) {
	if f.isOffline(host) {
		return nil, fmt.Errorf("dial tcp %s: connection refused", host.Addr())
	}
	tail := opts.Tail
	if tail <= 0 {
		tail = 50
	}
	pr, pw := io.Pipe()
	go func() {
		defer pw.Close()

		h := hash(a.Key())
		levels := []string{"INFO", "INFO", "INFO", "WARN", "ERROR", "DEBUG"}

		write := func(i int, t time.Time) bool {
			lvl := levels[int(h+uint32(i))%len(levels)]
			msg := logMessage(int(h), i)
			line := fmt.Sprintf("%s %-5s %s\n", t.Format("15:04:05"), lvl, msg)
			if _, err := io.WriteString(pw, line); err != nil {
				return false
			}
			return true
		}

		// History.
		base := time.Now().Add(-time.Duration(tail) * 400 * time.Millisecond)
		for i := range tail {
			if !write(i, base.Add(time.Duration(i)*400*time.Millisecond)) {
				return
			}
		}
		if !opts.Follow {
			return
		}

		// Live tail.
		ticker := time.NewTicker(900 * time.Millisecond)
		defer ticker.Stop()
		i := tail
		for {
			select {
			case <-ctx.Done():
				return
			case t := <-ticker.C:
				if !write(i, t) {
					return
				}
				i++
			}
		}
	}()
	return pr, nil
}

// Summary renders a one-line description of the fake provider, used by
// `wmdep status` so the output is honest about what generated it.
func (f *Fake) Summary() string {
	f.mu.Lock()
	defer f.mu.Unlock()
	if len(f.offline) == 0 {
		return "fake (no hosts offline)"
	}
	var down []string
	for h := range f.offline {
		down = append(down, h)
	}
	sort.Strings(down)
	return "fake (offline: " + strings.Join(down, ", ") + ")"
}

// logMessage generates one plausible line. Keeping it as a switch (rather than
// a table of format strings) means each template carries exactly the arguments
// it needs.
func logMessage(seed, i int) string {
	upstreams := []string{"db", "redis", "queue", "auth"}
	switch i % 6 {
	case 0:
		return fmt.Sprintf("request handled path=/api/%d status=%d dur=%dms",
			1+(seed+i)%40,
			200+((seed+i)%3)*100,
			1+(seed+i*3)%400)
	case 1:
		return fmt.Sprintf("pool size=%d idle=%d waiting=%d",
			4+(seed+i)%12,
			1+(seed+i*5)%8,
			(seed+i)%3)
	case 2:
		return fmt.Sprintf("cache hit ratio=0.%d", 70+(seed+i)%30)
	case 3:
		return fmt.Sprintf("reconnecting to upstream=%s attempt=%d",
			upstreams[(seed+i)%len(upstreams)],
			1+(seed+i)%5)
	case 4:
		return fmt.Sprintf("job completed id=%d in %dms",
			1000+(seed+i*11)%9000,
			5+(seed+i*13)%500)
	default:
		return fmt.Sprintf("scheduled tick drift=%dms", 1+(seed+i*17)%120)
	}
}
