package ui

import (
	"errors"
	"reflect"
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"

	"vimwhat/internal/store"
)

func selectedComposer(t *testing.T, body string) Model {
	t.Helper()
	m := composerTestModel()
	m.mode = ModeInsert
	m.composer = body
	next, _ := m.Update(tea.KeyMsg{Type: tea.KeyCtrlA})
	m = next.(Model)
	if m.composerSelectAll != (body != "") || m.composer != body {
		t.Fatalf("select all: selected=%v body=%q", m.composerSelectAll, m.composer)
	}
	return m
}

func TestComposerSelectionReplacement(t *testing.T) {
	for _, tt := range []struct {
		name string
		msg  tea.Msg
		want string
	}{
		{"typing", tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("é")}, "é"},
		{"terminal paste", tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("new\n👩🏻‍⚕️"), Paste: true}, "new\n👩🏻‍⚕️"},
		{"space", tea.KeyMsg{Type: tea.KeySpace}, " "},
		{"backspace", tea.KeyMsg{Type: tea.KeyBackspace}, ""},
		{"alternate backspace", tea.KeyMsg{Type: tea.KeyCtrlH}, ""},
		{"delete", tea.KeyMsg{Type: tea.KeyDelete}, ""},
		{"newline", tea.KeyMsg{Type: tea.KeyCtrlJ}, "\n"},
		{"shift enter", rawStringMsg("\x1b[13;2u"), "\n"},
		{"clipboard", ClipboardTextPastedMsg{ChatID: "a", Text: "pasted"}, "pasted"},
	} {
		t.Run(tt.name, func(t *testing.T) {
			m := selectedComposer(t, "old 👩🏻‍⚕️\n"+strings.Repeat("more\n", 12))
			m.attachments = []Attachment{{LocalPath: "/tmp/fixture.png", FileName: "fixture.png"}}
			m.replyTo = &store.Message{ID: "quoted", Body: "reply context"}
			next, _ := m.Update(tt.msg)
			got := next.(Model)
			if got.composer != tt.want || got.composerSelectAll || got.mode != ModeInsert {
				t.Fatalf("replacement: body=%q selected=%v mode=%v", got.composer, got.composerSelectAll, got.mode)
			}
			if !reflect.DeepEqual(got.attachments, m.attachments) || !reflect.DeepEqual(got.replyTo, m.replyTo) {
				t.Fatal("text selection changed attachments or reply")
			}
			if draft := got.composerDrafts["a"]; draft.Body != tt.want || len(draft.Media) != 1 || draft.Reply == nil || draft.Reply.ID != "quoted" {
				t.Fatalf("replacement not captured in full draft: %+v", draft)
			}
			if len(got.messagesByChat["a"]) != 0 {
				t.Fatal("replacement unexpectedly sent a message")
			}
		})
	}
}

func TestComposerSelectionEscapeAndEmpty(t *testing.T) {
	m := selectedComposer(t, "keep this")
	before := m.composerDraft()
	next, _ := m.Update(tea.KeyMsg{Type: tea.KeyCtrlA})
	m = next.(Model)
	if !m.composerSelectAll || !reflect.DeepEqual(m.composerDraft(), before) {
		t.Fatal("repeated select all changed the draft or deselected")
	}
	next, _ = m.Update(tea.KeyMsg{Type: tea.KeyEsc})
	m = next.(Model)
	if m.composerSelectAll || m.mode != ModeInsert || !reflect.DeepEqual(m.composerDraft(), before) {
		t.Fatal("first Escape must only deselect")
	}
	next, _ = m.Update(tea.KeyMsg{Type: tea.KeyEsc})
	m = next.(Model)
	if m.mode != ModeNormal || m.draftsByChat["a"] != "keep this" {
		t.Fatal("second Escape must leave insert mode and preserve draft")
	}
	m = selectedComposer(t, "")
	next, _ = m.Update(tea.KeyMsg{Type: tea.KeyEsc})
	if next.(Model).mode != ModeNormal {
		t.Fatal("empty composer should not require a second Escape")
	}
}

