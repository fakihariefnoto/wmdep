// Package sshx owns every remote connection wmdep makes: dialing, auth, host
// key verification, a per-host connection pool, command execution, and SFTP.
//
// The UI never touches this package's details: providers call Exec/Stream and
// get plain results or an io.ReadCloser.
package sshx

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/kevinburke/ssh_config"
	"github.com/pkg/sftp"
	"github.com/zalando/go-keyring"
	"golang.org/x/crypto/ssh"
	"golang.org/x/crypto/ssh/agent"
	"golang.org/x/crypto/ssh/knownhosts"

	"github.com/fakihariefnoto/wmdep/internal/app"
)

// KeyringService is the OS-keyring service name for host passwords.
const KeyringService = "wmdep"

// ErrUnknownHostKey is returned when a host is not in known_hosts. It is a
// deliberate hard stop: wmdep never trusts an unknown key silently.
var ErrUnknownHostKey = errors.New("host key not found in known_hosts")

// Options configures the pool.
type Options struct {
	Timeout    time.Duration
	KnownHosts string // defaults to ~/.ssh/known_hosts
	SSHConfig  string // defaults to ~/.ssh/config
	Insecure   bool   // opt-in, prints a warning at the call site
}

// Result is one command execution.
type Result struct {
	Stdout   string
	Stderr   string
	ExitCode int
}

// Ok reports whether the command exited zero.
func (r Result) Ok() bool { return r.ExitCode == 0 }

// Error renders a failure with stderr, for provider error messages.
func (r Result) Error() string {
	msg := strings.TrimSpace(r.Stderr)
	if msg == "" {
		msg = strings.TrimSpace(r.Stdout)
	}
	if msg == "" {
		msg = fmt.Sprintf("exit %d", r.ExitCode)
	}
	return msg
}

// dialFunc is ssh.Dial in production; tests can substitute it.
type dialFunc func(network, addr string, cfg *ssh.ClientConfig) (*ssh.Client, error)

// Pool keeps at most one SSH client per host and re-dials on demand.
type Pool struct {
	opts Options
	dial dialFunc

	mu      sync.Mutex
	clients map[string]*ssh.Client
}

// NewPool returns a pool with defaults applied.
func NewPool(opts Options) *Pool {
	if opts.Timeout <= 0 {
		opts.Timeout = 10 * time.Second
	}
	if opts.KnownHosts == "" {
		opts.KnownHosts = defaultPath(".ssh/known_hosts")
	}
	if opts.SSHConfig == "" {
		opts.SSHConfig = defaultPath(".ssh/config")
	}
	return &Pool{
		opts:    opts,
		dial:    ssh.Dial,
		clients: map[string]*ssh.Client{},
	}
}

func defaultPath(rel string) string {
	home, err := os.UserHomeDir()
	if err != nil {
		return ""
	}
	return filepath.Join(home, rel)
}

// Exec runs argv on the host and returns captured output and exit status. A
// non-zero exit is not an error; transport failures are.
func (p *Pool) Exec(ctx context.Context, host app.Host, argv []string) (Result, error) {
	client, err := p.client(ctx, host)
	if err != nil {
		return Result{}, err
	}

	sess, err := client.NewSession()
	if err != nil {
		p.drop(host.ID)
		return Result{}, fmt.Errorf("%s: new session: %w", host.ID, err)
	}
	defer sess.Close()

	var stdout, stderr bytes.Buffer
	sess.Stdout = &stdout
	sess.Stderr = &stderr

	done := make(chan error, 1)
	go func() { done <- sess.Run(shellQuote(argv)) }()

	select {
	case <-ctx.Done():
		_ = sess.Signal(ssh.SIGKILL)
		_ = sess.Close()
		return Result{}, ctx.Err()
	case err := <-done:
		res := Result{Stdout: stdout.String(), Stderr: stderr.String()}
		var exit *ssh.ExitError
		switch {
		case err == nil:
			return res, nil
		case errors.As(err, &exit):
			res.ExitCode = exit.ExitStatus()
			return res, nil
		default:
			p.drop(host.ID)
			return res, err
		}
	}
}

// Stream runs argv and returns its combined output as a reader. Closing the
// reader kills the remote process. ctx cancellation also kills it.
func (p *Pool) Stream(ctx context.Context, host app.Host, argv []string) (io.ReadCloser, error) {
	client, err := p.client(ctx, host)
	if err != nil {
		return nil, err
	}
	sess, err := client.NewSession()
	if err != nil {
		p.drop(host.ID)
		return nil, err
	}
	stdout, err := sess.StdoutPipe()
	if err != nil {
		_ = sess.Close()
		return nil, err
	}
	sess.Stderr = sess.Stdout

	if err := sess.Start(shellQuote(argv)); err != nil {
		_ = sess.Close()
		p.drop(host.ID)
		return nil, err
	}

	rc := &streamReader{
		r:    stdout,
		done: make(chan struct{}),
		cancel: func() {
			_ = sess.Signal(ssh.SIGKILL)
			_ = sess.Close()
		},
	}
	go func() {
		select {
		case <-ctx.Done():
			_ = rc.Close()
		case <-rc.done:
		}
	}()
	go func() {
		_ = sess.Wait()
		_ = rc.Close()
	}()
	return rc, nil
}

