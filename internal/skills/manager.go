package skills

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
)

// Manager discovers and manages skills. It loads SKILL.md files from a set of
// search paths:
//
//   - project:  <dir>/.metis-agent/skills/        (per-project skills)
//   - extra:    $METIS_SKILL_PATHS (colon-separated extra dirs)
//   - global:   ~/.agents/skills/                  (user-level)
//
// A skill is the folder's SKILL.md. The manager caches results so repeated
// calls (e.g. every /skills or system-prompt build) are cheap.
type Manager struct {
	mu         sync.RWMutex
	projectDir string
	dirs       []dir
	skills     []Skill
}

// dirKind tags which source a skill came from.
type dirKind byte

const (
	kindProject dirKind = iota
	kindExtra
	kindGlobal
)

type dir struct {
	path string
	kind dirKind
}

// NewManager builds a Manager for a project directory.
func NewManager(dir string) *Manager {
	m := &Manager{projectDir: dir}
	m.reload()
	return m
}

// buildDirs returns the search paths in priority order (project > extra > global).
// It re-checks for dir existence each time so newly created project skill dirs
// are picked up after Invalidate.
func buildDirs(projectDir string) []dir {
	var ds []dir
	// project-local
	p := filepath.Join(projectDir, ".metis-agent", "skills")
	if _, err := os.Stat(p); err == nil {
		ds = append(ds, dir{path: p, kind: kindProject})
	}
	// extra dirs from env
	if ev := os.Getenv("METIS_SKILL_PATHS"); ev != "" {
		for _, p := range strings.Split(ev, ":") {
			p = strings.TrimSpace(p)
			if p == "" {
				continue
			}
			if _, err := os.Stat(p); err == nil {
				ds = append(ds, dir{path: p, kind: kindExtra})
			}
		}
	}
	// global (user-level)
	g := os.Getenv("METIS_GLOBAL_SKILLS_DIR")
	if g == "" {
		home, _ := os.UserHomeDir()
		g = filepath.Join(home, ".agents", "skills")
	}
	if _, err := os.Stat(g); err == nil {
		ds = append(ds, dir{path: g, kind: kindGlobal})
	}
	return ds
}

// reload reads all skills from the dirs into m.skills. Must hold the write lock.
func (m *Manager) reload() {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.dirs = buildDirs(m.projectDir)
	var out []Skill
	for _, d := range m.dirs {
		entries, err := os.ReadDir(d.path)
		if err != nil {
			continue
		}
		for _, e := range entries {
			if !e.IsDir() {
				continue
			}
			path := filepath.Join(d.path, e.Name(), "SKILL.md")
			sk, err := ParseSkill(path)
			if err != nil {
				continue
			}
			sk.Kind = kindName(d.kind)
			out = append(out, sk)
		}
	}
	sort.Slice(out, func(i, j int) bool {
		return strings.ToLower(out[i].Name) < strings.ToLower(out[j].Name)
	})
	m.skills = out
}

// Invalidate re-reads all skill directories (call after adding/removing files).
func (m *Manager) Invalidate() {
	m.reload()
}

// List returns all discovered skills, sorted by name.
func (m *Manager) List(ctx context.Context) []Skill {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.skills
}

// Get returns a skill by name (case-insensitive), or false if not found.
func (m *Manager) Get(ctx context.Context, name string) (Skill, bool) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	for _, s := range m.skills {
		if strings.EqualFold(s.Name, name) {
			return s, true
		}
	}
	return Skill{}, false
}

// kindName maps dirKind to a display string.
func kindName(k dirKind) string {
	switch k {
	case kindProject:
		return "project"
	case kindExtra:
		return "extra"
	default:
		return "global"
	}
}

// Command is the shared handler for the /skills slash command and the skills
// tool.
//
//	/skills                      -> list
//	/skills list                 -> list
//	/skills info <name>          -> full text of a skill
//	/skills use <name>           -> load the skill into the caller's context
//	/skills run <name> [args...] -> run the skill's declared command (approval-gated)
//
// Returns a string.
func (m *Manager) Command(ctx context.Context, fields []string) string {
	if m == nil {
		return "no skill manager is available"
	}
	if len(fields) == 0 {
		return m.listText(ctx)
	}
	// Drop the leading "/skills" command token if present. Real callers pass
	// ["/skills", sub, ...]; the tool may pass ["skills", sub, ...] or [sub, ...].
	sub := ""
	args := []string{}
	found := false
	for i, f := range fields {
		l := strings.ToLower(strings.TrimSpace(f))
		if l == "/skills" || l == "skills" || l == "" {
			continue
		}
		sub = l
		args = fields[i+1:]
		found = true
		break
	}
	if !found {
		return m.listText(ctx)
	}
	if sub == "" {
		return m.listText(ctx)
	}
	args = args[0:]
	switch sub {
	case "list", "ls":
		return m.listText(ctx)
	case "info", "show", "cat":
		if len(args) == 0 {
			return "usage: /skills info <name>"
		}
		sk, ok := m.Get(ctx, args[0])
		if !ok {
			return fmt.Sprintf("no skill %q (try /skills list)", args[0])
		}
		return sk.FullText()
	case "use":
		if len(args) == 0 {
			return "usage: /skills use <name>"
		}
		sk, ok := m.Get(ctx, args[0])
		if !ok {
			return fmt.Sprintf("no skill %q (try /skills list)", args[0])
		}
		return "skill " + sk.Name + " loaded into context. Its declared command is:\n  " + sk.Run + "\n\n" + sk.Body
	case "run":
		if len(args) == 0 {
			return "usage: /skills run <name> [args...]"
		}
		cmd, ok := m.RunCommand(ctx, args)
		if !ok {
			return cmd
		}
		return cmd
	default:
		return "unknown skills subcommand " + sub + " (try list/info/use/run)"
	}
}

// listText renders the current skill list.
func (m *Manager) listText(ctx context.Context) string {
	sks := m.List(ctx)
	if len(sks) == 0 {
		return "no skills found (searching: " + strings.Join(searchPaths(m), ", ") + ")"
	}
	var b strings.Builder
	fmt.Fprintf(&b, "%d skills\n", len(sks))
	for _, sk := range sks {
		fmt.Fprintf(&b, "  %s", sk.String())
	}
	return strings.TrimRight(b.String(), "\n")
}

// searchPaths returns the list of search dirs.
func searchPaths(m *Manager) []string {
	var ps []string
	m.mu.RLock()
	defer m.mu.RUnlock()
	for _, d := range m.dirs {
		ps = append(ps, d.path)
	}
	return ps
}

// RunCommand builds the command to execute and reports whether it's a real
// command (true) or an error string (false). The caller (the skills tool or
// REPL) runs it through the registry's shell tool, which invokes the approval
// gate. The error string is returned for display when bool is false.
func (m *Manager) RunCommand(ctx context.Context, args []string) (string, bool) {
	sk, ok := m.Get(ctx, args[0])
	if !ok {
		return fmt.Sprintf("no skill %q (try /skills list)", args[0]), false
	}
	if sk.Run == "" {
		return fmt.Sprintf("skill %q has no declared run command", sk.Name), false
	}
	cmd := sk.Run
	if len(args) > 1 {
		cmd = sk.Run + " " + strings.Join(args[1:], " ")
	}
	return cmd, true
}

// HasRunCommand reports whether the named skill declares a run command.
func (m *Manager) HasRunCommand(ctx context.Context, name string) bool {
	sk, ok := m.Get(ctx, name)
	return ok && sk.Run != ""
}
