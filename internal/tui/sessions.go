package tui

import (
	"fmt"
	"strings"

	"metis-agent/internal/session"
)

// replayHistory turns a resumed conversation into transcript blocks. Tool
// traffic is not replayed; the user and assistant prose is enough to restore
// context visually while the full history remains available to the model.
func (m *Model) replayHistory() {
	hist := m.agent.ExportHistory()
	replayed := make([]block, 0, len(hist)+1)
	for _, msg := range hist {
		switch msg.Role {
		case "user":
			replayed = append(replayed, block{kind: blockUser, text: msg.Content})
		case "assistant":
			if strings.TrimSpace(msg.Content) != "" {
				replayed = append(replayed, block{kind: blockAssistant, text: msg.Content})
			}
		}
	}
	if m.sess != nil {
		replayed = append(replayed, block{
			kind: blockSystem,
			text: "resumed session " + m.sess.ID + "  (" + fmt.Sprintf("%d messages", len(hist)) + ")",
		})
	}
	if len(replayed) == 0 {
		m.blocks = nil
		m.addWelcome()
		return
	}
	m.blocks = replayed
}

// saveSession persists the current conversation after a successful turn.
func (m *Model) saveSession() {
	if m.store == nil || m.sess == nil {
		return
	}
	m.sess.Provider = m.opts.Provider
	m.sess.Model = m.currentModel()
	m.sess.WorkDir = m.opts.Dir
	m.sess.Messages = m.agent.ExportHistory()
	if err := m.store.Save(m.sess); err != nil {
		if !m.sessionErrOnce {
			m.sessionErrOnce = true
			m.blocks = append(m.blocks, block{kind: blockSystem, text: "session could not be saved: " + err.Error()})
		}
		return
	}
	m.opts.SessionID = m.sess.ID
	m.opts.SessionPath = m.store.Path(m.sess)
}

// resetSessionHistory clears both the model's memory and the saved session.
func (m *Model) resetSessionHistory() {
	m.agent.Reset()
	if m.sess != nil {
		m.sess.Messages = nil
		m.saveSession()
	}
}

func (m *Model) sessionsOn() bool {
	return m.store != nil && m.sess != nil
}

func (m *Model) sessionInfo() string {
	if !m.sessionsOn() {
		return "sessions are disabled (start with --session NAME or --resume)"
	}
	msgs := m.agent.HistoryLen()
	return fmt.Sprintf("session  %s\npath     %s\nmessages %d", m.sess.ID, m.store.Path(m.sess), msgs)
}

func (m *Model) sessionList() string {
	if m.store == nil {
		return "sessions are disabled (start with --session NAME or --resume)"
	}
	infos, err := m.store.List(m.opts.Dir, 10)
	if err != nil {
		return "could not list sessions: " + err.Error()
	}
	if len(infos) == 0 {
		return "no saved sessions for this directory"
	}
	var b strings.Builder
	b.WriteString("recent sessions\n")
	for _, info := range infos {
		marker := "  "
		if m.sess != nil && info.ID == m.sess.ID {
			marker = "* "
		}
		when := info.UpdatedAt.Local().Format("2006-01-02 15:04")
		desc := strings.TrimSpace(info.Provider + " " + info.Model)
		if desc != "" {
			desc = "  " + desc
		}
		fmt.Fprintf(&b, "%s%-24s %s%s\n", marker, info.ID, when, desc)
		if info.Preview != "" {
			fmt.Fprintf(&b, "    %s\n", truncate(info.Preview, max(20, m.width-8)))
		}
	}
	return strings.TrimRight(b.String(), "\n")
}

func (m *Model) newSession() {
	if m.store == nil {
		m.blocks = append(m.blocks, block{kind: blockSystem, text: "sessions are disabled"})
		return
	}
	m.agent.Reset()
	m.sess = m.store.New(m.opts.Dir, m.opts.Provider, m.opts.Model)
	m.opts.SessionID = m.sess.ID
	m.opts.SessionPath = m.store.Path(m.sess)
	m.blocks = nil
	m.addWelcome()
	m.err = ""
	m.status = "ready"
	m.elapsed = 0
	m.step = 0
	m.activeAssistant = -1
	m.saveSession()
}

func (m *Model) resumeSession(name string) {
	if m.store == nil {
		m.blocks = append(m.blocks, block{kind: blockSystem, text: "sessions are disabled"})
		return
	}
	var (
		sess *session.Session
		err  error
	)
	if name == "" {
		sess, err = m.store.Latest(m.opts.Dir)
	} else {
		sess, err = m.store.Load(m.opts.Dir, name)
	}
	if err != nil {
		m.blocks = append(m.blocks, block{kind: blockError, text: "could not resume session: " + err.Error()})
		return
	}
	m.sess = sess
	m.opts.SessionID = sess.ID
	m.opts.SessionPath = m.store.Path(sess)
	m.agent.LoadHistory(sess.Messages)
	m.blocks = nil
	m.addWelcome()
	if m.agent.HistoryLen() > 0 {
		m.replayHistory()
	}
	m.err = ""
	m.status = "ready"
	m.elapsed = 0
	m.step = 0
	m.activeAssistant = -1
}