type streamReader struct {
	r         io.Reader
	cancel    func()
	done      chan struct{}
	closeOnce sync.Once
}

func (s *streamReader) Read(p []byte) (int, error) { return s.r.Read(p) }

func (s *streamReader) Close() error {
	s.closeOnce.Do(func() {
		s.cancel()
		close(s.done)
	})
	return nil
}

// SFTP opens an SFTP client on the pooled connection. The caller must close it.
func (p *Pool) SFTP(ctx context.Context, host app.Host) (*sftp.Client, error) {
	client, err := p.client(ctx, host)
	if err != nil {
		return nil, err
	}
	return sftp.NewClient(client)
}

// Close tears down every pooled connection.
func (p *Pool) Close() error {
	p.mu.Lock()
	defer p.mu.Unlock()
	for id, c := range p.clients {
		_ = c.Close()
		delete(p.clients, id)
	}
	return nil
}

func (p *Pool) drop(hostID string) {
	p.mu.Lock()
	defer p.mu.Unlock()
	if c, ok := p.clients[hostID]; ok {
		_ = c.Close()
		delete(p.clients, hostID)
	}
}

// client returns the pooled connection for a host, dialing when needed.
func (p *Pool) client(ctx context.Context, host app.Host) (*ssh.Client, error) {
	p.mu.Lock()
	if c, ok := p.clients[host.ID]; ok {
		p.mu.Unlock()
		return c, nil
	}
	p.mu.Unlock()

	cfg, err := p.clientConfig(host)
	if err != nil {
		return nil, err
	}

	addr := resolveAddr(host, p.opts.SSHConfig)
	client, err := p.dial("tcp", addr, cfg)
	if err != nil {
		return nil, classify(err, host, p.opts.KnownHosts)
	}

	p.mu.Lock()
	p.clients[host.ID] = client
	p.mu.Unlock()
	return client, nil
}

// clientConfig builds auth and host-key verification.
func (p *Pool) clientConfig(host app.Host) (*ssh.ClientConfig, error) {
	auths, err := authMethods(host)
	if err != nil {
		return nil, err
	}

	var hostKey ssh.HostKeyCallback
	if p.opts.Insecure {
		hostKey = ssh.InsecureIgnoreHostKey() //nolint:gosec // explicit opt-in
	} else {
		if _, err := os.Stat(p.opts.KnownHosts); err != nil {
			return nil, fmt.Errorf("known_hosts %s is missing; ssh to the host once to record its key", p.opts.KnownHosts)
		}
		cb, err := knownhosts.New(p.opts.KnownHosts)
		if err != nil {
			return nil, fmt.Errorf("known_hosts %s: %w", p.opts.KnownHosts, err)
		}
		hostKey = cb
	}

	return &ssh.ClientConfig{
		User:            resolveUser(host),
		Auth:            auths,
		HostKeyCallback: hostKey,
		Timeout:         p.opts.Timeout,
	}, nil
}

func authMethods(host app.Host) ([]ssh.AuthMethod, error) {
	switch host.Auth {
	case app.AuthAgent, "":
		sock := os.Getenv("SSH_AUTH_SOCK")
		if sock == "" {
			return nil, errors.New("SSH_AUTH_SOCK is not set; start ssh-agent or use auth: key")
		}
		conn, err := net.Dial("unix", sock)
		if err != nil {
			return nil, fmt.Errorf("ssh-agent: %w", err)
		}
		ag := agent.NewClient(conn)
		return []ssh.AuthMethod{ssh.PublicKeysCallback(ag.Signers)}, nil

	case app.AuthKey:
		raw, err := os.ReadFile(expandHome(host.Key))
		if err != nil {
			return nil, fmt.Errorf("key %s: %w", host.Key, err)
		}
		signer, err := ssh.ParsePrivateKey(raw)
		if err != nil {
			return nil, fmt.Errorf("key %s: %w", host.Key, err)
		}
		return []ssh.AuthMethod{ssh.PublicKeys(signer)}, nil

	case app.AuthPassword:
		secret, err := keyring.Get(KeyringService, host.ID)
		if err != nil {
			return nil, fmt.Errorf("no keyring password for %s; run `wmdep auth set %s`", host.ID, host.ID)
		}
		return []ssh.AuthMethod{ssh.Password(secret)}, nil
	}
	return nil, fmt.Errorf("unknown auth %q", host.Auth)
}

