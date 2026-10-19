package config

import (
	"crypto/sha256"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"gopkg.in/yaml.v3"
)

// ErrStaleFile is returned when a file changed on disk after it was loaded.
// The caller should reload before retrying, so an external edit is never
// silently overwritten.
var ErrStaleFile = errors.New("file changed on disk")

// Writer mutates the YAML files in place. It edits the parsed node tree rather
// than re-emitting a whole config, so comments and key order survive.
//
// Every save is: lock directory, verify content stamp, mutate nodes, write temp
// file + fsync + rename, keep a .bak, then validate the whole directory and
// roll back if the result would not load.
type Writer struct {
	dir    string
	stamps map[string]fileStamp
}

// NewWriter returns a writer bound to the bundle's directory and load stamps.
func NewWriter(b *Bundle) *Writer {
	stamps := make(map[string]fileStamp, len(b.stamps))
	for k, v := range b.stamps {
		stamps[k] = v
	}
	return &Writer{dir: b.Dir, stamps: stamps}
}

// UpsertDeployment adds an app to an environment file, or replaces the entry
// whose id is originalID. When originalID is empty the app is appended.
func (w *Writer) UpsertDeployment(env, originalID string, spec AppSpec) error {
	path := filepath.Join(LayoutFor(w.dir).Environments, env+".yaml")
	return w.withLock(func() error {
		if err := w.checkStamp(path); err != nil {
			return err
		}
		doc, err := loadNode(path)
		if err != nil {
			return err
		}
		root := docRoot(doc)

		apps := mappingGet(root, "apps")
		if apps == nil || apps.Kind != yaml.SequenceNode {
			apps = &yaml.Node{Kind: yaml.SequenceNode, Tag: "!!seq"}
			mappingSet(root, "apps", apps)
		}

		node, err := nodeFor(spec)
		if err != nil {
			return err
		}

		replaced := false
		if originalID != "" {
			for i, item := range apps.Content {
				if scalarField(item, "id") == originalID {
					apps.Content[i] = node
					replaced = true
					break
				}
			}
		}
		if !replaced {
			apps.Content = append(apps.Content, node)
		}

		ensureEnvHost(root, spec.Host)

		if err := writeAtomic(path, doc); err != nil {
			return err
		}
		return w.validate(path)
	})
}

// DeleteDeployment removes the app with the given id from an environment.
func (w *Writer) DeleteDeployment(env, id string) error {
	path := filepath.Join(LayoutFor(w.dir).Environments, env+".yaml")
	return w.withLock(func() error {
		if err := w.checkStamp(path); err != nil {
			return err
		}
		doc, err := loadNode(path)
		if err != nil {
			return err
		}
		apps := mappingGet(docRoot(doc), "apps")
		if apps == nil || apps.Kind != yaml.SequenceNode {
			return fmt.Errorf("app %q not found in %s", id, env)
		}
		for i, item := range apps.Content {
			if scalarField(item, "id") == id {
				apps.Content = append(apps.Content[:i], apps.Content[i+1:]...)
				if err := writeAtomic(path, doc); err != nil {
					return err
				}
				return w.validate(path)
			}
		}
		return fmt.Errorf("app %q not found in %s", id, env)
	})
}

// UpsertHost adds or replaces a host in hosts.yaml. Renaming is supported by
// passing the old id in originalID.
func (w *Writer) UpsertHost(id, originalID string, spec HostSpec) error {
	path := LayoutFor(w.dir).Hosts
	return w.withLock(func() error {
		if err := w.checkStamp(path); err != nil {
			return err
		}
		doc, err := loadNode(path)
		if err != nil {
			return err
		}
		root := docRoot(doc)
		hosts := mappingGet(root, "hosts")
		if hosts == nil || hosts.Kind != yaml.MappingNode {
			hosts = &yaml.Node{Kind: yaml.MappingNode, Tag: "!!map"}
			mappingSet(root, "hosts", hosts)
		}
		node, err := nodeFor(spec)
		if err != nil {
			return err
		}
		if originalID != "" && originalID != id {
			mappingDelete(hosts, originalID)
		}
		mappingSet(hosts, id, node)
		if err := writeAtomic(path, doc); err != nil {
			return err
		}
		return w.validate(path)
	})
}

// DeleteHost removes a host entry. Hosts still referenced by a deployment are
// rejected by validation, and the write is rolled back.
func (w *Writer) DeleteHost(id string) error {
	path := LayoutFor(w.dir).Hosts
	return w.withLock(func() error {
		if err := w.checkStamp(path); err != nil {
			return err
		}
		doc, err := loadNode(path)
		if err != nil {
			return err
		}
		hosts := mappingGet(docRoot(doc), "hosts")
		if hosts == nil || mappingGet(hosts, id) == nil {
			return fmt.Errorf("host %q not found", id)
		}
		mappingDelete(hosts, id)
		if err := writeAtomic(path, doc); err != nil {
			return err
		}
		return w.validate(path)
	})
}

