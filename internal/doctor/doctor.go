// Package doctor diagnoses a Metis installation and its model backend.
package doctor

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"metis-agent/internal/checkpoint"
	"metis-agent/internal/config"
	"metis-agent/internal/instructions"
	"metis-agent/internal/llm"
	"metis-agent/internal/session"
)

// Check is one diagnostic result.
type Check struct {
	Name   string
	OK     bool
	Detail string
}

// Options configures a doctor run.
type Options struct {
	Version   string
	Dir       string
	Values    config.Values
	ToolCount int
}

// Run performs local and network checks. It never returns an error; failures
// are reported as checks so the user sees the whole picture.
func Run(ctx context.Context, opts Options) []Check {
	var checks []Check
	add := func(name string, ok bool, detail string) {
		checks = append(checks, Check{Name: name, OK: ok, Detail: detail})
	}

	add("version", true, opts.Version)

	if p, err := config.GlobalPath(); err == nil {
		if _, err := os.Stat(p); err == nil {
			add("global config", true, p)
		} else {
			add("global config", false, p+" (not found; defaults and flags still work)")
		}
	} else {
		add("global config", false, err.Error())
	}
	if _, err := os.Stat(config.ProjectPath(opts.Dir)); err == nil {
		add("project config", true, config.ProjectPath(opts.Dir))
	} else {
		add("project config", true, "none (optional)")
	}

	if dir, err := config.DataDir(); err == nil {
		add("data directory", writable(dir), dir)
	} else {
		add("data directory", false, err.Error())
	}
	if st, err := session.DefaultStore(); err == nil {
		add("sessions", writable(st.Root()), st.Root())
	}

	docs := instructions.Load(opts.Dir, opts.Values.Instructions)
	if len(docs) == 0 {
		add("instructions", true, "none found (AGENTS.md and METIS.md are optional)")
	} else {
		var names []string
		for _, d := range docs {
			names = append(names, filepath.Base(d.Path))
		}
		add("instructions", true, strings.Join(names, ", "))
	}

	if cp, err := checkpoint.New(opts.Dir); err == nil {
		add("undo checkpoints", writable(cp.Root()), cp.Root())
	} else {
		add("undo checkpoints", false, err.Error())
	}

	if git, err := exec.LookPath("git"); err == nil {
		add("git", true, git)
	} else {
		add("git", false, "git not found on PATH")
	}
	if _, err := exec.LookPath("sh"); err == nil {
		add("shell", opts.Values.Shell, "sh available")
	} else if opts.Values.Shell {
		add("shell", false, "sh not found; pass --no-shell")
	}

	add("tools", opts.ToolCount > 0, fmt.Sprintf("%d registered", opts.ToolCount))
	if opts.Values.Verify != "" {
		add("auto-verify", true, opts.Values.Verify)
	} else {
		add("auto-verify", true, "disabled (set verify in config or --verify)")
	}

	probeCtx, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()
	resolved, err := llm.Resolve(probeCtx, llm.ResolveOptions{
		Provider: opts.Values.Provider,
		BaseURL:  opts.Values.BaseURL,
		Model:    opts.Values.Model,
		APIKey:   opts.Values.APIKey,
		Temp:     opts.Values.Temperature,
	})
	if err != nil {
		add("provider", false, err.Error())
		return checks
	}
	add("provider", true, fmt.Sprintf("%s · %s · %s", resolved.Provider, resolved.Model, resolved.BaseURL))

	client, err := llm.NewClientWithOptions(llm.ClientOptions{
		Provider: resolved.Provider,
		BaseURL:  resolved.BaseURL,
		APIKey:   resolved.APIKey,
		Model:    resolved.Model,
		Temp:     opts.Values.Temperature,
	})
	if err != nil {
		add("model catalog", false, err.Error())
		return checks
	}
	listCtx, cancel2 := context.WithTimeout(ctx, 10*time.Second)
	defer cancel2()
	models, err := client.ListModels(listCtx)
	if err != nil {
		add("model catalog", false, err.Error())
		return checks
	}
	add("model catalog", true, fmt.Sprintf("%d model(s) available", len(models)))
	return checks
}

func writable(dir string) bool {
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return false
	}
	f, err := os.CreateTemp(dir, ".doctor-*")
	if err != nil {
		return false
	}
	name := f.Name()
	f.Close()
	_ = os.Remove(name)
	return true
}
