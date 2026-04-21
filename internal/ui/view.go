package ui

import (
	"fmt"
	"strings"
	"unicode/utf8"

	"github.com/charmbracelet/lipgloss"

	"maybewhats/internal/store"
)

var (
	appBG        = lipgloss.Color("#101418")
	panelBG      = lipgloss.Color("#101418")
	softFG       = lipgloss.Color("#9AA5B1")
	primaryFG    = lipgloss.Color("#F5F7FA")
	accentFG     = lipgloss.Color("#7ED7C1")
	warnFG       = lipgloss.Color("#F4D35E")
	borderColor  = lipgloss.Color("#2B3A42")
	activeBorder = lipgloss.Color("#7ED7C1")
	outgoingFG   = lipgloss.Color("#C7F9CC")
	incomingLine = lipgloss.Color("#4B6472")
	outgoingLine = lipgloss.Color("#2EA56F")
	selectedLine = lipgloss.Color("#F4D35E")
)

func (m Model) View() string {
	if m.width == 0 || m.height == 0 {
		return "loading..."
	}

	bodyHeight := max(3, m.height-2)
	body := m.renderBody(bodyHeight)
	status := m.renderStatus()
	input := m.renderInput()

	rendered := lipgloss.NewStyle().
		Background(appBG).
		Foreground(primaryFG).
		Width(m.width).
		Render(lipgloss.JoinVertical(lipgloss.Left, body, status, input))

	return capLines(rendered, m.height)
}

func (m Model) renderBody(height int) string {
	chatWidth := max(24, m.width/4)
	previewWidth := max(26, m.width/4)
	messageWidth := m.width - chatWidth

	chats := m.renderPanel(FocusChats, chatWidth, height, m.renderChats(panelContentWidth(m.panelStyle(FocusChats), chatWidth)))

	if m.compactLayout {
		switch m.focus {
		case FocusChats:
			return chats
		case FocusPreview:
			return m.renderPanel(FocusPreview, m.width, height, m.renderInfo(panelContentWidth(m.panelStyle(FocusPreview), m.width)))
		default:
			style := m.panelStyle(FocusMessages)
			return m.renderPanel(FocusMessages, m.width, height, m.renderMessages(panelContentWidth(style, m.width), panelContentHeight(style, height)))
		}
	}

	if m.infoPaneVisible {
		messageWidth -= previewWidth
	}
	messageStyle := m.panelStyle(FocusMessages)
	messages := m.renderPanel(FocusMessages, messageWidth, height, m.renderMessages(panelContentWidth(messageStyle, messageWidth), panelContentHeight(messageStyle, height)))
	if !m.infoPaneVisible {
		return lipgloss.JoinHorizontal(lipgloss.Top, chats, messages)
	}

	info := m.renderPanel(FocusPreview, previewWidth, height, m.renderInfo(panelContentWidth(m.panelStyle(FocusPreview), previewWidth)))
	return lipgloss.JoinHorizontal(lipgloss.Top, chats, messages, info)
}

func (m Model) renderPanel(focus Focus, width, height int, content string) string {
	style := m.panelStyle(focus)
	frameWidth, frameHeight := style.GetFrameSize()
	innerWidth := max(1, width-frameWidth)
	innerHeight := max(1, height-frameHeight)
	content = clipLines(content, innerHeight)

	filled := lipgloss.Place(
		innerWidth,
		innerHeight,
		lipgloss.Left,
		lipgloss.Top,
		content,
		lipgloss.WithWhitespaceBackground(panelBG),
		lipgloss.WithWhitespaceChars(" "),
	)

	return style.
		Width(innerWidth).
		Height(innerHeight).
		Render(filled)
}

func panelContentWidth(style lipgloss.Style, totalWidth int) int {
	frameWidth, _ := style.GetFrameSize()
	return max(1, totalWidth-frameWidth)
}

func panelContentHeight(style lipgloss.Style, totalHeight int) int {
	_, frameHeight := style.GetFrameSize()
	return max(1, totalHeight-frameHeight)
}