// withLock serialises writes across wmdep instances sharing a config dir.
func (w *Writer) withLock(fn func() error) error {
	lock := filepath.Join(w.dir, ".wmdep.lock")
	f, err := os.OpenFile(lock, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o600)
	if err != nil {
		return fmt.Errorf("another wmdep instance is writing the config (%s)", lock)
	}
	defer func() {
		_ = f.Close()
		_ = os.Remove(lock)
	}()
	return fn()
}

func (w *Writer) checkStamp(path string) error {
	want, ok := w.stamps[path]
	if !ok {
		return nil
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	if sha256.Sum256(raw) != want.sum {
		return fmt.Errorf("%w: %s; reload the config and try again", ErrStaleFile, filepath.Base(path))
	}
	return nil
}

// validate reloads the directory and restores the backup if the change would
// make it invalid.
func (w *Writer) validate(path string) error {
	_, diags := Load(w.dir, Options{})
	var msgs []string
	for _, d := range diags {
		if d.Severity == "error" {
			msgs = append(msgs, d.Error())
		}
	}
	if len(msgs) == 0 {
		return nil
	}
	if old, err := os.ReadFile(path + ".bak"); err == nil {
		_ = os.WriteFile(path, old, 0o600)
	}
	return fmt.Errorf("change would make the config invalid (rolled back):\n%s", strings.Join(msgs, "\n"))
}

func loadNode(path string) (*yaml.Node, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var doc yaml.Node
	if err := yaml.Unmarshal(raw, &doc); err != nil {
		return nil, fmt.Errorf("%s: %w", path, err)
	}
	return &doc, nil
}

// writeAtomic writes via a temp file in the same directory, keeps a .bak, and
// renames into place only after the bytes are durable.
func writeAtomic(path string, doc *yaml.Node) error {
	out, err := yaml.Marshal(doc)
	if err != nil {
		return err
	}
	if old, err := os.ReadFile(path); err == nil {
		if err := os.WriteFile(path+".bak", old, 0o600); err != nil {
			return err
		}
	}
	tmp, err := os.CreateTemp(filepath.Dir(path), ".wmdep-*.tmp")
	if err != nil {
		return err
	}
	tmpName := tmp.Name()
	defer func() { _ = os.Remove(tmpName) }()

	if _, err := tmp.Write(out); err != nil {
		_ = tmp.Close()
		return err
	}
	if err := tmp.Sync(); err != nil {
		_ = tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	return os.Rename(tmpName, path)
}

func docRoot(doc *yaml.Node) *yaml.Node {
	if doc.Kind == yaml.DocumentNode && len(doc.Content) > 0 {
		return doc.Content[0]
	}
	return doc
}

func mappingGet(m *yaml.Node, key string) *yaml.Node {
	if m == nil || m.Kind != yaml.MappingNode {
		return nil
	}
	for i := 0; i+1 < len(m.Content); i += 2 {
		if m.Content[i].Value == key {
			return m.Content[i+1]
		}
	}
	return nil
}

func mappingSet(m *yaml.Node, key string, val *yaml.Node) {
	for i := 0; i+1 < len(m.Content); i += 2 {
		if m.Content[i].Value == key {
			m.Content[i+1] = val
			return
		}
	}
	keyNode := &yaml.Node{Kind: yaml.ScalarNode, Tag: "!!str", Value: key}
	m.Content = append(m.Content, keyNode, val)
}

func mappingDelete(m *yaml.Node, key string) {
	for i := 0; i+1 < len(m.Content); i += 2 {
		if m.Content[i].Value == key {
			m.Content = append(m.Content[:i], m.Content[i+2:]...)
			return
		}
	}
}

// scalarField returns a scalar field's value inside a mapping node.
func scalarField(n *yaml.Node, key string) string {
	v := mappingGet(n, key)
	if v == nil {
		return ""
	}
	return v.Value
}

// ensureEnvHost keeps the environment's host list in step with the apps it
// contains, so adding a deployment to a new host is one step.
func ensureEnvHost(root *yaml.Node, host string) {
	if host == "" {
		return
	}
	hosts := mappingGet(root, "hosts")
	if hosts == nil || hosts.Kind != yaml.SequenceNode {
		if hosts != nil && hosts.Kind == yaml.ScalarNode && hosts.Value == host {
			return
		}
		hosts = &yaml.Node{Kind: yaml.SequenceNode, Tag: "!!seq"}
		mappingSet(root, "hosts", hosts)
	}
	for _, h := range hosts.Content {
		if h.Value == host {
			return
		}
	}
	hosts.Content = append(hosts.Content, &yaml.Node{Kind: yaml.ScalarNode, Tag: "!!str", Value: host})
}

func nodeFor(v any) (*yaml.Node, error) {
	var n yaml.Node
	if err := n.Encode(v); err != nil {
		return nil, err
	}
	return &n, nil
}