func TestComposerSelectionBindingsAreConfigurable(t *testing.T) {
	m := composerTestModel()
	m.mode, m.composer = ModeInsert, "draft"
	m.config.Keymap.InsertSelectAll = "ctrl+s"
	m.config.Keymap.InsertDelete = "ctrl+d"
	next, _ := m.Update(tea.KeyMsg{Type: tea.KeyCtrlA})
	m = next.(Model)
	if m.composerSelectAll {
		t.Fatal("default select key still active after remapping")
	}
	next, _ = m.Update(tea.KeyMsg{Type: tea.KeyCtrlS})
	m = next.(Model)
	if !m.composerSelectAll {
		t.Fatal("configured select key ignored")
	}
	next, _ = m.Update(tea.KeyMsg{Type: tea.KeyDelete})
	m = next.(Model)
	if !m.composerSelectAll || m.composer != "draft" {
		t.Fatal("default delete key still active after remapping")
	}
	next, _ = m.Update(tea.KeyMsg{Type: tea.KeyCtrlD})
	m = next.(Model)
	if m.composerSelectAll || m.composer != "" {
		t.Fatal("configured delete key ignored")
	}
	help := stripANSI(m.renderHelp(100))
	if !strings.Contains(help, "ctrl+s") || !strings.Contains(help, "select all composer text") || !strings.Contains(help, "ctrl+d") {
		t.Fatalf("help omitted configured selection bindings:\n%s", help)
	}
}

func TestComposerSelectionDropsReplacedMentions(t *testing.T) {
	for _, msg := range []tea.Msg{
		tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("@Alice")},
		ClipboardTextPastedMsg{ChatID: "a", Text: "@Alice"},
	} {
		m := composerTestModel()
		m.mode, m.composer = ModeInsert, "@Alice"
		m.composerMentions = []store.MessageMention{{JID: "alice", DisplayName: "Alice", StartByte: 0, EndByte: 6}}
		m.composerMentionsByChat["a"] = m.composerMentions
		m.mentionActive = true
		next, _ := m.Update(tea.KeyMsg{Type: tea.KeyCtrlA})
		m = next.(Model)
		if m.mentionActive || !m.composerSelectAll || len(m.composerMentions) != 1 {
			t.Fatal("select all must dismiss autocomplete but preserve existing mention metadata")
		}
		next, _ = m.Update(msg)
		m = next.(Model)
		if len(m.composerMentions) != 0 || len(m.composerMentionsByChat["a"]) != 0 || m.composer != "@Alice" {
			t.Fatal("replacement retained old mention metadata for newly typed/pasted identical text")
		}
	}
}

func TestComposerSelectionSend(t *testing.T) {
	for _, blocked := range []bool{false, true} {
		m := selectedComposer(t, "send me")
		m.blockSending = blocked
		next, _ := m.Update(tea.KeyMsg{Type: tea.KeyEnter})
		m = next.(Model)
		if blocked {
			if m.composer != "send me" || !m.composerSelectAll || len(m.messagesByChat["a"]) != 0 {
				t.Fatal("blocked send discarded selected draft")
			}
		} else {
			if m.composer != "" || m.composerSelectAll || len(m.messagesByChat["a"]) != 1 || m.messagesByChat["a"][0].Body != "send me" {
				t.Fatal("send did not queue original text and reset selection")
			}
		}
	}
}