func wrapPlainText(text string, width int) string {
	width = max(1, width)
	var out []string

	for _, rawLine := range strings.Split(text, "\n") {
		words := strings.Fields(rawLine)
		if len(words) == 0 {
			out = append(out, "")
			continue
		}

		line := ""
		for _, word := range words {
			for lipgloss.Width(word) > width {
				prefix, rest := splitDisplayWidth(word, width)
				if line != "" {
					out = append(out, line)
					line = ""
				}
				out = append(out, prefix)
				word = rest
			}

			if line == "" {
				line = word
				continue
			}
			if lipgloss.Width(line)+1+lipgloss.Width(word) <= width {
				line += " " + word
				continue
			}
			out = append(out, line)
			line = word
		}
		if line != "" {
			out = append(out, line)
		}
	}

	return strings.Join(out, "\n")
}

func splitDisplayWidth(value string, width int) (string, string) {
	if value == "" {
		return "", ""
	}

	current := 0
	cut := 0
	for i, r := range value {
		runeWidth := lipgloss.Width(string(r))
		if current > 0 && current+runeWidth > width {
			break
		}
		current += runeWidth
		cut = i + len(string(r))
		if current >= width {
			break
		}
	}
	if cut == 0 {
		_, size := utf8.DecodeRuneInString(value)
		cut = size
	}

	return value[:cut], value[cut:]
}

func clipLines(content string, height int) string {
	return strings.Join(clipLinesSlice(strings.Split(content, "\n"), height), "\n")
}

func capLines(content string, height int) string {
	lines := strings.Split(content, "\n")
	if len(lines) > height {
		lines = lines[:height]
	}
	return strings.Join(lines, "\n")
}

func clipLinesSlice(lines []string, height int) []string {
	height = max(1, height)
	if len(lines) <= height {
		return lines
	}
	return lines[:height]
}

func messageViewport(blocks []string, cursor, height int) []string {
	height = max(1, height)
	if len(blocks) == 0 {
		return nil
	}

	selected := clamp(cursor, 0, len(blocks)-1)
	var out []string
	used := 0

	for i := selected; i >= 0; i-- {
		block := blockLines(blocks[i])
		if len(out) > 0 {
			block = append(block, "")
		}
		if used+len(block) > height {
			if len(out) == 0 {
				return tailLines(blockLines(blocks[i]), height)
			}
			break
		}
		out = append(block, out...)
		used += len(block)
	}

	for i := selected + 1; i < len(blocks) && used < height; i++ {
		block := append([]string{""}, blockLines(blocks[i])...)
		if used+len(block) > height {
			remaining := height - used
			if remaining > 0 {
				out = append(out, block[:remaining]...)
			}
			break
		}
		out = append(out, block...)
		used += len(block)
	}

	if len(out) > height {
		return tailLines(out, height)
	}
	return out
}

func blockLines(block string) []string {
	if block == "" {
		return nil
	}
	return strings.Split(block, "\n")
}

func tailLines(lines []string, height int) []string {
	height = max(1, height)
	if len(lines) <= height {
		return lines
	}
	return lines[len(lines)-height:]
}

func alignDisplay(value string, width int, right bool) string {
	current := lipgloss.Width(value)
	if current >= width {
		return value
	}
	padding := strings.Repeat(" ", width-current)
	if right {
		return padding + value
	}
	return value + padding
}

func padDisplay(value string, width int) string {
	return alignDisplay(value, width, false)
}

func truncateDisplay(value string, width int) string {
	if lipgloss.Width(value) <= width {
		return value
	}
	if width <= 1 {
		return ""
	}
	head, _ := splitDisplayWidth(value, width-1)
	return head + "~"
}

