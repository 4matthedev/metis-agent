package main

import (
	"context"
	"encoding/json"
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

// The process-wide audit log: one append-only file under the data dir, shared
// by every session (CLI and ACP). It is opened lazily and intentionally left
// open for the life of the process (process exit flushes it); it is not closed
// by any single agent's Close, which would cut off the other sessions.
var (
	auditLogMu       sync.Mutex
	auditLogFile     *os.File
	auditLogFileOpen bool
)

// openAuditLog lazily opens the process-wide audit log.
func openAuditLog() error {
	auditLogMu.Lock()
	defer auditLogMu.Unlock()
	if auditLogFileOpen {
		return nil
	}
	d, err := config.DataDir()
	if err != nil {
		return err
	}
	p := filepath.Join(d, "audit.log")
	f, err := os.OpenFile(p, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o600)
	if err != nil {
		return err
	}
	auditLogFile = f
	auditLogFileOpen = true
	return nil
}

// writeAudit appends one tools.AuditEntry to the audit log. It is best effort:
// it never fails a tool call, and a crashed process may lose the last unflushed
// line.
func writeAudit(e tools.AuditEntry) {
	if err := openAuditLog(); err != nil {
		// Best effort: a logging failure must never fail a tool call.
		return
	}
	auditLogMu.Lock()
	defer auditLogMu.Unlock()
	if auditLogFile == nil {
		return
	}
	b, err := json.Marshal(e)
	if err != nil {
		return
	}
	// Append a newline and write best-effort; auditing must never fail a tool
	// call, so the (int, error) result is discarded.
	auditLogFile.Write(append(b, byte('\n')))
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
	// Every tool invocation (allowed or refused) is recorded to the process-wide
	// audit log, for both the CLI and ACP sessions.
	registry.SetAuditor(writeAudit)

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

	// [mcp.<name>] sections in the project's .metis-agent.conf layer on top of
	// the legacy JSON servers; within this scope .conf wins on name conflicts
	// (see AGENT.md §8).
	if cfPath := config.ProjectConfPath(dir); cfPath != "" {
		if cf, err := config.LoadFile(cfPath); err == nil && len(cf.McpServers) > 0 {
			confCtx, confCancel := context.WithTimeout(context.Background(), 15*time.Second)
			mgr.LayerServers(confCtx, cf.McpServers)
			confCancel()
		}
	}

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
