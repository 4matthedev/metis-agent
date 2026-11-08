package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

type manifestInfo struct {
	path string
	kind string
}

var knownManifests = []manifestInfo{
	{"go.mod", "Go"},
	{"go.sum", ""},
	{"package.json", "Node.js"},
	{"pnpm-lock.yaml", ""},
	{"yarn.lock", ""},
	{"package-lock.json", ""},
	{"tsconfig.json", "TypeScript"},
	{"pyproject.toml", "Python"},
	{"requirements.txt", "Python"},
	{"setup.py", "Python"},
	{"setup.cfg", "Python"},
	{"Pipfile", "Python"},
	{"poetry.lock", ""},
	{"Cargo.toml", "Rust"},
	{"pom.xml", "Java"},
	{"build.gradle", "Java/Kotlin"},
	{"build.gradle.kts", "Java/Kotlin"},
	{"settings.gradle", ""},
	{"Gemfile", "Ruby"},
	{"composer.json", "PHP"},
	{"CMakeLists.txt", "C/C++"},
	{"Makefile", "Make"},
	{"meson.build", "C/C++"},
	{"Dockerfile", "Docker"},
	{"docker-compose.yml", "Docker"},
	{"docker-compose.yaml", "Docker"},
	{"compose.yml", "Docker"},
	{"compose.yaml", "Docker"},
	{"flake.nix", "Nix"},
	{"default.nix", "Nix"},
	{"deno.json", "Deno"},
	{"bun.lockb", "Bun"},
	{"AGENTS.md", ""},
	{"README.md", ""},
}

// NewProjectInfoTool summarizes the project layout and detected stacks.
func NewProjectInfoTool(workDir string) *Tool {
	return &Tool{
		Name: "project_info",
		Description: "Summarize the project: detected languages, manifests, git branch, file counts by extension, " +
			"total size, and top-level entries. Use it before exploring an unfamiliar repository.",
		Parameters: objectSchema(map[string]any{
			"path":           propSchema{Type: "string", Description: "Project directory relative to the project, or absolute (default .)."},
			"max_files":      propSchema{Type: "integer", Description: "Files to inspect for statistics (default 5000, max 50000)."},
			"include_hidden": propSchema{Type: "boolean", Description: "Include dotfiles and dot-directories in statistics."},
		}),
		Handler: func(ctx context.Context, args map[string]any) (string, error) {
			root, err := resolvePath(workDir, defaultString(str(args["path"]), "."))
			if err != nil {
				return "", err
			}
			info, err := os.Stat(root)
			if err != nil {
				return "", fmt.Errorf("project_info %s: %w", root, err)
			}
			if !info.IsDir() {
				return "", fmt.Errorf("%s is not a directory", relPath(workDir, root))
			}
			maxFiles := clamp(intArg(args, "max_files", 5000), 1, 50000)
			includeHidden := boolArg(args, "include_hidden")

			types := map[string]bool{}
			var manifests []string
			for _, m := range knownManifests {
				p := filepath.Join(root, m.path)
				if _, err := os.Stat(p); err != nil {
					continue
				}
				manifests = append(manifests, m.path)
				if m.kind != "" {
					types[m.kind] = true
				}
			}

			extCounts := map[string]int{}
			fileCount := 0
			dirCount := 0
			var totalSize int64
			truncated := false
			walkErr := filepath.WalkDir(root, func(p string, d fs.DirEntry, err error) error {
				if err != nil {
					return nil
				}
				if cerr := ctx.Err(); cerr != nil {
					return cerr
				}
				if p == root {
					return nil
				}
				if d.IsDir() {
					if skipDirs[d.Name()] {
						return fs.SkipDir
					}
					if !includeHidden && strings.HasPrefix(d.Name(), ".") {
						return fs.SkipDir
					}
					dirCount++
					return nil
				}
				if !d.Type().IsRegular() {
					return nil
				}
				if !includeHidden && strings.HasPrefix(d.Name(), ".") {
					return nil
				}
				fileCount++
				if fi, err := d.Info(); err == nil {
					totalSize += fi.Size()
				}
				ext := strings.ToLower(filepath.Ext(d.Name()))
				if ext == "" {
					ext = "(none)"
				}
				extCounts[ext]++
				if fileCount >= maxFiles {
					truncated = true
					return fs.SkipAll
				}
				return nil
			})
			if walkErr != nil && walkErr != fs.SkipAll {
				return "", fmt.Errorf("project_info %s: %w", root, walkErr)
			}

			var b strings.Builder
			fmt.Fprintf(&b, "project: %s\n", relPath(workDir, root))
			if len(types) > 0 {
				fmt.Fprintf(&b, "types: %s\n", strings.Join(sortedKeys(types), ", "))
			}
			if len(manifests) > 0 {
				fmt.Fprintf(&b, "manifests: %s\n", strings.Join(manifests, ", "))
			}
			if branch, ok := gitBranch(root); ok {
				fmt.Fprintf(&b, "git: branch %s\n", branch)
			} else if _, err := os.Stat(filepath.Join(root, ".git")); err == nil {
				b.WriteString("git: repository (detached or unreadable HEAD)\n")
			}
			fmt.Fprintf(&b, "files: %d (%s), directories: %d", fileCount, humanSize(totalSize), dirCount)
			if truncated {
				fmt.Fprintf(&b, " (stopped after %d files)", maxFiles)
			}
			b.WriteByte('\n')

			if entries, err := os.ReadDir(root); err == nil {
				var names []string
				for _, e := range entries {
					if !includeHidden && strings.HasPrefix(e.Name(), ".") {
						continue
					}
					name := e.Name()
					if e.IsDir() {
						name += "/"
					}
					names = append(names, name)
				}
				sort.Strings(names)
				if len(names) > 40 {
					names = append(names[:40], fmt.Sprintf("... (%d more)", len(names)-40))
				}
				if len(names) > 0 {
					b.WriteString("top-level:\n")
					for _, name := range names {
						fmt.Fprintf(&b, "  %s\n", name)
					}
				}
			}

			if len(extCounts) > 0 {
				type extCount struct {
					ext string
					n   int
				}
				var counts []extCount
				for ext, n := range extCounts {
					counts = append(counts, extCount{ext, n})
				}
				sort.Slice(counts, func(i, j int) bool {
					if counts[i].n != counts[j].n {
						return counts[i].n > counts[j].n
					}
					return counts[i].ext < counts[j].ext
				})
				if len(counts) > 10 {
					counts = counts[:10]
				}
				b.WriteString("file types:\n")
				for _, c := range counts {
					fmt.Fprintf(&b, "  %-12s %d\n", c.ext, c.n)
				}
			}

			if detail := manifestDetail(root); detail != "" {
				b.WriteString("manifest details:\n")
				b.WriteString(detail)
				if !strings.HasSuffix(detail, "\n") {
					b.WriteByte('\n')
				}
			}
			return strings.TrimRight(b.String(), "\n"), nil
		},
	}
}

