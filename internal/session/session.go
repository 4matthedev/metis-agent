// Package session persists conversation history in per-project files.
package session

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"runtime"
	"sort"
	"strings"
	"time"

	"metis-agent/internal/config"
	"metis-agent/internal/llm"
)

const fileVersion = 1

var validID = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._-]{0,63}$`)

// Session is one persisted conversation.
type Session struct {
	Version   int           `json:"version"`
	ID        string        `json:"id"`
	CreatedAt time.Time     `json:"created_at"`
	UpdatedAt time.Time     `json:"updated_at"`
	Provider  string        `json:"provider,omitempty"`
	Model     string        `json:"model,omitempty"`
	WorkDir   string        `json:"workdir"`
	Messages  []llm.Message `json:"messages"`

	path string
}

// Info is the lightweight listing shape used by /sessions.
type Info struct {
	ID        string
	UpdatedAt time.Time
	Provider  string
	Model     string
	Preview   string
	Path      string
}

// Store manages sessions under a root directory.
type Store struct {
	root string
}

// DefaultStore uses the platform data directory.
func DefaultStore() (*Store, error) {
	dir, err := config.DataDir()
	if err != nil {
		return nil, err
	}
	return NewStore(filepath.Join(dir, "sessions")), nil
}

// NewStore creates a store rooted at dir.
func NewStore(dir string) *Store {
	return &Store{root: dir}
}

// Root returns the store root.
func (s *Store) Root() string { return s.root }

// New creates an unsaved session with a generated ID.
func (s *Store) New(workDir, provider, model string) *Session {
	now := time.Now().UTC()
	id := now.Format("20060102-150405") + "-" + randomHex(3)
	return &Session{
		Version:   fileVersion,
		ID:        id,
		CreatedAt: now,
		UpdatedAt: now,
		Provider:  provider,
		Model:     model,
		WorkDir:   workDir,
	}
}

// NewNamed creates an unsaved named session.
func (s *Store) NewNamed(name, workDir, provider, model string) (*Session, error) {
	if !validID.MatchString(name) {
		return nil, fmt.Errorf("invalid session name %q (use letters, digits, dots, dashes, underscores)", name)
	}
	now := time.Now().UTC()
	return &Session{
		Version:   fileVersion,
		ID:        name,
		CreatedAt: now,
		UpdatedAt: now,
		Provider:  provider,
		Model:     model,
		WorkDir:   workDir,
	}, nil
}

// Load reads a session by ID for workDir.
func (s *Store) Load(workDir, id string) (*Session, error) {
	if !validID.MatchString(id) {
		return nil, fmt.Errorf("invalid session id %q", id)
	}
	path := filepath.Join(s.projectDir(workDir), id+".json")
	b, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var sess Session
	if err := json.Unmarshal(b, &sess); err != nil {
		return nil, fmt.Errorf("parse session %s: %w", path, err)
	}
	sess.path = path
	return &sess, nil
}

// Latest returns the most recently updated session for workDir.
func (s *Store) Latest(workDir string) (*Session, error) {
	infos, err := s.List(workDir, 1)
	if err != nil {
		return nil, err
	}
	if len(infos) == 0 {
		return nil, os.ErrNotExist
	}
	return s.Load(workDir, infos[0].ID)
}

// List returns recent sessions for workDir, newest first.
func (s *Store) List(workDir string, limit int) ([]Info, error) {
	dir := s.projectDir(workDir)
	entries, err := os.ReadDir(dir)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}
	var infos []Info
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".json") {
			continue
		}
		path := filepath.Join(dir, e.Name())
		b, err := os.ReadFile(path)
		if err != nil {
			continue
		}
		var sess Session
		if err := json.Unmarshal(b, &sess); err != nil || sess.ID == "" {
			continue
		}
		infos = append(infos, Info{
			ID:        sess.ID,
			UpdatedAt: sess.UpdatedAt,
			Provider:  sess.Provider,
			Model:     sess.Model,
			Preview:   preview(sess.Messages),
			Path:      path,
		})
	}
	sort.Slice(infos, func(i, j int) bool {
		return infos[i].UpdatedAt.After(infos[j].UpdatedAt)
	})
	if limit > 0 && len(infos) > limit {
		infos = infos[:limit]
	}
	return infos, nil
}

// Save writes the session atomically.
func (s *Store) Save(sess *Session) error {
	if sess == nil {
		return fmt.Errorf("session is nil")
	}
	if !validID.MatchString(sess.ID) {
		return fmt.Errorf("invalid session id %q", sess.ID)
	}
	if strings.TrimSpace(sess.WorkDir) == "" {
		return fmt.Errorf("session workdir is empty")
	}
	if sess.Version == 0 {
		sess.Version = fileVersion
	}
	if sess.CreatedAt.IsZero() {
		sess.CreatedAt = time.Now().UTC()
	}
	sess.UpdatedAt = time.Now().UTC()

	dir := s.projectDir(sess.WorkDir)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return err
	}
	b, err := json.MarshalIndent(sess, "", "  ")
	if err != nil {
		return err
	}
	b = append(b, '\n')

	tmp, err := os.CreateTemp(dir, ".session-*.tmp")
	if err != nil {
		return err
	}
	tmpName := tmp.Name()
	defer os.Remove(tmpName)
	if err := tmp.Chmod(0o600); err != nil {
		tmp.Close()
		return err
	}
	if _, err := tmp.Write(b); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Sync(); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}

	target := filepath.Join(dir, sess.ID+".json")
	if err := os.Rename(tmpName, target); err != nil {
		// Windows cannot always replace an existing file with Rename.
		if runtime.GOOS == "windows" {
			_ = os.Remove(target)
			if err := os.Rename(tmpName, target); err == nil {
				sess.path = target
				return nil
			}
		}
		return err
	}
	sess.path = target
	return nil
}

// Path returns the on-disk path of a loaded or saved session.
func (s *Store) Path(sess *Session) string {
	if sess == nil {
		return ""
	}
	if sess.path != "" {
		return sess.path
	}
	return filepath.Join(s.projectDir(sess.WorkDir), sess.ID+".json")
}

func (s *Store) projectDir(workDir string) string {
	return filepath.Join(s.root, projectHash(workDir))
}

func projectHash(workDir string) string {
	abs, err := filepath.Abs(workDir)
	if err != nil {
		abs = workDir
	}
	abs = filepath.Clean(abs)
	if runtime.GOOS == "windows" {
		abs = strings.ToLower(abs)
	}
	sum := sha256.Sum256([]byte(abs))
	return hex.EncodeToString(sum[:])[:12]
}

func randomHex(n int) string {
	b := make([]byte, n)
	if _, err := rand.Read(b); err != nil {
		return fmt.Sprintf("%d", time.Now().UnixNano()%1000000)
	}
	return hex.EncodeToString(b)
}

func preview(messages []llm.Message) string {
	for _, m := range messages {
		if m.Role != "user" {
			continue
		}
		s := strings.Join(strings.Fields(m.Content), " ")
		if len(s) > 72 {
			s = s[:72] + "…"
		}
		return s
	}
	return ""
}
