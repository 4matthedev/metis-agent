// Package main is a tiny MCP stdio server used only by metis integration tests
// to verify the mcp client's connect/list/call path end-to-end.
package main

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"os"
	"os/signal"
	"syscall"

	mcp "github.com/modelcontextprotocol/go-sdk/mcp"
)

type greetArgs struct {
	Name string `json:"name"`
}

func main() {
	server := mcp.NewServer(&mcp.Implementation{Name: "metis-smoke", Version: "v1.0.0"}, nil)

	greetSchema := map[string]any{
		"type": "object",
		"properties": map[string]any{
			"name": map[string]any{"type": "string"},
		},
	}
	server.AddTool(&mcp.Tool{
		Name:        "greet",
		Description: "Return a greeting for the given name.",
		InputSchema: greetSchema,
	}, func(ctx context.Context, req *mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		var a greetArgs
		if req.Params.Arguments != nil {
			if err := json.Unmarshal(req.Params.Arguments, &a); err != nil {
				return nil, fmt.Errorf("unmarshal args: %w", err)
			}
		}
		return &mcp.CallToolResult{
			Content: []mcp.Content{&mcp.TextContent{Text: "Hi " + a.Name}},
		}, nil
	})

	// Also add an erroring tool to exercise the error path.
	server.AddTool(&mcp.Tool{
		Name:        "fail",
		Description: "Always errors, used to exercise error propagation.",
		InputSchema: map[string]any{"type": "object"},
	}, func(ctx context.Context, req *mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		return nil, fmt.Errorf("boom")
	})

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	// Shutdown on SIGINT/SIGTERM so the test can close the server.
	ch := make(chan os.Signal, 1)
	signal.Notify(ch, os.Interrupt, syscall.SIGTERM)
	go func() {
		<-ch
		cancel()
	}()

	if err := server.Run(ctx, &mcp.StdioTransport{}); err != nil && err != context.Canceled {
		log.Fatalf("server failed: %v", err)
	}
}