func TestComposerSelectionWhileEditingPreservesSavedDraft(t *testing.T) {
	m := composerTestModel()
	m.mode, m.composer = ModeInsert, "@Alice draft"
	m.composerMentions = []store.MessageMention{{JID: "alice", DisplayName: "Alice", StartByte: 0, EndByte: 6}}
	m.captureComposerDraft()
	wantDraft := m.composerDraft()
	m.composer = "message being edited"
	m.editTarget = &store.Message{ID: "sent", Body: m.composer}
	for _, key := range []tea.KeyType{tea.KeyCtrlA, tea.KeyDelete} {
		next, _ := m.Update(tea.KeyMsg{Type: key})
		m = next.(Model)
	}
	if m.editTarget == nil || m.composer != "" || m.composerSelectAll {
		t.Fatal("selection did not clear only the text being edited")
	}
	if !reflect.DeepEqual(m.composerDrafts["a"], wantDraft) || len(m.composerMentionsByChat["a"]) != 1 {
		t.Fatal("editing an existing message changed the saved compose draft")
	}
}

func TestComposerSelectionDoesNotLeakIntoRestoredDraft(t *testing.T) {
	m := selectedComposer(t, "Alice draft")
	m.draftsByChat["b"] = "Bob draft"
	m.activeChat = 1
	next, _ := m.beginInsert(nil)
	m = next.(Model)
	if m.composerSelectAll || m.composer != "Bob draft" {
		t.Fatal("selection leaked to restored chat draft")
	}
	next, _ = m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("!")})
	if next.(Model).composer != "Bob draft!" {
		t.Fatal("typing replaced unselected restored draft")
	}
	m = selectedComposer(t, "old")
	next, _ = m.Update(ComposerEditedMsg{ChatID: "a", Body: "from editor"})
	if next.(Model).composerSelectAll || next.(Model).composer != "from editor" {
		t.Fatal("selection leaked into external editor result")
	}
}

func TestComposerSelectionClipboardIsolation(t *testing.T) {
	for _, msg := range []ClipboardTextPastedMsg{
		{ChatID: "b", Text: "other chat"},
		{ChatID: "a", Err: errors.New("clipboard unavailable")},
		{ChatID: "a", Text: ""},
	} {
		m := selectedComposer(t, "keep selected")
		next, _ := m.Update(msg)
		m = next.(Model)
		if !m.composerSelectAll || m.composer != "keep selected" {
			t.Fatalf("clipboard result %+v changed current selection", msg)
		}
	}
}

func TestComposerDeleteWithoutSelectionAndNormalSelectAll(t *testing.T) {
	m := composerTestModel()
	m.mode, m.composer = ModeInsert, "draft 👩🏻‍⚕️"
	next, _ := m.Update(tea.KeyMsg{Type: tea.KeyDelete})
	if next.(Model).composer != m.composer {
		t.Fatal("Delete at end of text changed unselected composer")
	}
	m.mode = ModeNormal
	next, _ = m.Update(tea.KeyMsg{Type: tea.KeyCtrlA})
	if next.(Model).composerSelectAll || next.(Model).mode != ModeNormal {
		t.Fatal("insert selection action leaked into normal mode")
	}
}

func TestComposerSelectionHighlightAndWrapping(t *testing.T) {
	withANSIStyles(t)
	m := selectedComposer(t, "first\n\nlast 👩🏻‍⚕️")
	for _, width := range []int{12, 24, 80} {
		view := m.renderComposer(width)
		for _, word := range []string{"first", "last"} {
			codes := sgrCodesBeforeNth(view, word, 0)
			if !hasSGRCode(codes, "38") || !hasSGRCode(codes, "48") {
				t.Fatalf("selected %q lacks foreground/background highlight at width %d: %v", word, width, codes)
			}
		}
		for _, line := range strings.Split(stripANSI(view), "\n") {
			if displayWidth(line) > width {
				t.Fatalf("selected composer overflows width %d: %q", width, line)
			}
		}
	}
	view := stripANSI(m.renderComposer(80))
	if !strings.Contains(view, "all text selected") || !strings.Contains(view, "esc deselects") || !strings.Contains(view, m.sanitizeDisplayText("👩🏻‍⚕️", true)) {
		t.Fatalf("selection notice or grapheme missing:\n%s", view)
	}
	t.Logf("Selected composer terminal capture (ANSI colors stripped):\n%s", view)
}