func sortedKeys(m map[string]bool) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

func gitBranch(root string) (string, bool) {
	b, err := os.ReadFile(filepath.Join(root, ".git", "HEAD"))
	if err != nil {
		return "", false
	}
	line := strings.TrimSpace(string(b))
	if strings.HasPrefix(line, "ref: refs/heads/") {
		return strings.TrimPrefix(line, "ref: refs/heads/"), true
	}
	if len(line) >= 7 {
		return line[:7] + " (detached)", true
	}
	return "", false
}

func manifestDetail(root string) string {
	var b strings.Builder
	if data, err := os.ReadFile(filepath.Join(root, "go.mod")); err == nil {
		for _, line := range strings.Split(strings.ToValidUTF8(string(data), ""), "\n") {
			line = strings.TrimSpace(line)
			if strings.HasPrefix(line, "module ") || strings.HasPrefix(line, "go ") {
				fmt.Fprintf(&b, "  go.mod: %s\n", line)
			}
		}
	}
	if data, err := os.ReadFile(filepath.Join(root, "package.json")); err == nil {
		var pkg struct {
			Name    string            `json:"name"`
			Version string            `json:"version"`
			Scripts map[string]string `json:"scripts"`
		}
		if json.Unmarshal(data, &pkg) == nil {
			if pkg.Name != "" {
				fmt.Fprintf(&b, "  package.json: %s", pkg.Name)
				if pkg.Version != "" {
					fmt.Fprintf(&b, " %s", pkg.Version)
				}
				b.WriteByte('\n')
			}
			if len(pkg.Scripts) > 0 {
				var names []string
				for name := range pkg.Scripts {
					names = append(names, name)
				}
				sort.Strings(names)
				if len(names) > 12 {
					names = names[:12]
				}
				fmt.Fprintf(&b, "  npm scripts: %s\n", strings.Join(names, ", "))
			}
		}
	}
	if data, err := os.ReadFile(filepath.Join(root, "Cargo.toml")); err == nil {
		for _, line := range strings.Split(strings.ToValidUTF8(string(data), ""), "\n") {
			line = strings.TrimSpace(line)
			if strings.HasPrefix(line, "name =") || strings.HasPrefix(line, "version =") {
				fmt.Fprintf(&b, "  Cargo.toml: %s\n", line)
			}
		}
	}
	return b.String()
}
