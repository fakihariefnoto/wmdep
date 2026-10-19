package sshprov

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/fakihariefnoto/wmdep/internal/app"
)

// Discovered is one service found on a host during a scan.
type Discovered struct {
	Kind   app.ProviderKind
	Name   string // unit, container, or process name
	Detail string
}

// Discover runs one read-only pass over the host's supervisors. A supervisor
// that is not installed is skipped, not an error.
func (p *SSH) Discover(ctx context.Context, host app.Host) ([]Discovered, error) {
	var out []Discovered
	var firstErr error

	if res, err := p.run.Exec(ctx, host, []string{
		"systemctl", "list-units", "--type=service", "--state=running",
		"--output=json", "--no-pager",
	}); err == nil && res.Ok() {
		units, perr := parseSystemdUnits(res.Stdout)
		if perr != nil && firstErr == nil {
			firstErr = perr
		}
		out = append(out, units...)
	}

	if res, err := p.run.Exec(ctx, host, []string{
		"docker", "ps", "--format", "{{json .}}",
	}); err == nil && res.Ok() {
		containers, perr := parseDockerPS(res.Stdout)
		if perr != nil && firstErr == nil {
			firstErr = perr
		}
		out = append(out, containers...)
	}

	if res, err := p.run.Exec(ctx, host, []string{"pm2", "jlist"}); err == nil && res.Ok() {
		procs, perr := parsePM2List(res.Stdout)
		if perr != nil && firstErr == nil {
			firstErr = perr
		}
		out = append(out, procs...)
	}

	if len(out) == 0 && firstErr != nil {
		return nil, firstErr
	}
	return out, nil
}

type systemdUnit struct {
	Unit        string `json:"unit"`
	ActiveState string `json:"active"`
	SubState    string `json:"sub"`
	Description string `json:"description"`
}

func parseSystemdUnits(raw string) ([]Discovered, error) {
	var units []systemdUnit
	if err := json.Unmarshal([]byte(strings.TrimSpace(raw)), &units); err != nil {
		return nil, fmt.Errorf("systemctl json: %w", err)
	}
	out := make([]Discovered, 0, len(units))
	for _, u := range units {
		name := strings.TrimSuffix(u.Unit, ".service")
		out = append(out, Discovered{
			Kind: app.KindSystemd, Name: name,
			Detail: strings.TrimSpace(u.ActiveState + " " + u.SubState),
		})
	}
	return out, nil
}

type dockerPSRow struct {
	Names  string `json:"Names"`
	Image  string `json:"Image"`
	State  string `json:"State"`
	Status string `json:"Status"`
}

func parseDockerPS(raw string) ([]Discovered, error) {
	var out []Discovered
	for _, line := range strings.Split(strings.TrimSpace(raw), "\n") {
		if strings.TrimSpace(line) == "" {
			continue
		}
		var row dockerPSRow
		if err := json.Unmarshal([]byte(line), &row); err != nil {
			return nil, fmt.Errorf("docker ps json: %w", err)
		}
		out = append(out, Discovered{
			Kind: app.KindDocker, Name: row.Names,
			Detail: strings.TrimSpace(row.Image + " · " + row.Status),
		})
	}
	return out, nil
}

func parsePM2List(raw string) ([]Discovered, error) {
	var rows []pm2Row
	if err := json.Unmarshal([]byte(strings.TrimSpace(raw)), &rows); err != nil {
		return nil, fmt.Errorf("pm2 json: %w", err)
	}
	out := make([]Discovered, 0, len(rows))
	for _, r := range rows {
		out = append(out, Discovered{Kind: app.KindPM2, Name: r.Name, Detail: r.PM2Env.Status})
	}
	return out, nil
}
