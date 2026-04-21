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
	bodyHeight := m.height - 1 - inputHeight
	if bodyHeight < 1 {
		bodyHeight = 1
	}
	body := capLines(m.renderBody(bodyHeight), bodyHeight)
	status := m.renderStatus()
	parts := []string{body, status}
	if inputHeight > 0 {
		parts = append(parts, m.renderInput())
	}

	rendered := lipgloss.NewStyle().
		Foreground(primaryFG).
		Width(m.width).
		Render(lipgloss.JoinVertical(lipgloss.Left, parts...))

	return capLines(trimRightSpaces(rendered), m.height)
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

func trimRightSpaces(content string) string {
	lines := strings.Split(content, "\n")
	for i, line := range lines {
		lines[i] = strings.TrimRight(line, " ")
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

type messageBlock struct {
	lines []string
}

func messageViewport(blocks []messageBlock, scrollTop, cursor, height int) []string {
	height = max(1, height)
	if len(blocks) == 0 {
		return nil
	}

	selected := clamp(cursor, 0, len(blocks)-1)
	scrollTop = adjustedMessageScrollTop(blocks, scrollTop, selected, height)
	var out []string
	used := 0

	for i := scrollTop; i < len(blocks) && used < height; i++ {
		block := blocks[i].lines
		if used+len(block) > height {
			remaining := height - used
			if remaining > 0 {
				if i == selected {
					out = append(out, tailLines(block, remaining)...)
				} else {
					out = append(out, block[:remaining]...)
				}
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

func adjustedMessageScrollTop(blocks []messageBlock, scrollTop, selected, height int) int {
	scrollTop = clamp(scrollTop, 0, len(blocks)-1)
	if selected < scrollTop {
		return selected
	}

	for scrollTop < selected && !messageBlockFullyVisible(blocks, scrollTop, selected, height) {
		scrollTop++
	}
	return scrollTop
}

func messageBlockFullyVisible(blocks []messageBlock, scrollTop, selected, height int) bool {
	used := 0
	for i := scrollTop; i < len(blocks); i++ {
		blockHeight := len(blocks[i].lines)
		if i == selected {
			return used+blockHeight <= height || (used == 0 && blockHeight >= height)
		}
		if used+blockHeight >= height {
			return false
		}
		used += blockHeight
	}
	return false
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
	if len(messages) == 0 && m.mode != ModeInsert {
		return strings.Join(clipLinesSlice([]string{
			header,
			lipgloss.NewStyle().Foreground(softFG).Render("No messages in current chat."),
		}, height), "\n")
	}

	blocks := make([]messageBlock, 0, len(messages))
	var lastDate string
	for i, message := range messages {
		date := messageDate(message)
		selected := m.mode == ModeVisual && i >= min(m.visualAnchor, m.messageCursor) && i <= max(m.visualAnchor, m.messageCursor)
		active := i == m.messageCursor
		bubble := m.renderMessageBubble(message, width, active, selected)
		lines := strings.Split(alignMessageBubble(bubble, width, message.IsOutgoing), "\n")
		if date != "" && date != lastDate {
			lines = append([]string{renderDaySeparator(date, width)}, lines...)
			lastDate = date
		}
		blocks = append(blocks, messageBlock{lines: lines})
	}

	bodyHeight := max(1, height-1)
	composerHeight := 0
	if m.mode == ModeInsert {
		composerHeight = min(m.composerHeight(), bodyHeight)
	}

	body := make([]string, 0, bodyHeight)
	if len(blocks) == 0 {
		body = append(body, lipgloss.NewStyle().Foreground(softFG).Render("No messages in current chat."))
	} else {
		body = append(body, messageViewport(blocks, m.messageScrollTop, clamp(m.messageCursor, 0, len(blocks)-1), bodyHeight)...)
	}
	if composerHeight > 0 {
		body = padLines(body, bodyHeight)
		composer := padLines(strings.Split(clipLines(m.renderComposer(max(1, width-2)), composerHeight), "\n"), composerHeight)
		start := max(0, bodyHeight-composerHeight)
		copy(body[start:], composer)
	}
	return strings.Join(clipLinesSlice(append([]string{header}, body...), height), "\n")
}

func padLines(lines []string, height int) []string {
	height = max(1, height)
	if len(lines) >= height {
		return lines[:height]
	}
	out := append([]string{}, lines...)
	for len(out) < height {
		out = append(out, "")
	}
	return out
}

func (m Model) renderMessageHeader(title string, width int) string {
	parts := []string{title}
	if m.unreadOnly {
		parts = append(parts, "unread")
	}
	if m.activeSearch != "" {
		parts = append(parts, "/"+m.activeSearch)
	}
	if m.messageFilter != "" {
		parts = append(parts, "filter:"+m.messageFilter)
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
	lines := []string{bubbleMessageLine(contourStyle, metaStyle, meta, contentWidth, message.IsOutgoing)}
	for _, line := range strings.Split(wrapPlainText(body, contentWidth), "\n") {
		lines = append(lines, bubbleMessageLine(contourStyle, bodyStyle, line, contentWidth, message.IsOutgoing))
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
	if !outgoing {
		return strings.Join(lines, "\n")
	}

	for i, line := range lines {
		indent := max(0, width-lipgloss.Width(line)-3)
		lines[i] = truncateDisplay(strings.Repeat(" ", indent)+line, width)
	}
	return strings.Join(lines, "\n")
}

func bubbleMessageLine(contourStyle, textStyle lipgloss.Style, text string, width int, outgoing bool) string {
	text = truncateDisplay(text, width)
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
	chatFilter := "all"
	if m.unreadOnly {
		chatFilter = "unread"
	}
	sortMode := "recent"
	if m.pinnedFirst {
		sortMode = "pinned"
	}

	mode := strings.ToUpper(string(m.mode))
	focus := strings.ToUpper(string(m.focus))
	chatTitle := "no chat"
	if chat := m.currentChat(); chat.Title != "" {
		chatTitle = chat.Title
	}
	left := strings.Join([]string{
		statusSegment(" "+mode+" ", uiTheme.BarBG, modeStatusColor(m.mode), true),
		statusSegment(" "+focus+" ", primaryFG, borderColor, false),
		statusSegment(" "+truncateDisplay(chatTitle, max(8, m.width/5))+" ", primaryFG, "", false),
	}, "")

	search := ""
	if m.activeSearch != "" {
		search = " /" + truncateDisplay(m.activeSearch, 16)
	}
	messageFilter := ""
	if m.messageFilter != "" {
		messageFilter = " filter:" + truncateDisplay(m.messageFilter, 16)
	}
	center := " " + truncateDisplay(m.status, max(8, m.width/3)) + " "
	rightText := fmt.Sprintf(" %s/%s%s%s ", chatFilter, sortMode, search, messageFilter)
	rightCount := " no chats "
	if len(m.chats) > 0 {
		rightCount = fmt.Sprintf(" chat %d/%d ", m.activeChat+1, len(m.chats))
	}
	right := statusSegment(rightText+rightCount, primaryFG, borderColor, false)

	spacerStyle := lipgloss.NewStyle().Foreground(softFG)
	if !barsTransparent() {
		spacerStyle = spacerStyle.Background(uiTheme.BarBG)
	}

	used := lipgloss.Width(left) + lipgloss.Width(center) + lipgloss.Width(right)
	centerWidth := lipgloss.Width(center)
	if used > m.width {
		centerWidth = max(0, centerWidth-(used-m.width))
		center = truncateDisplay(center, centerWidth)
		used = lipgloss.Width(left) + lipgloss.Width(center) + lipgloss.Width(right)
	}
	if used > m.width {
		return truncateDisplay(" "+mode+" "+focus+" "+m.status+" "+rightText+rightCount, m.width)
	}
	spacer := spacerStyle.Render(strings.Repeat(" ", max(0, m.width-used)))
	return left + center + spacer + right
}

func statusSegment(text string, fg, bg lipgloss.Color, bold bool) string {
	style := lipgloss.NewStyle().Foreground(fg).Bold(bold)
	if bg != "" {
		style = style.Background(bg)
	}
	return style.Render(text)
}

func modeStatusColor(mode Mode) lipgloss.Color {
	switch mode {
	case ModeInsert:
		return outgoingLine
	case ModeVisual:
		return selectedLine
	case ModeCommand:
		return activeBorder
	case ModeSearch:
		return warnFG
	default:
		return accentFG
	}
}

func (m Model) renderInput() string {
	switch m.mode {
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
	prefix := "[" + label + "] "
	content = truncateDisplay(prefix+content, max(1, m.width-1))
	style := lipgloss.NewStyle().Foreground(softFG).Width(m.width)
	if !barsTransparent() {
		style = style.Background(uiTheme.BarBG)
	}
	return style.Render(" " + content)
}

func (m Model) renderComposer(width int) string {
	chat := m.currentChat().Title
	if chat == "" {
		chat = "no chat"
	}

	header := fmt.Sprintf(" [INSERT] to %s | enter send | ctrl+j newline | esc save draft", chat)
	separator := lipgloss.NewStyle().Foreground(borderColor).Render(strings.Repeat("-", max(1, width)))
	lines := []string{separator, truncateDisplay(header, width)}

	bodyLines := composerLines(m.composer)
	maxBodyLines := max(1, m.composerHeight()-2)
	if len(bodyLines) > maxBodyLines {
		bodyLines = bodyLines[len(bodyLines)-maxBodyLines:]
	}
	for i, line := range bodyLines {
		if i == len(bodyLines)-1 {
			line += "▌"
		}
		lines = append(lines, truncateDisplay("> "+line, width))
	}

	style := lipgloss.NewStyle().Foreground(primaryFG).Width(width)
	if !barsTransparent() {
		style = style.Background(uiTheme.BarBG)
	}
	return style.Render(strings.Join(lines, "\n"))
}

func (m Model) inputHeight() int {
	if m.mode == ModeInsert {
		return 0
	}
	return 1
}

func (m Model) composerHeight() int {
	if m.mode != ModeInsert {
		return 0
	}
	return min(5, max(3, len(composerLines(m.composer))+2))
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
		"command: clear-search  filter unread/all  filter messages <text>  filter clear",
		"         sort pinned/recent  preview",
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
