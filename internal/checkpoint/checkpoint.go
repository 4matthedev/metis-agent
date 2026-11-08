// Package checkpoint records pre-images of files a turn is about to change so
// the user can undo the whole turn with one command.
package checkpoint

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

	"metis-agent/internal/config"
)

const (
	// maxFileBytes bounds a single backed-up file.
	maxFileBytes = 32 * 1024 * 1024
	// maxTurnBytes bounds all backups for one turn.
	maxTurnBytes = 256 * 1024 * 1024
	// keep is the number of completed checkpoints retained per project.
	keep = 30
)

// entry is one path captured before a mutation.
type entry struct {
	Path    string `json:"path"`
	Existed bool   `json:"existed"`
	Backup  string `json:"backup,omitempty"`
	Mode    uint32 `json:"mode,omitempty"`
	IsDir   bool   `json:"is_dir,omitempty"`
	IsLink  bool   `json:"is_link,omitempty"`
	Link    string `json:"link,omitempty"`
	Skipped string `json:"skipped,omitempty"`
}

// manifest is stored next to a checkpoint's backups.
type manifest struct {
	Created time.Time `json:"created"`
	Label   string    `json:"label,omitempty"`
	Entries []entry   `json:"entries"`
}

// Result describes an undo.
type Result struct {
	Directory string
	Restored  int
	Skipped   []string
}

// Manager captures snapshots for one project directory.
type Manager struct {
	root string
	mu   sync.Mutex
	turn *turn
}

type turn struct {
	dir     string
	label   string
	entries []entry
	seen    map[string]bool
	bytes   int64
}

// New returns a manager for workDir. Snapshots live under the platform data
// directory, scoped by a hash of the project path.
func New(workDir string) (*Manager, error) {
	base, err := config.DataDir()
	if err != nil {
		return nil, err
	}
	abs, err := filepath.Abs(workDir)
	if err != nil {
		return nil, err
	}
	sum := sha256.Sum256([]byte(filepath.Clean(abs)))
	root := filepath.Join(base, "checkpoints", hex.EncodeToString(sum[:])[:12])
	return &Manager{root: root}, nil
}

// Root returns the checkpoint directory for this project.
func (m *Manager) Root() string { return m.root }

// Begin starts a new turn. Any open turn is discarded.
func (m *Manager) Begin(label string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.turn != nil {
		_ = os.RemoveAll(m.turn.dir)
		m.turn = nil
	}
	if err := os.MkdirAll(m.root, 0o700); err != nil {
		return err
	}
	dir, err := os.MkdirTemp(m.root, ".turn-")
	if err != nil {
		return err
	}
	m.turn = &turn{dir: dir, label: label, seen: make(map[string]bool)}
	return nil
}

// Snapshot records the current state of path. Missing paths are recorded so
// undo can remove anything the turn created. Snapshot is a no-op when no turn
// is active, so it is safe for tools to call unconditionally.
func (m *Manager) Snapshot(path string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.turn == nil || strings.TrimSpace(path) == "" {
		return
	}
	abs, err := filepath.Abs(path)
	if err != nil {
		return
	}
	abs = filepath.Clean(abs)
	if m.turn.seen[abs] {
		return
	}
	m.turn.seen[abs] = true

	info, err := os.Lstat(abs)
	if err != nil {
		m.turn.entries = append(m.turn.entries, entry{Path: abs})
		return
	}
	e := entry{Path: abs, Existed: true, Mode: uint32(info.Mode().Perm())}
	switch {
	case info.Mode()&os.ModeSymlink != 0:
		target, err := os.Readlink(abs)
		if err != nil {
			e.Existed = false
		} else {
			e.IsLink = true
			e.Link = target
		}
	case info.IsDir():
		e.IsDir = true
		name := fmt.Sprintf("%d", len(m.turn.entries))
		backup := filepath.Join(m.turn.dir, name)
		if err := copyTree(abs, backup, m.turn); err != nil {
			e.Skipped = err.Error()
		} else {
			// Store the backup relative to the checkpoint directory: the
			// staging directory is renamed when the turn ends.
			e.Backup = name
		}
	default:
		if info.Size() > maxFileBytes {
			e.Skipped = fmt.Sprintf("file is too large to snapshot (%d bytes)", info.Size())
			break
		}
		if m.turn.bytes+info.Size() > maxTurnBytes {
			e.Skipped = "turn snapshot budget exceeded"
			break
		}
		name := fmt.Sprintf("%d", len(m.turn.entries))
		backup := filepath.Join(m.turn.dir, name)
		if err := copyFile(abs, backup, info.Mode().Perm()); err != nil {
			e.Skipped = err.Error()
		} else {
			e.Backup = name
			m.turn.bytes += info.Size()
		}
	}
	m.turn.entries = append(m.turn.entries, e)
}

// End finishes the current turn, keeping it only when it captured something.
func (m *Manager) End() error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.turn == nil {
		return nil
	}
	t := m.turn
	m.turn = nil
	if len(t.entries) == 0 {
		return os.RemoveAll(t.dir)
	}
	b, err := json.MarshalIndent(manifest{Created: time.Now().UTC(), Label: t.label, Entries: t.entries}, "", "  ")
	if err != nil {
		_ = os.RemoveAll(t.dir)
		return err
	}
	b = append(b, '\n')
	if err := os.WriteFile(filepath.Join(t.dir, "manifest.json"), b, 0o600); err != nil {
		_ = os.RemoveAll(t.dir)
		return err
	}
	final := filepath.Join(m.root, time.Now().UTC().Format("20060102-150405.000000000"))
	if err := os.Rename(t.dir, final); err != nil {
		_ = os.RemoveAll(t.dir)
		return err
	}
	m.prune()
	return nil
}

