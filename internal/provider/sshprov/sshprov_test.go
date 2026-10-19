package sshprov

import (
	"testing"
	"time"

	"github.com/fakihariefnoto/wmdep/internal/app"
)

func TestParseSystemdShow(t *testing.T) {
	out := `ActiveState=active
SubState=running
ActiveEnterTimestamp=Mon 2026-10-12 08:31:02 UTC
MainPID=4242
`
	st := parseSystemdShow(out)
	if st.State != app.StateRunning {
		t.Errorf("state = %v, want running", st.State)
	}
	if st.Since.IsZero() {
		t.Error("since not parsed")
	}
	if st.Detail != "active (running)" {
		t.Errorf("detail = %q", st.Detail)
	}

	for _, tc := range []struct {
		active, sub string
		want        app.State
	}{
		{"inactive", "dead", app.StateStopped},
		{"failed", "failed", app.StateFailed},
		{"activating", "start", app.StateDegraded},
	} {
		st := parseSystemdShow("ActiveState=" + tc.active + "\nSubState=" + tc.sub)
		if st.State != tc.want {
			t.Errorf("%s/%s = %v, want %v", tc.active, tc.sub, st.State, tc.want)
		}
	}
}

func TestParsePSAndBytes(t *testing.T) {
	cpu, mem := parsePS("12.5 204800\n")
	if cpu != 12.5 || mem != 204800*1024 {
		t.Errorf("ps = %v %v", cpu, mem)
	}
	for _, tc := range []struct {
		in   string
		want int64
	}{
		{"1.5GiB", 1610612736},
		{"512MiB", 536870912},
		{"1.2MB", 1200000},
		{"42B", 42},
	} {
		if got := parseBytes(tc.in); got != tc.want {
			t.Errorf("parseBytes(%q) = %d, want %d", tc.in, got, tc.want)
		}
	}
}

func TestParseDockerState(t *testing.T) {
	st, err := parseDockerState(`{"Status":"running","Running":true,"StartedAt":"2026-10-19T10:00:00Z","Health":{"Status":"healthy"}}`)
	if err != nil {
		t.Fatal(err)
	}
	if st.State != app.StateRunning || st.Since.IsZero() {
		t.Errorf("state = %+v", st)
	}

	st, _ = parseDockerState(`{"Status":"running","Running":true,"Health":{"Status":"unhealthy"}}`)
	if st.State != app.StateDegraded {
		t.Errorf("unhealthy = %v, want degraded", st.State)
	}

	st, _ = parseDockerState(`{"Status":"exited","Running":false}`)
	if st.State != app.StateStopped {
		t.Errorf("exited = %v, want stopped", st.State)
	}

	if _, err := parseDockerState("not json"); err == nil {
		t.Error("expected a parse error")
	}
}

func TestParseDockerStats(t *testing.T) {
	cpu, mem := parseDockerStats("2.5%|120MiB / 1GiB")
	if cpu != 2.5 {
		t.Errorf("cpu = %v", cpu)
	}
	if mem != 120*1024*1024 {
		t.Errorf("mem = %d", mem)
	}
}

func TestParseComposePS(t *testing.T) {
	array := `[{"Service":"api","State":"running","Status":"Up 2 hours"},{"Service":"db","State":"exited","Status":"Exited"}]`
	st, err := parseComposePS(array, "api")
	if err != nil || st.State != app.StateRunning {
		t.Fatalf("array: %+v %v", st, err)
	}
	st, _ = parseComposePS(array, "db")
	if st.State != app.StateStopped {
		t.Errorf("db = %v", st.State)
	}

	lines := "{\"Service\":\"api\",\"State\":\"restarting\",\"Status\":\"Restarting\"}\n"
	st, _ = parseComposePS(lines, "api")
	if st.State != app.StateDegraded {
		t.Errorf("lines = %v", st.State)
	}

	st, _ = parseComposePS("", "api")
	if st.State != app.StateUnknown {
		t.Errorf("empty = %v", st.State)
	}
}

func TestParsePM2(t *testing.T) {
	raw := `[{"name":"api","pm2_env":{"status":"online","pm_uptime":1729000000000},"monit":{"cpu":4.5,"memory":104857600}}]`
	st, err := parsePM2(raw, "api")
	if err != nil {
		t.Fatal(err)
	}
	if st.State != app.StateRunning || st.CPU != 4.5 || st.MemBytes != 104857600 {
		t.Errorf("state = %+v", st)
	}
	if st.Since.IsZero() {
		t.Error("uptime not parsed")
	}

	st, _ = parsePM2(raw, "missing")
	if st.State != app.StateUnknown {
		t.Errorf("missing = %v", st.State)
	}
}

func TestField(t *testing.T) {
	out := "ActiveState=active\nMainPID=0\n"
	if field(out, "ActiveState") != "active" {
		t.Error("ActiveState")
	}
	if field(out, "Nope") != "" {
		t.Error("missing field should be empty")
	}
	_ = time.Now
}
