package main

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"time"

	"metis-agent/internal/agent"
	"metis-agent/internal/checkpoint"
	"metis-agent/internal/config"
	"metis-agent/internal/embeddings"
	"metis-agent/internal/instructions"
	"metis-agent/internal/llm"
	"metis-agent/internal/mcp"
	"metis-agent/internal/skills"
	"metis-agent/internal/tools"
)

// builtAgent bundles everything main (or an ACP session) needs from one agent
// construction. Close releases background resources exactly once.
type builtAgent struct {
	Agent       *agent.Agent
	Router      *llm.Router
	Registry    *tools.Registry
	MCP         *mcp.Manager
	Skills      *skills.Manager
	Checkpoints *checkpoint.Manager
	Close       func()
}

// buildAgent constructs an agent for dir with the resolved provider, tools,
// skills, MCP servers, instruction files, checkpoints, and verification. It is
// used by the interactive entry point and by every ACP session.
func buildAgent(dir string, resolved *llm.Resolved, v config.Values, systemPrompt string, approveEdits bool) (*builtAgent, error) {
	router, err := llm.NewRouter(resolved.Provider, resolved.BaseURL, resolved.APIKey,
		resolved.Model, v.Temperature, v.ThinkingLevel(), v.FallbackModels)
	if err != nil {
		return nil, err
	}

	registry := tools.NewRegistryWithOptions(tools.Options{
		WorkDir:      dir,
		EnableShell:  v.Shell,
		ShellTimeout: time.Duration(v.ShellTimeoutSec) * time.Second,
		AutoApprove:  v.AutoApprove,
	})
	registry.SetRedactSecrets(v.RedactSecrets)
	registry.SetApproveEdits(approveEdits)

	cp, cpErr := checkpoint.New(dir)
	if cpErr != nil {
		fmt.Fprintf(os.Stderr, "warning: undo checkpoints are disabled: %v\n", cpErr)
		cp = nil
	}
	if cp != nil {
		registry.AddMutationHook(func(_ string, _ map[string]any, paths []string) {
			for _, p := range paths {
				cp.Snapshot(p)
			}
		})
	}
	registerSubagentTool(registry, router, dir)

	// MCP servers: load the ones already configured for this project.
	mgr := mcp.NewManager(registry, config.ProjectPath(dir))
	loadCtx, loadCancel := context.WithTimeout(context.Background(), 15*time.Second)
	mgr.Load(loadCtx)
	loadCancel()

	// Skills: discover SKILL.md files from project-local and global dirs.
	skillsM := skills.NewManager(dir)
	registry.SetSkillsTool(tools.NewSkillsTool(skillsM, registry))

	// Semantic search is opt-in: it needs an embedding model, which is often a
	// separate download from the chat model.
	if model := v.EmbeddingModel; model != "" {
		cacheDir := ""
		if d, err := config.DataDir(); err == nil {
			cacheDir = filepath.Join(d, "embeddings")
		}
		client := embeddings.NewClient(resolved.BaseURL, resolved.APIKey, model)
		index := embeddings.NewIndex(dir, model, client, cacheDir)
		registry.Add(tools.NewSemanticSearchTool(dir, index))
	}

	docs := instructions.Load(dir, v.Instructions)
	cfg := agent.Config{
		LLM:      router,
		Registry: registry,
		System:   systemPrompt,
		MaxSteps: v.MaxSteps,
		Window:   v.Window,
		Mode:     agent.ModeGeneral,
	}
	if cfg.System == "" {
		cfg.System = baseSystemPrompt
	}
	if prompt := instructions.Prompt(docs); prompt != "" {
		cfg.System += prompt
	}
	if !v.Shell {
		cfg.System += "\n\nSession note: the shell tool is disabled. Do not call it; " +
			"use the file tools only and clearly explain anything you cannot run or verify."
	}
	if v.Verify != "" {
		timeout := time.Duration(v.VerifyTimeoutSec) * time.Second
		cfg.Verify = func(ctx context.Context) (string, error) {
			return runVerifyCommand(ctx, v.Verify, dir, timeout)
		}
	}
	if cp != nil {
		cfg.BeginTurn = func() { _ = cp.Begin("turn") }
		cfg.EndTurn = func() { _ = cp.End() }
	}
	a := agent.New(cfg)

	var once sync.Once
	closeFn := func() {
		once.Do(func() {
			if err := mgr.CloseAll(); err != nil {
				fmt.Fprintf(os.Stderr, "warning: closing mcp server: %v\n", err)
			}
		})
	}
	return &builtAgent{
		Agent:       a,
		Router:      router,
		Registry:    registry,
		MCP:         mgr,
		Skills:      skillsM,
		Checkpoints: cp,
		Close:       closeFn,
	}, nil
}
