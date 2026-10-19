package sshprov

import (
	"testing"

	"github.com/fakihariefnoto/wmdep/internal/app"
)

func TestParseSystemdUnits(t *testing.T) {
	raw := `[{"unit":"api.service","active":"active","sub":"running"},{"unit":"worker.service","active":"active","sub":"running"}]`
	got, err := parseSystemdUnits(raw)
	if err != nil || len(got) != 2 {
		t.Fatalf("got %v %v", got, err)
	}
	if got[0].Kind != app.KindSystemd || got[0].Name != "api" {
		t.Errorf("unit = %+v", got[0])
	}
}

func TestParseDockerPS(t *testing.T) {
	raw := "{\"Names\":\"redis\",\"Image\":\"redis:7\",\"State\":\"running\",\"Status\":\"Up 9 days\"}\n{\"Names\":\"postgres\",\"Image\":\"pg:16\",\"State\":\"running\",\"Status\":\"Up 2 days\"}"
	got, err := parseDockerPS(raw)
	if err != nil || len(got) != 2 {
		t.Fatalf("got %v %v", got, err)
	}
	if got[0].Kind != app.KindDocker || got[0].Name != "redis" {
		t.Errorf("container = %+v", got[0])
	}
}

func TestParsePM2List(t *testing.T) {
	raw := `[{"name":"api","pm2_env":{"status":"online"}}]`
	got, err := parsePM2List(raw)
	if err != nil || len(got) != 1 || got[0].Name != "api" {
		t.Fatalf("got %v %v", got, err)
	}
}