func (m *Manager) prune() {
	dirs, _ := m.listDirs()
	if len(dirs) <= keep {
		return
	}
	for _, d := range dirs[:len(dirs)-keep] {
		_ = os.RemoveAll(d)
	}
}

// Undo restores the newest checkpoint and removes it.
func (m *Manager) Undo() (Result, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.turn != nil {
		_ = os.RemoveAll(m.turn.dir)
		m.turn = nil
	}
	dirs, err := m.listDirs()
	if err != nil {
		return Result{}, err
	}
	if len(dirs) == 0 {
		return Result{}, errors.New("nothing to undo")
	}
	dir := dirs[len(dirs)-1]
	b, err := os.ReadFile(filepath.Join(dir, "manifest.json"))
	if err != nil {
		return Result{}, fmt.Errorf("read checkpoint: %w", err)
	}
	var man manifest
	if err := json.Unmarshal(b, &man); err != nil {
		return Result{}, fmt.Errorf("parse checkpoint: %w", err)
	}

	res := Result{Directory: dir}
	for i := len(man.Entries) - 1; i >= 0; i-- {
		e := man.Entries[i]
		if e.Skipped != "" {
			res.Skipped = append(res.Skipped, fmt.Sprintf("%s: %s", e.Path, e.Skipped))
			continue
		}
		if err := restore(e, dir); err != nil {
			return res, fmt.Errorf("restore %s: %w", e.Path, err)
		}
		if e.Existed {
			res.Restored++
		}
	}
	if err := os.RemoveAll(dir); err != nil {
		return res, err
	}
	return res, nil
}

// listDirs returns completed checkpoint directories, oldest first.
func (m *Manager) listDirs() ([]string, error) {
	entries, err := os.ReadDir(m.root)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil, nil
		}
		return nil, err
	}
	var dirs []string
	for _, e := range entries {
		if !e.IsDir() || strings.HasPrefix(e.Name(), ".") {
			continue
		}
		dirs = append(dirs, filepath.Join(m.root, e.Name()))
	}
	sort.Strings(dirs)
	return dirs, nil
}

func restore(e entry, dir string) error {
	if !e.Existed {
		if err := os.RemoveAll(e.Path); err != nil && !errors.Is(err, os.ErrNotExist) {
			return err
		}
		return nil
	}
	if e.IsLink {
		_ = os.RemoveAll(e.Path)
		if err := os.MkdirAll(filepath.Dir(e.Path), 0o755); err != nil {
			return err
		}
		return os.Symlink(e.Link, e.Path)
	}
	if e.Backup == "" {
		return fmt.Errorf("backup missing from checkpoint")
	}
	backup := filepath.Join(dir, e.Backup)
	if e.IsDir {
		if err := os.RemoveAll(e.Path); err != nil {
			return err
		}
		return copyTreePlain(backup, e.Path, os.FileMode(e.Mode))
	}
	if err := os.MkdirAll(filepath.Dir(e.Path), 0o755); err != nil {
		return err
	}
	return copyFile(backup, e.Path, os.FileMode(e.Mode))
}

func copyFile(src, dst string, mode os.FileMode) error {
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer in.Close()
	if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil {
		return err
	}
	out, err := os.OpenFile(dst, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, mode)
	if err != nil {
		return err
	}
	if _, err := io.Copy(out, in); err != nil {
		out.Close()
		return err
	}
	return out.Close()
}

func copyTree(src, dst string, t *turn) error {
	return filepath.WalkDir(src, func(p string, d os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		rel, err := filepath.Rel(src, p)
		if err != nil {
			return err
		}
		target := filepath.Join(dst, rel)
		info, err := d.Info()
		if err != nil {
			return err
		}
		switch {
		case info.Mode()&os.ModeSymlink != 0:
			link, err := os.Readlink(p)
			if err != nil {
				return err
			}
			if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
				return err
			}
			return os.Symlink(link, target)
		case d.IsDir():
			return os.MkdirAll(target, info.Mode().Perm())
		default:
			if info.Size() > maxFileBytes {
				return fmt.Errorf("file is too large to snapshot: %s", p)
			}
			if t.bytes+info.Size() > maxTurnBytes {
				return fmt.Errorf("turn snapshot budget exceeded")
			}
			if err := copyFile(p, target, info.Mode().Perm()); err != nil {
				return err
			}
			t.bytes += info.Size()
			return nil
		}
	})
}

func copyTreePlain(src, dst string, mode os.FileMode) error {
	return filepath.WalkDir(src, func(p string, d os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		rel, err := filepath.Rel(src, p)
		if err != nil {
			return err
		}
		target := filepath.Join(dst, rel)
		info, err := d.Info()
		if err != nil {
			return err
		}
		switch {
		case info.Mode()&os.ModeSymlink != 0:
			link, err := os.Readlink(p)
			if err != nil {
				return err
			}
			return os.Symlink(link, target)
		case d.IsDir():
			m := info.Mode().Perm()
			if rel == "." && mode != 0 {
				m = mode
			}
			return os.MkdirAll(target, m)
		default:
			return copyFile(p, target, info.Mode().Perm())
		}
	})
}

// randomSuffix is kept for tests that need a unique checkpoint name.
func randomSuffix() string {
	b := make([]byte, 4)
	_, _ = rand.Read(b)
	return hex.EncodeToString(b)
}
