package config

import (
	"fmt"
	"time"

	"gopkg.in/yaml.v3"
)

// Version is the config schema version this build understands.
const Version = 1

// Duration is a time.Duration that unmarshals from "10s", "1m30s", or a plain
// integer number of seconds.
type Duration time.Duration

// UnmarshalYAML implements yaml.Unmarshaler.
func (d *Duration) UnmarshalYAML(node *yaml.Node) error {
	var raw any
	if err := node.Decode(&raw); err != nil {
		return err
	}
	switch v := raw.(type) {
	case int:
		*d = Duration(time.Duration(v) * time.Second)
		return nil
	case string:
		parsed, err := time.ParseDuration(v)
		if err != nil {
			return fmt.Errorf("invalid duration %q: %w", v, err)
		}
		*d = Duration(parsed)
		return nil
	default:
		return fmt.Errorf("invalid duration %v (want \"10s\" or seconds)", raw)
	}
}

// Std returns the value as a time.Duration.
func (d Duration) Std() time.Duration { return time.Duration(d) }

// MarshalYAML renders a duration the same way it is written: "10s", not a raw
// nanosecond count. Without this, every editor save would rewrite timeouts as
// integers.
func (d Duration) MarshalYAML() (any, error) {
	return time.Duration(d).String(), nil
}

// EnvFilter is the environments.enabled field. It accepts either the scalar
// "all" or a list of environment names.
type EnvFilter struct {
	All   bool
	Names []string
}

// UnmarshalYAML implements yaml.Unmarshaler.
func (e *EnvFilter) UnmarshalYAML(node *yaml.Node) error {
	if node.Kind == yaml.ScalarNode {
		if node.Value == "all" {
			e.All = true
			return nil
		}
		e.Names = []string{node.Value}
		return nil
	}
	var names []string
	if err := node.Decode(&names); err != nil {
		return err
	}
	e.Names = names
	return nil
}

// Enabled reports whether an environment should be loaded.
func (e EnvFilter) Enabled(name string) bool {
	if e.All || len(e.Names) == 0 {
		return true
	}
	for _, n := range e.Names {
		if n == name {
			return true
		}
	}
	return false
}

// File is the shape of config.yaml, the root of a wmdep config directory.
type File struct {
	Version      int       `yaml:"version"`
	Defaults     Defaults  `yaml:"defaults"`
	Environments EnvFilter `yaml:"environments"`
	UI           UI        `yaml:"ui"`
}

// Defaults are applied to every host and app unless overridden.
type Defaults struct {
	SSHTimeout   Duration `yaml:"ssh_timeout"`
	PollInterval Duration `yaml:"poll_interval"`
	LogTail      int      `yaml:"log_tail"`
	MaxStreams   int      `yaml:"max_streams_per_host"`
}

// UI holds presentation preferences.
type UI struct {
	Theme       string `yaml:"theme"` // "dark" | "light"
	ConfirmProd *bool  `yaml:"confirm_prod"`
	Splash      *bool  `yaml:"splash"` // false skips the menu screen
}

// HostsFile is the shape of hosts.yaml: a shared inventory referenced by name.
type HostsFile struct {
	Hosts map[string]HostSpec `yaml:"hosts"`
}

// HostSpec is one host entry.
type HostSpec struct {
	Address   string   `yaml:"address"`
	Port      int      `yaml:"port"`
	User      string   `yaml:"user"`
	Auth      string   `yaml:"auth"`
	Key       string   `yaml:"key"`
	ProxyJump string   `yaml:"proxy_jump"`
	Tags      []string `yaml:"tags"`
}

// PipelinesFile is the shape of pipelines.yaml: shared deploy definitions.
type PipelinesFile struct {
	Pipelines map[string]Pipeline `yaml:"pipelines"`
}

// Pipeline is an ordered list of steps plus an optional rollback.
type Pipeline struct {
	Steps    []Step `yaml:"steps"`
	Rollback []Step `yaml:"rollback"`
}

// Step is one deploy step. Exactly one field may be set; the step type is the
// field name, which keeps pipelines self-describing in YAML.
type Step struct {
	Build    *BuildStep    `yaml:"build"`
	Pull     *PullStep     `yaml:"pull"`
	Upload   *UploadStep   `yaml:"upload"`
	Download *DownloadStep `yaml:"download"`
	Run      *RunStep      `yaml:"run"`
	Wait     *WaitStep     `yaml:"wait"`
	Notify   *NotifyStep   `yaml:"notify"`
}

// Kind returns the step type name and whether exactly one was set.
func (s Step) Kind() (string, bool) {
	type named struct {
		name string
		set  bool
	}
	all := []named{
		{"build", s.Build != nil},
		{"pull", s.Pull != nil},
		{"upload", s.Upload != nil},
		{"download", s.Download != nil},
		{"run", s.Run != nil},
		{"wait", s.Wait != nil},
		{"notify", s.Notify != nil},
	}
	var found string
	for _, n := range all {
		if !n.set {
			continue
		}
		if found != "" {
			return "", false
		}
		found = n.name
	}
	return found, found != ""
}

// BuildStep compiles something, locally or on the server.
type BuildStep struct {
	Local  string `yaml:"local"`  // shell command run on the operator's machine
	Remote string `yaml:"remote"` // directory on the server to build in
	Cmd    string `yaml:"cmd"`    // command run in Remote
}

// PullStep updates a checkout on the server from git.
type PullStep struct {
	Remote string `yaml:"remote"`
	Branch string `yaml:"branch"`
	Commit string `yaml:"commit"`
}

// UploadStep copies a local path to the server over SFTP.
type UploadStep struct {
	Local  string `yaml:"local"`
	Remote string `yaml:"remote"`
	Mode   string `yaml:"mode"`
}

// DownloadStep copies a server path to the operator's machine.
type DownloadStep struct {
	Remote string `yaml:"remote"`
	Local  string `yaml:"local"`
}

// RunStep runs a command on the server.
type RunStep struct {
	Remote  string   `yaml:"remote"`
	Timeout Duration `yaml:"timeout"`
}

// WaitStep blocks until a condition holds.
type WaitStep struct {
	Health  bool     `yaml:"health"`
	Sleep   Duration `yaml:"sleep"`
	Timeout Duration `yaml:"timeout"`
}

// NotifyStep surfaces a message in the UI or desktop notification.
type NotifyStep struct {
	Message string `yaml:"message"`
}

// EnvironmentFile is the shape of environments/<name>.yaml.
type EnvironmentFile struct {
	Name  string    `yaml:"name"`
	Hosts []string  `yaml:"hosts"`
	Apps  []AppSpec `yaml:"apps"`
}

// HealthSpec mirrors app.Health in YAML.
type HealthSpec struct {
	Type    string   `yaml:"type"`
	URL     string   `yaml:"url"`
	Cmd     string   `yaml:"cmd"`
	Address string   `yaml:"address"`
	Expect  string   `yaml:"expect"`
	Timeout Duration `yaml:"timeout"`
}

// AppSpec is one app entry inside an environment file.
type AppSpec struct {
	ID        string      `yaml:"id"`
	Host      string      `yaml:"host"`
	Provider  string      `yaml:"provider"`
	Unit      string      `yaml:"unit"`
	Container string      `yaml:"container"`
	Project   string      `yaml:"project"`
	Service   string      `yaml:"service"`
	Process   string      `yaml:"process"`
	Command   string      `yaml:"command"`
	Workdir   string      `yaml:"workdir"`
	Health    *HealthSpec `yaml:"health"`
	Deploy    string      `yaml:"deploy"`
	Tags      []string    `yaml:"tags"`
}
