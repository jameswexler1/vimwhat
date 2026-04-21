package ui

import (
	"fmt"
	"strings"
	"unicode/utf8"

	"github.com/charmbracelet/lipgloss"

	"maybewhats/internal/store"
)

var (
	softFG       = uiTheme.SoftFG
	primaryFG    = uiTheme.PrimaryFG
	accentFG     = uiTheme.AccentFG
	warnFG       = uiTheme.WarnFG
	borderColor  = uiTheme.Border
	activeBorder = uiTheme.ActiveBorder
	outgoingFG   = uiTheme.OutgoingFG
	incomingLine = uiTheme.IncomingLine
	outgoingLine = uiTheme.OutgoingLine
	selectedLine = uiTheme.SelectedLine
)

func (m Model) View() string {
	if m.width == 0 || m.height == 0 {
		return "loading..."
	}

	inputHeight := m.inputHeight()
	bodyHeight := max(3, m.height-1-inputHeight)
	body := m.renderBody(bodyHeight)
	status := m.renderStatus()
	input := m.renderInput()

	rendered := lipgloss.NewStyle().
		Foreground(primaryFG).
		Width(m.width).
		Render(lipgloss.JoinVertical(lipgloss.Left, body, status, input))

	return capLines(rendered, m.height)
}

func (m Model) renderBody(height int) string {
	if m.helpVisible {
		return m.renderPanel(FocusMessages, m.width, height, m.renderHelp(panelContentWidth(m.panelStyle(FocusMessages), m.width)))
	}

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
		block := blockLines(blocks[i])
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
	headerText := "Chats"
	if m.unreadOnly {
		headerText += " unread"
	}
	if m.activeSearch != "" {
		headerText += fmt.Sprintf(" /%s", truncateDisplay(m.activeSearch, max(4, width-8)))
	}
	header := lipgloss.NewStyle().Bold(true).Foreground(accentFG).Render(headerText)
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

		flags := make([]string, 0, 4)
		if chat.Pinned {
			flags = append(flags, "P")
		}
		if chat.Muted {
			flags = append(flags, "M")
		}
		if chat.HasDraft {
			flags = append(flags, "D")
		}
		if chat.Kind == "group" {
			flags = append(flags, "G")
		}

		left := fmt.Sprintf("%s %s", cursor, truncateDisplay(chat.Title, max(1, width-8)))
		suffix := ""
		if len(flags) > 0 {
			suffix = "[" + strings.Join(flags, "") + "]"
		}
		if chat.Unread > 0 {
			suffix += fmt.Sprintf(" %d", chat.Unread)
		}
		if suffix != "" {
			left = truncateDisplay(left, max(1, width-lipgloss.Width(suffix)-1)) + " " + suffix
		}

		line := padDisplay(left, width)
		if i == m.activeChat {
			line = lipgloss.NewStyle().Foreground(primaryFG).Bold(true).Render(line)
		} else {
			line = lipgloss.NewStyle().Foreground(softFG).Render(line)
		}
		lines = append(lines, lipgloss.NewStyle().Width(width).Render(line))

		preview := strings.TrimSpace(chat.LastPreview)
		if chat.HasDraft {
			if draft := strings.TrimSpace(m.draftsByChat[chat.ID]); draft != "" {
				preview = "draft: " + firstLine(draft)
			}
		}
		if preview == "" {
			preview = "no local messages"
		}
		previewLine := "  " + truncateDisplay(firstLine(preview), max(1, width-2))
		previewStyle := lipgloss.NewStyle().Foreground(softFG)
		if i == m.activeChat {
			previewStyle = previewStyle.Foreground(accentFG)
		}
		lines = append(lines, previewStyle.Width(width).Render(previewLine))
	}

	return strings.Join(lines, "\n")
}