func (m Model) renderChats(width int) string {
	var lines []string
	header := lipgloss.NewStyle().Bold(true).Foreground(accentFG).Render("Chats")
	lines = append(lines, header)

	if len(m.chats) == 0 {
		lines = append(lines,
			"",
			lipgloss.NewStyle().Foreground(softFG).Width(width).Render("No chats in the local database yet."),
			lipgloss.NewStyle().Foreground(softFG).Width(width).Render("The next step is wiring WhatsApp sync into this store."),
		)
		return strings.Join(lines, "\n")
	}

	for i, chat := range m.chats {
		cursor := " "
		if i == m.activeChat {
			cursor = ">"
		}

		flags := make([]string, 0, 3)
		if chat.Pinned {
			flags = append(flags, "pin")
		}
		if chat.Muted {
			flags = append(flags, "muted")
		}
		if chat.HasDraft {
			flags = append(flags, "draft")
		}

		suffix := ""
		if len(flags) > 0 {
			suffix = " [" + strings.Join(flags, ",") + "]"
		}
		if chat.Unread > 0 {
			suffix += fmt.Sprintf(" (%d)", chat.Unread)
		}

		line := fmt.Sprintf("%s %s%s", cursor, chat.Title, suffix)
		if i == m.activeChat {
			line = lipgloss.NewStyle().Foreground(primaryFG).Bold(true).Render(line)
		} else {
			line = lipgloss.NewStyle().Foreground(softFG).Render(line)
		}
		lines = append(lines, lipgloss.NewStyle().Width(width).Render(line))
	}

	return strings.Join(lines, "\n")
}

func (m Model) renderMessages(width, height int) string {
	chat := m.currentChat()
	title := chat.Title
	if title == "" {
		title = "Messages"
	}
	header := lipgloss.NewStyle().Bold(true).Foreground(accentFG).Render(title)

	if chat.ID == "" {
		return strings.Join(clipLinesSlice([]string{
			header,
			"",
			lipgloss.NewStyle().Foreground(softFG).Width(width).Render("No active chat."),
			lipgloss.NewStyle().Foreground(softFG).Width(width).Render("Use `maybewhats doctor` to confirm the database path and preview backend."),
		}, height), "\n")
	}

	messages := m.currentMessages()
	if len(messages) == 0 {
		return strings.Join(clipLinesSlice([]string{
			header,
			lipgloss.NewStyle().Foreground(softFG).Render("No messages in current chat."),
		}, height), "\n")
	}

	blocks := make([]string, 0, len(messages))
	for i, message := range messages {
		selected := m.mode == ModeVisual && i >= min(m.visualAnchor, m.messageCursor) && i <= max(m.visualAnchor, m.messageCursor)
		active := i == m.messageCursor
		bubble := m.renderMessageBubble(message, width, active, selected)
		blocks = append(blocks, alignMessageBubble(bubble, width, message.IsOutgoing))
	}

	bodyHeight := max(1, height-1)
	body := messageViewport(blocks, clamp(m.messageCursor, 0, len(blocks)-1), bodyHeight)
	return strings.Join(append([]string{header}, body...), "\n")
}

func (m Model) renderMessageBubble(message store.Message, availableWidth int, active, selected bool) string {
	lineColor := incomingLine
	fg := primaryFG
	if message.IsOutgoing {
		lineColor = outgoingLine
		fg = outgoingFG
	}
	if selected {
		lineColor = selectedLine
		fg = primaryFG
	}
	if active {
		lineColor = activeBorder
	}

	sender := message.Sender
	if message.IsOutgoing {
		sender = "me"
	}
	if sender == "" {
		sender = "unknown"
	}

	meta := sender
	if !message.Timestamp.IsZero() {
		meta = fmt.Sprintf("%s  %s", meta, message.Timestamp.Format("15:04"))
	}

	body := strings.TrimSpace(message.Body)
	if body == "" {
		body = "(empty)"
	}

	maxBubbleWidth := bubbleWidth(availableWidth)
	contentWidth := clamp(max(bubbleTextWidth(meta, body), 16), 8, maxBubbleWidth-2)

	contourStyle := lipgloss.NewStyle().Foreground(lineColor)
	metaStyle := lipgloss.NewStyle().Foreground(softFG)
	bodyStyle := lipgloss.NewStyle().Foreground(fg)

	meta = truncateDisplay(meta, contentWidth)
	metaText := alignDisplay(meta, contentWidth, message.IsOutgoing)
	lines := []string{bubbleMessageLine(contourStyle, metaStyle, metaText, contentWidth, message.IsOutgoing)}
	for _, line := range strings.Split(wrapPlainText(body, contentWidth), "\n") {
		lines = append(lines, bubbleMessageLine(contourStyle, bodyStyle, padDisplay(line, contentWidth), contentWidth, message.IsOutgoing))
	}

	return strings.Join(lines, "\n")
}

func bubbleWidth(available int) int {
	if available < 28 {
		return max(10, available-2)
	}
	return min(max(22, available*46/100), available-2)
}

