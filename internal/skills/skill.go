package skills

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// Skill is a single discovered skill: a SKILL.md file with a small
// YAML-frontmatter header (name, description, and an optional run command)
// followed by a markdown body.
//
// Format (matches the open agent-skills / .agents/skills convention):
//
//	---
//	name: my-skill
//	description: What it does, when to use it.
//	run: npx skills add something   # optional command the skill "runs"
//	---
//
//	## Body
//
//	Reference material the agent can consult.
type Skill struct {
	Name        string // frontmatter name, or directory name
	Description string
	Run         string // optional command declared by the skill
	Body        string // the markdown body after the frontmatter
	Path        string // absolute path to the SKILL.md
	Kind        string // "project", "global", or "extra"
}

// ParseSkill reads a SKILL.md file and returns its parsed content.
func ParseSkill(path string) (Skill, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return Skill{}, err
	}

	s := strings.TrimSpace(string(data))
	sk := Skill{}

	if !strings.HasPrefix(s, "---") {
		// No frontmatter: the whole file is the body.
		sk.Body = s
		sk.Path = path
		sk.Name = filepath.Base(filepath.Dir(path))
		return sk, nil
	}

	// Frontmatter is delimited by an opening --- and a closing ---.
	rest := s[3:]
	if idx := strings.Index(rest, "---"); idx >= 0 {
		header := rest[:idx]
		body := rest[idx+len("---"):]
		sk.Body = strings.TrimLeft(body, "\r\n")

		for _, line := range strings.Split(header, "\n") {
			key, val := splitKV(line)
			switch key {
			case "name":
				sk.Name = val
			case "description":
				sk.Description = val
			case "run":
				sk.Run = val
			}
		}
	} else {
		// Opening --- with no closing ---; treat whole as body.
		sk.Body = s
		sk.Path = path
		sk.Name = filepath.Base(filepath.Dir(path))
		return sk, nil
	}

	sk.Path = path
	if sk.Name == "" {
		sk.Name = filepath.Base(filepath.Dir(path))
	}
	return sk, nil
}

// splitKV parses a "key: value" frontmatter line, tolerating comments and
// trailing whitespace. Returns ("", "") for lines that aren't key/value.
func splitKV(line string) (key, val string) {
	line = strings.TrimSpace(line)
	if line == "" || strings.HasPrefix(line, "#") {
		return "", ""
	}
	idx := strings.Index(line, ":")
	if idx < 0 {
		return "", ""
	}
	key = strings.TrimSpace(line[:idx])
	val = strings.TrimSpace(line[idx+1:])
	// Strip an inline comment (starts with #, preceded by space).
	if ci := strings.Index(val, " #"); ci >= 0 {
		val = strings.TrimSpace(val[:ci])
	}
	return key, val
}

// SkillListFormat renders a skill into the same shape the system prompt and
// /skills command print, for consistent display.
func (sk Skill) String() string {
	var b strings.Builder
	fmt.Fprintf(&b, "- %s: %s", sk.Name, sk.Description)
	if sk.Run != "" {
		fmt.Fprintf(&b, " (run: %s)", sk.Run)
	}
	fmt.Fprintf(&b, " [%s]", sk.Kind)
	return b.String()
}

func (sk Skill) FullText() string {
	var b strings.Builder
	fmt.Fprintf(&b, "## Skill: %s (%s)\n\n", sk.Name, sk.Kind)
	if sk.Description != "" {
		fmt.Fprintf(&b, "%s\n\n", sk.Description)
	}
	if sk.Run != "" {
		fmt.Fprintf(&b, "Declared command (runs through the approval gate):\n%s\n\n", sk.Run)
	}
	b.WriteString(sk.Body)
	return b.String()
}

// ErrNotConfigured is returned when no skill directories are configured.
var ErrNoSkillDirs = fmt.Errorf("no skill directories configured")