func (m Model) renderMessages(width, height int) string {
	chat := m.currentChat()
	title := chat.Title
	if title == "" {
		title = "Messages"
	}
	header := m.renderMessageHeader(title, width)

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
	var lastDate string
	for i, message := range messages {
		date := messageDate(message)
		selected := m.mode == ModeVisual && i >= min(m.visualAnchor, m.messageCursor) && i <= max(m.visualAnchor, m.messageCursor)
		active := i == m.messageCursor
		bubble := m.renderMessageBubble(message, width, active, selected)
		block := alignMessageBubble(bubble, width, message.IsOutgoing)
		if date != "" && date != lastDate {
			block = renderDaySeparator(date, width) + "\n" + block
			lastDate = date
		}
		blocks = append(blocks, block)
	}

	bodyHeight := max(1, height-1)
	body := messageViewport(blocks, clamp(m.messageCursor, 0, len(blocks)-1), bodyHeight)
	return strings.Join(append([]string{header}, body...), "\n")
}

func (m Model) renderMessageHeader(title string, width int) string {
	parts := []string{title}
	if m.unreadOnly {
		parts = append(parts, "unread")
	}
	if m.activeSearch != "" {
		parts = append(parts, "/"+m.activeSearch)
	}
	if count := len(m.currentMessages()); count > 0 {
		parts = append(parts, fmt.Sprintf("%d msgs", count))
	}
	if draft := strings.TrimSpace(m.draftsByChat[m.currentChat().ID]); draft != "" {
		parts = append(parts, "draft")
	}

	return lipgloss.NewStyle().
		Bold(true).
		Foreground(accentFG).
		Render(truncateDisplay(strings.Join(parts, "  "), width))
}

func messageDate(message store.Message) string {
	if message.Timestamp.IsZero() {
		return ""
	}
	return message.Timestamp.Format("2006-01-02")
}

func renderDaySeparator(date string, width int) string {
	label := " " + date + " "
	if lipgloss.Width(label) >= width {
		return truncateDisplay(label, width)
	}
	left := (width - lipgloss.Width(label)) / 2
	right := max(0, width-lipgloss.Width(label)-left)
	return lipgloss.NewStyle().Foreground(borderColor).Render(strings.Repeat("-", left) + label + strings.Repeat("-", right))
}

func (m Model) renderMessageBubble(message store.Message, availableWidth int, active, selected bool) string {
	lineColor := incomingLine
	fg := primaryFG
	metaFG := softFG
	if message.IsOutgoing {
		lineColor = outgoingLine
		fg = outgoingFG
	}
	if selected {
		lineColor = selectedLine
		fg = primaryFG
		metaFG = warnFG
	}
	if active {
		lineColor = activeBorder
		metaFG = primaryFG
	}

	sender := message.Sender
	if message.IsOutgoing {
		sender = "me"
	}
	if sender == "" {
		sender = "unknown"
	}

	metaParts := []string{sender}
	if !message.Timestamp.IsZero() {
		metaParts = append(metaParts, message.Timestamp.Format("15:04"))
	}
	if message.IsOutgoing && message.Status != "" {
		metaParts = append(metaParts, message.Status)
	}
	meta := strings.Join(metaParts, " ")

	body := strings.TrimSpace(message.Body)
	if body == "" {
		body = "(empty)"
	}

	maxBubbleWidth := bubbleWidth(availableWidth)
	contentWidth := clamp(max(bubbleTextWidth(meta, body), 16), 8, maxBubbleWidth-2)

	contourStyle := lipgloss.NewStyle().Foreground(lineColor)
	metaStyle := lipgloss.NewStyle().Foreground(metaFG)
	bodyStyle := lipgloss.NewStyle().Foreground(fg)
	if m.activeSearch != "" && strings.Contains(strings.ToLower(body), strings.ToLower(m.activeSearch)) {
		bodyStyle = bodyStyle.Foreground(warnFG)
	}

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
		message = messages[clamp(m.messageCursor, 0, len(messages)-1)].Body
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
	filter := "all"
	if m.unreadOnly {
		filter = "unread"
	}
	sortMode := "recent"
	if m.pinnedFirst {
		sortMode = "pinned"
	}
	left := fmt.Sprintf(" %s %s  %s  %s/%s ", strings.ToUpper(string(m.mode)), strings.ToUpper(string(m.focus)), m.status, filter, sortMode)
	right := " no chats "
	if len(m.chats) > 0 {
		right = fmt.Sprintf(" chat %d/%d ", m.activeChat+1, len(m.chats))
	}

	style := lipgloss.NewStyle().Foreground(primaryFG)
	if !barsTransparent() {
		style = style.Background(uiTheme.BarBG)
	}

	leftWidth := max(0, m.width-lipgloss.Width(right))
	left = truncateDisplay(left, leftWidth)
	return style.Width(m.width).Render(lipgloss.JoinHorizontal(lipgloss.Top,
		lipgloss.NewStyle().Width(leftWidth).Render(left),
		right,
	))
}

