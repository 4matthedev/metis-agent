package tools

import (
	"bytes"
	"context"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
)

type symbolPattern struct {
	re   *regexp.Regexp
	kind string
}

var languageSymbols = map[string][]symbolPattern{
	".go": {
		{regexp.MustCompile(`^func\s+(?:\([^)]*\)\s*)?([A-Za-z_]\w*)`), "func"},
		{regexp.MustCompile(`^type\s+([A-Za-z_]\w*)`), "type"},
	},
	".py": {
		{regexp.MustCompile(`^\s*(?:async\s+)?def\s+([A-Za-z_]\w*)`), "def"},
		{regexp.MustCompile(`^\s*class\s+([A-Za-z_]\w*)`), "class"},
	},
	".js":  jsSymbols(),
	".jsx": jsSymbols(),
	".ts":  jsSymbols(),
	".tsx": jsSymbols(),
	".mjs": jsSymbols(),
	".cjs": jsSymbols(),
	".rs": {
		{regexp.MustCompile(`^\s*(?:pub\s+)?(?:async\s+)?fn\s+([A-Za-z_]\w*)`), "fn"},
		{regexp.MustCompile(`^\s*(?:pub\s+)?(?:struct|enum|trait)\s+([A-Za-z_]\w*)`), "type"},
	},
	".java": {
		{regexp.MustCompile(`^\s*(?:public|private|protected)?\s*(?:static\s+)?(?:final\s+)?(?:class|interface|enum|record)\s+([A-Za-z_]\w*)`), "type"},
		{regexp.MustCompile(`^\s*(?:public|private|protected)\s+(?:static\s+)?(?:final\s+)?[\w<>\[\], ?]+\s+([A-Za-z_]\w*)\s*\([^;]*$`), "method"},
	},
	".kt": {
		{regexp.MustCompile(`^\s*(?:public|private|protected|internal)?\s*(?:data\s+|sealed\s+|abstract\s+)?(?:class|interface|object)\s+([A-Za-z_]\w*)`), "type"},
		{regexp.MustCompile(`^\s*(?:public|private|protected|internal)?\s*fun\s+([A-Za-z_]\w*)`), "fun"},
	},
	".rb": {
		{regexp.MustCompile(`^\s*def\s+(?:self\.)?([A-Za-z_]\w*[!?=]?)`), "def"},
		{regexp.MustCompile(`^\s*(?:class|module)\s+([A-Za-z_]\w*)`), "type"},
	},
	".php": {
		{regexp.MustCompile(`^\s*(?:public|private|protected|static|\s)*function\s+([A-Za-z_]\w*)`), "function"},
		{regexp.MustCompile(`^\s*(?:abstract\s+|final\s+)?(?:class|interface|trait)\s+([A-Za-z_]\w*)`), "type"},
	},
	".c":   cSymbols(),
	".h":   cSymbols(),
	".cc":  cSymbols(),
	".cpp": cSymbols(),
	".hpp": cSymbols(),
	".cs": {
		{regexp.MustCompile(`^\s*(?:public|private|protected|internal)?\s*(?:static\s+)?(?:class|interface|enum|record|struct)\s+([A-Za-z_]\w*)`), "type"},
	},
	".sh": {
		{regexp.MustCompile(`^\s*(?:function\s+)?([A-Za-z_]\w*)\s*\(\)\s*\{`), "function"},
	},
	".bash": {
		{regexp.MustCompile(`^\s*(?:function\s+)?([A-Za-z_]\w*)\s*\(\)\s*\{`), "function"},
	},
}

func jsSymbols() []symbolPattern {
	return []symbolPattern{
		{regexp.MustCompile(`^\s*(?:export\s+)?(?:default\s+)?(?:async\s+)?function\s+([A-Za-z_$][\w$]*)`), "function"},
		{regexp.MustCompile(`^\s*(?:export\s+)?(?:default\s+)?(?:abstract\s+)?class\s+([A-Za-z_$][\w$]*)`), "class"},
		{regexp.MustCompile(`^\s*(?:export\s+)?(?:interface|type)\s+([A-Za-z_$][\w$]*)`), "type"},
		{regexp.MustCompile(`^\s*(?:export\s+)?(?:const|let|var)\s+([A-Za-z_$][\w$]*)\s*=\s*(?:async\s*)?(?:function|\()`), "function"},
	}
}

func cSymbols() []symbolPattern {
	return []symbolPattern{
		{regexp.MustCompile(`^\s*(?:class|struct|enum|union)\s+([A-Za-z_]\w*)`), "type"},
		{regexp.MustCompile(`^[A-Za-z_][\w\s\*&:<>,]*\s+([A-Za-z_]\w*)\s*\([^;]*\)\s*\{?\s*$`), "function"},
	}
}