// resolveUser applies ssh_config User= when the host entry does not set one.
func resolveUser(host app.Host) string {
	if host.User != "" {
		return host.User
	}
	return ssh_config.Get(host.ID, "User")
}

// resolveAddr applies ssh_config HostName/Port, falling back to the explicit
// address and port from config.
func resolveAddr(host app.Host, cfgPath string) string {
	address := host.Address
	port := host.Port
	if address == "" {
		address = ssh_config.Get(host.ID, "HostName")
	}
	if port == 0 {
		if p, err := ssh_config.GetStrict(host.ID, "Port"); err == nil {
			if n, convErr := parsePort(p); convErr == nil {
				port = n
			}
		}
	}
	if port == 0 {
		port = 22
	}
	_ = cfgPath
	return fmt.Sprintf("%s:%d", address, port)
}

func parsePort(s string) (int, error) {
	var n int
	_, err := fmt.Sscanf(s, "%d", &n)
	if err != nil || n < 1 || n > 65535 {
		return 0, fmt.Errorf("invalid port %q", s)
	}
	return n, nil
}

// classify turns dial errors into operator-readable ones, with a clear hint for
// an unknown host key.
func classify(err error, host app.Host, knownHosts string) error {
	var keyErr *knownhosts.KeyError
	if errors.As(err, &keyErr) {
		if len(keyErr.Want) == 0 {
			return fmt.Errorf("%w for %s; add it with `ssh %s` once, or set insecure: true in config",
				ErrUnknownHostKey, host.Addr(), host.Addr())
		}
		return fmt.Errorf("host key for %s changed (known_hosts: %s)", host.Addr(), knownHosts)
	}
	return fmt.Errorf("%s: %w", host.ID, err)
}

// shellQuote renders argv as a single shell command with each argument quoted,
// so user-provided values cannot inject shell syntax.
func shellQuote(argv []string) string {
	parts := make([]string, 0, len(argv))
	for _, a := range argv {
		parts = append(parts, "'"+strings.ReplaceAll(a, "'", `'\''`)+"'")
	}
	return strings.Join(parts, " ")
}

func expandHome(p string) string {
	if p == "~" || strings.HasPrefix(p, "~/") {
		if home, err := os.UserHomeDir(); err == nil {
			return filepath.Join(home, strings.TrimPrefix(p, "~/"))
		}
	}
	return p
}

// Upload copies a local file to the server atomically: write to a temp path in
// the same directory, then rename over the target.
func (p *Pool) Upload(ctx context.Context, host app.Host, local, remote string, mode os.FileMode) error {
	client, err := p.SFTP(ctx, host)
	if err != nil {
		return err
	}
	defer client.Close()

	src, err := os.Open(local)
	if err != nil {
		return err
	}
	defer src.Close()

	tmp := remote + ".wmdep-tmp"
	dst, err := client.OpenFile(tmp, os.O_WRONLY|os.O_CREATE|os.O_TRUNC)
	if err != nil {
		return err
	}
	if _, err := io.Copy(dst, src); err != nil {
		_ = dst.Close()
		_ = client.Remove(tmp)
		return err
	}
	if err := dst.Close(); err != nil {
		_ = client.Remove(tmp)
		return err
	}
	if mode != 0 {
		if err := client.Chmod(tmp, mode); err != nil {
			return err
		}
	}
	return client.Rename(tmp, remote)
}

// Download copies a remote file to a local path atomically.
func (p *Pool) Download(ctx context.Context, host app.Host, remote, local string) error {
	client, err := p.SFTP(ctx, host)
	if err != nil {
		return err
	}
	defer client.Close()

	src, err := client.Open(remote)
	if err != nil {
		return err
	}
	defer src.Close()

	tmp := local + ".wmdep-tmp"
	dst, err := os.Create(tmp)
	if err != nil {
		return err
	}
	if _, err := io.Copy(dst, src); err != nil {
		_ = dst.Close()
		_ = os.Remove(tmp)
		return err
	}
	if err := dst.Close(); err != nil {
		_ = os.Remove(tmp)
		return err
	}
	return os.Rename(tmp, local)
}

// ReadDir lists a remote directory, used by the SFTP browser.
func (p *Pool) ReadDir(ctx context.Context, host app.Host, dir string) ([]os.FileInfo, error) {
	client, err := p.SFTP(ctx, host)
	if err != nil {
		return nil, err
	}
	defer client.Close()
	return client.ReadDir(dir)
}
