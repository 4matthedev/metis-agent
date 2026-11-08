package tui

import (
	"fmt"
	"strings"

	tea "github.com/charmbracelet/bubbletea"

	"metis-agent/internal/tools"
)

// approvalRequestMsg asks the UI to confirm a dangerous tool call. The agent
// goroutine blocks on resp until the user answers.
type approvalRequestMsg struct {
	req  tools.ApprovalRequest
	resp chan tools.ApprovalDecision
}

type pendingApproval struct {
	req  tools.ApprovalRequest
	resp chan tools.ApprovalDecision
}

func (m *Model) handleApprovalKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	p := m.pendingApproval
	decision := tools.ApprovalDeny
	label := "denied"
	cancel := false
	switch msg.String() {
	case "y", "Y":
		decision = tools.ApprovalOnce
		label = "approved once"
	case "a":
		decision = tools.ApprovalAlways
		label = "always allowed for this session"
	case "A":
		decision = tools.ApprovalAll
		label = "auto-approve enabled"
		m.autoApprove = true
		m.agent.SetAutoApprove(true)
	case "n", "N":
		decision = tools.ApprovalDeny
		label = "denied"
	case "esc", "ctrl+c":
		decision = tools.ApprovalDeny
		label = "denied"
		cancel = true
	default:
		return m, nil
	}
	p.resp <- decision
	m.pendingApproval = nil
	m.blocks = append(m.blocks, block{
		kind: blockSystem,
		text: "approval · " + p.req.Tool + " · " + label,
	})
	if cancel {
		m.cancelRun()
	} else {
		m.status = "thinking"
		m.syncViewport(false)
		return m, m.input.Focus()
	}
	m.syncViewport(false)
	return m, nil
}

func formatApprovalRequest(req tools.ApprovalRequest) string {
	var b strings.Builder
	fmt.Fprintf(&b, "approval required · %s\n", req.Tool)
	if req.Summary != "" {
		fmt.Fprintf(&b, "  %s\n", oneLine(req.Summary))
	}
	if req.Reason != "" {
		fmt.Fprintf(&b, "  %s\n", req.Reason)
	}
	if req.Preview != "" {
		b.WriteString("\n")
		for _, line := range strings.Split(strings.TrimRight(req.Preview, "\n"), "\n") {
			b.WriteString("  ")
			b.WriteString(line)
			b.WriteString("\n")
		}
	}
	fmt.Fprintf(&b, "  y allow once   a always allow %s   A auto-approve all   n deny", req.Tool)
	return b.String()
}