func bubbleTextWidth(meta, body string) int {
	widest := lipgloss.Width(meta)
	for _, line := range strings.Split(body, "\n") {
		lineWidth := 0
		for _, word := range strings.Fields(line) {
			if lineWidth > 0 {
				lineWidth++
			}
			lineWidth += lipgloss.Width(word)
			widest = max(widest, lipgloss.Width(word))
		}
		if lineWidth > 0 {
			widest = max(widest, lineWidth)
		}
	}
	return widest
}

func alignMessageBubble(bubble string, width int, outgoing bool) string {
	lines := strings.Split(bubble, "\n")
	blockWidth := 0
	for _, line := range lines {
		blockWidth = max(blockWidth, lipgloss.Width(line))
	}
	if !outgoing {
		for i, line := range lines {
			lines[i] = lipgloss.NewStyle().Width(width).Render(line)
		}
		return strings.Join(lines, "\n")
	}

	for i, line := range lines {
		indent := max(0, width-blockWidth)
		lines[i] = strings.Repeat(" ", indent) + line
	}
	return strings.Join(lines, "\n")
}

func bubbleMessageLine(contourStyle, textStyle lipgloss.Style, text string, width int, outgoing bool) string {
	text = truncateDisplay(text, width)
	text = padDisplay(text, width)
	if outgoing {
		return textStyle.Render(text) + " " + contourStyle.Render("│")
	}
	return contourStyle.Render("│") + " " + textStyle.Render(text)
}

func (m Model) renderInfo(width int) string {
	chat := m.currentChat()
	chatTitle := chat.Title
	if chatTitle == "" {
		chatTitle = "none"
	}
	message := ""
	if messages := m.currentMessages(); len(messages) > 0 {
		message = messages[m.messageCursor].Body
	}

	lines := []string{
		lipgloss.NewStyle().Bold(true).Foreground(accentFG).Render("Info"),
		fmt.Sprintf("chat: %s", chatTitle),
		fmt.Sprintf("mode: %s", m.mode),
		fmt.Sprintf("focus: %s", m.focus),
		fmt.Sprintf("backend: %s", m.previewReport.Selected),
		"",
		"message:",
		lipgloss.NewStyle().Foreground(softFG).Width(width).Render(message),
		"",
		"paths:",
		lipgloss.NewStyle().Foreground(softFG).Width(width).Render(m.paths.DatabaseFile),
	}

	if m.yankRegister != "" {
		lines = append(lines, "", "unnamed register:", lipgloss.NewStyle().Foreground(warnFG).Width(width).Render(m.yankRegister))
	}

	return strings.Join(lines, "\n")
}

func (m Model) renderStatus() string {
	left := fmt.Sprintf(" %s  %s  %s ", strings.ToUpper(string(m.mode)), strings.ToUpper(string(m.focus)), m.status)
	right := " no chats "
	if len(m.chats) > 0 {
		right = fmt.Sprintf(" chat %d/%d ", m.activeChat+1, len(m.chats))
	}

	style := lipgloss.NewStyle().
		Foreground(primaryFG).
		Background(lipgloss.Color("#1C252B"))

	return style.Width(m.width).Render(lipgloss.JoinHorizontal(lipgloss.Top,
		lipgloss.NewStyle().Width(max(0, m.width-lipgloss.Width(right))).Render(left),
		right,
	))
}

func (m Model) renderInput() string {
	content := ""
	switch m.mode {
	case ModeInsert:
		content = "insert> " + m.composer
	case ModeCommand:
		content = ":" + m.commandLine
	case ModeSearch:
		content = "/" + m.searchLine
	default:
		content = "normal> hjkl move  tab cycle  i insert  v visual  : command  / search  :preview  q quit"
	}

	return lipgloss.NewStyle().
		Foreground(softFG).
		Background(lipgloss.Color("#161E24")).
		Width(m.width).
		Render(" " + content)
}

func (m Model) panelStyle(focus Focus) lipgloss.Style {
	border := borderColor
	if m.focus == focus {
		border = activeBorder
	}

	return lipgloss.NewStyle().
		Border(lipgloss.NormalBorder()).
		BorderForeground(border).
		Background(panelBG).
		Padding(0, 1)
}