// NewRepoMapTool outlines the symbols in a repository without reading every
// file into the model context.
func NewRepoMapTool(workDir string) *Tool {
	return &Tool{
		Name: "repo_map",
		Description: "Produce a compact symbol map of the repository: functions, types, and classes with line numbers. " +
			"Use it to orient yourself in an unfamiliar codebase before reading files.",
		Parameters: objectSchema(map[string]any{
			"path":        propSchema{Type: "string", Description: "Directory relative to the project, or absolute (default .)."},
			"include":     propSchema{Type: "string", Description: "Optional glob filter, e.g. *.go or src/**/*.ts."},
			"max_files":   propSchema{Type: "integer", Description: "Maximum files to inspect (default 1500, max 10000)."},
			"max_symbols": propSchema{Type: "integer", Description: "Maximum symbols to print (default 800, max 5000)."},
		}),
		Handler: func(ctx context.Context, args map[string]any) (string, error) {
			root, err := resolvePath(workDir, defaultString(str(args["path"]), "."))
			if err != nil {
				return "", err
			}
			info, err := os.Stat(root)
			if err != nil {
				return "", fmt.Errorf("repo_map %s: %w", root, err)
			}
			if !info.IsDir() {
				return "", fmt.Errorf("%s is not a directory", relPath(workDir, root))
			}
			include := str(args["include"])
			maxFiles := clamp(intArg(args, "max_files", 1500), 1, 10000)
			maxSymbols := clamp(intArg(args, "max_symbols", 800), 1, 5000)

			type fileMap struct {
				path    string
				lang    string
				symbols []string
			}
			var files []fileMap
			symbols := 0
			walkErr := filepath.WalkDir(root, func(p string, d fs.DirEntry, err error) error {
				if err != nil {
					return nil
				}
				if cerr := ctx.Err(); cerr != nil {
					return cerr
				}
				if d.IsDir() {
					if p != root && (skipDirs[d.Name()] || strings.HasPrefix(d.Name(), ".")) {
						return fs.SkipDir
					}
					return nil
				}
				if !d.Type().IsRegular() {
					return nil
				}
				ext := strings.ToLower(filepath.Ext(d.Name()))
				pats, ok := languageSymbols[ext]
				if !ok {
					return nil
				}
				rel, err := filepath.Rel(root, p)
				if err != nil {
					return nil
				}
				relSlash := filepath.ToSlash(rel)
				if include != "" && !matchInclude(include, relSlash) {
					return nil
				}
				fi, err := d.Info()
				if err != nil || fi.Size() > 1024*1024 {
					return nil
				}
				b, err := os.ReadFile(p)
				if err != nil || bytes.IndexByte(b, 0) >= 0 {
					return nil
				}
				var found []string
				for i, line := range strings.Split(strings.ToValidUTF8(string(b), ""), "\n") {
					line = strings.TrimSuffix(line, "\r")
					for _, pat := range pats {
						m := pat.re.FindStringSubmatch(line)
						if len(m) < 2 {
							continue
						}
						found = append(found, fmt.Sprintf("%s %s (line %d)", pat.kind, m[1], i+1))
						break
					}
				}
				if len(found) > 0 {
					files = append(files, fileMap{path: relSlash, lang: strings.TrimPrefix(ext, "."), symbols: found})
					symbols += len(found)
				}
				if len(files) >= maxFiles || symbols >= maxSymbols {
					return fs.SkipAll
				}
				return nil
			})
			if walkErr != nil && walkErr != fs.SkipAll {
				return "", fmt.Errorf("repo_map %s: %w", root, walkErr)
			}
			if len(files) == 0 {
				return "no indexed source files found", nil
			}
			sort.Slice(files, func(i, j int) bool { return files[i].path < files[j].path })

			var b strings.Builder
			fmt.Fprintf(&b, "repo map: %s (%d files, %d symbols)\n", relPath(workDir, root), len(files), symbols)
			printed := 0
			for _, f := range files {
				if printed >= maxSymbols {
					fmt.Fprintf(&b, "\n... (stopped after %d symbols)", maxSymbols)
					break
				}
				fmt.Fprintf(&b, "\n%s [%s]\n", f.path, f.lang)
				for _, s := range f.symbols {
					if printed >= maxSymbols {
						break
					}
					fmt.Fprintf(&b, "  %s\n", s)
					printed++
				}
			}
			return strings.TrimRight(b.String(), "\n"), nil
		},
	}
}