func (m Model) renderInput() string {
	switch m.mode {
	case ModeInsert:
		return m.renderComposer()
	case ModeCommand:
		return m.renderPrompt("COMMAND", ":"+m.commandLine, "enter run  esc cancel")
	case ModeSearch:
		return m.renderPrompt("SEARCH", "/"+m.searchLine, "enter search  esc cancel  empty clears")
	default:
		return m.renderPrompt("NORMAL", "j/k move  5j counts  enter open  i insert  : command  / search  ? help", "")
	}
}

func (m Model) renderPrompt(label, content, hint string) string {
	if hint != "" {
		content = content + "  " + hint
	}
	content = truncateDisplay(content, max(1, m.width-2))
	style := lipgloss.NewStyle().Foreground(softFG).Width(m.width)
	if !barsTransparent() {
		style = style.Background(uiTheme.BarBG)
	}
	prefix := lipgloss.NewStyle().Bold(true).Foreground(accentFG).Render(" " + label + " ")
	return style.Render(truncateDisplay(prefix+content, m.width))
}

func (m Model) renderComposer() string {
	chat := m.currentChat().Title
	if chat == "" {
		chat = "no chat"
	}

	header := fmt.Sprintf(" INSERT to %s  enter send  ctrl+j newline  esc save draft", chat)
	lines := []string{truncateDisplay(header, m.width)}

	bodyLines := composerLines(m.composer)
	maxBodyLines := max(1, m.inputHeight()-1)
	if len(bodyLines) > maxBodyLines {
		bodyLines = bodyLines[len(bodyLines)-maxBodyLines:]
	}
	for i, line := range bodyLines {
		if i == len(bodyLines)-1 {
			line += "▌"
		}
		lines = append(lines, truncateDisplay("> "+line, m.width))
	}

	style := lipgloss.NewStyle().Foreground(primaryFG).Width(m.width)
	if !barsTransparent() {
		style = style.Background(uiTheme.BarBG)
	}
	return style.Render(strings.Join(lines, "\n"))
}

func (m Model) inputHeight() int {
	if m.mode != ModeInsert {
		return 1
	}
	return min(4, max(2, len(composerLines(m.composer))+1))
}

func (m Model) renderHelp(width int) string {
	lines := []string{
		lipgloss.NewStyle().Bold(true).Foreground(accentFG).Render("maybewhats help"),
		"",
		"normal:  j/k move    5j count    g/G top/bottom    h/l pane    tab cycle",
		"         enter open  i insert    v visual          / search    : command",
		"         u unread    p sort      n/N next search   ? help      q quit",
		"insert:  enter send  ctrl+j newline  esc save draft",
		"visual:  j/k extend  y yank          esc normal",
		"command: clear-search  filter unread/all  sort pinned/recent  preview",
		"",
		"state:",
		fmt.Sprintf("mode=%s focus=%s filter=%s sort=%s search=%q",
			m.mode,
			m.focus,
			boolLabel(m.unreadOnly, "unread", "all"),
			boolLabel(m.pinnedFirst, "pinned", "recent"),
			m.activeSearch,
		),
	}

	for i, line := range lines {
		lines[i] = truncateDisplay(line, width)
	}
	return strings.Join(lines, "\n")
}

func boolLabel(value bool, yes, no string) string {
	if value {
		return yes
	}
	return no
}

func firstLine(value string) string {
	line, _, _ := strings.Cut(value, "\n")
	return line
}

func (m Model) panelStyle(focus Focus) lipgloss.Style {
	border := borderColor
	if m.focus == focus {
		border = activeBorder
	}

	return lipgloss.NewStyle().
		Border(lipgloss.NormalBorder()).
		BorderForeground(border).
		Padding(0, 1)
}

func composerLines(value string) []string {
	if value == "" {
		return []string{""}
	}
	return strings.Split(value, "\n")
}
