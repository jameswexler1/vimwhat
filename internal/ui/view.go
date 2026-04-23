package ui

import (
	"fmt"
	"strings"
	"unicode"
	"unicode/utf8"

	"github.com/charmbracelet/lipgloss"

	"vimwhat/internal/media"
	"vimwhat/internal/store"
)

var (
	softFG               = uiTheme.SoftFG
	primaryFG            = uiTheme.PrimaryFG
	accentFG             = uiTheme.AccentFG
	warnFG               = uiTheme.WarnFG
	searchMatchFG        = uiTheme.SearchMatchFG
	searchCurrentMatchFG = uiTheme.SearchCurrentMatchFG
	searchCurrentMatchBG = uiTheme.SearchCurrentMatchBG
	borderColor          = uiTheme.Border
	activeBorder         = uiTheme.ActiveBorder
	outgoingFG           = uiTheme.OutgoingFG
	incomingLine         = uiTheme.IncomingLine
	outgoingLine         = uiTheme.OutgoingLine
	selectedLine         = uiTheme.SelectedLine
	focusedLine          = uiTheme.FocusedLine
)

const (
	chatHeaderHeight = 1
	chatCellHeight   = 4
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

	if m.compactLayout {
		switch m.focus {
		case FocusChats:
			style := m.panelStyle(FocusChats)
			return m.renderPanel(FocusChats, m.width, height, m.renderChats(panelContentWidth(style, m.width), panelContentHeight(style, height)))
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
	chatStyle := m.panelStyle(FocusChats)
	chats := m.renderPanel(FocusChats, chatWidth, height, m.renderChats(panelContentWidth(chatStyle, chatWidth), panelContentHeight(chatStyle, height)))
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
	contentWidth := panelContentWidth(style, width)
	contentHeight := panelContentHeight(style, height)
	content = clipLines(content, contentHeight)

	filled := lipgloss.Place(
		contentWidth,
		contentHeight,
		lipgloss.Left,
		lipgloss.Top,
		content,
		lipgloss.WithWhitespaceChars(" "),
	)

	return style.
		Width(panelBoxWidth(style, width)).
		Height(panelBoxHeight(style, height)).
		Render(filled)
}

func panelContentWidth(style lipgloss.Style, totalWidth int) int {
	return max(1, panelBoxWidth(style, totalWidth)-style.GetHorizontalPadding())
}

func panelContentHeight(style lipgloss.Style, totalHeight int) int {
	return max(1, panelBoxHeight(style, totalHeight)-style.GetVerticalPadding())
}

func panelBoxWidth(style lipgloss.Style, totalWidth int) int {
	return max(1, totalWidth-style.GetHorizontalMargins()-style.GetHorizontalBorderSize())
}

func panelBoxHeight(style lipgloss.Style, totalHeight int) int {
	return max(1, totalHeight-style.GetVerticalMargins()-style.GetVerticalBorderSize())
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

type messageBlockSpan struct {
	index int
	start int
	end   int
}

const maxMessageRenderWindow = 48

func messageViewport(blocks []messageBlock, scrollTop, cursor, height int) []string {
	spans := messageViewportSpans(blocks, scrollTop, cursor, height)
	if len(spans) == 0 {
		return nil
	}
	return messageLinesForSpans(blocks, spans)
}

func messageViewportSpans(blocks []messageBlock, scrollTop, cursor, height int) []messageBlockSpan {
	height = max(1, height)
	if len(blocks) == 0 {
		return nil
	}

	selected := clamp(cursor, 0, len(blocks)-1)
	if selected == len(blocks)-1 {
		return bottomMessageViewportSpans(blocks, height)
	}
	if clamp(scrollTop, 0, len(blocks)-1) > selected {
		return bottomMessageViewportSpans(blocks, height)
	}
	scrollTop = adjustedMessageScrollTop(blocks, scrollTop, selected, height)
	scrollTop = clampMessageScrollTop(blocks, scrollTop, height)
	spans := make([]messageBlockSpan, 0, len(blocks)-scrollTop)
	used := 0

	for i := scrollTop; i < len(blocks) && used < height; i++ {
		blockHeight := len(blocks[i].lines)
		if blockHeight == 0 {
			continue
		}
		if used+blockHeight > height {
			remaining := height - used
			if remaining > 0 && i == selected {
				end := min(blockHeight, remaining)
				spans = append(spans, messageBlockSpan{index: i, start: 0, end: end})
			}
			break
		}
		spans = append(spans, messageBlockSpan{index: i, start: 0, end: blockHeight})
		used += blockHeight
	}

	if len(spans) == 0 {
		blockHeight := len(blocks[selected].lines)
		if blockHeight == 0 {
			return nil
		}
		spans = append(spans, messageBlockSpan{index: selected, start: 0, end: min(blockHeight, height)})
	}
	return spans
}

func messageLinesForSpans(blocks []messageBlock, spans []messageBlockSpan) []string {
	var out []string
	for _, span := range spans {
		if span.index < 0 || span.index >= len(blocks) {
			continue
		}
		lines := blocks[span.index].lines
		start := clamp(span.start, 0, len(lines))
		end := clamp(span.end, start, len(lines))
		out = append(out, lines[start:end]...)
	}
	return out
}

func bottomMessageViewportSpans(blocks []messageBlock, height int) []messageBlockSpan {
	height = max(1, height)
	if messageBlocksHeight(blocks) <= height {
		spans := make([]messageBlockSpan, 0, len(blocks))
		for i, block := range blocks {
			if len(block.lines) > 0 {
				spans = append(spans, messageBlockSpan{index: i, start: 0, end: len(block.lines)})
			}
		}
		return spans
	}

	var spans []messageBlockSpan
	used := 0

	for i := len(blocks) - 1; i >= 0; i-- {
		blockHeight := len(blocks[i].lines)
		if blockHeight == 0 {
			continue
		}
		if used+blockHeight > height {
			remaining := height - used
			if remaining > 0 {
				spans = append([]messageBlockSpan{{
					index: i,
					start: blockHeight - remaining,
					end:   blockHeight,
				}}, spans...)
			}
			break
		}
		spans = append([]messageBlockSpan{{index: i, start: 0, end: blockHeight}}, spans...)
		used += blockHeight
	}
	return spans
}

func clampMessageScrollTop(blocks []messageBlock, scrollTop, height int) int {
	scrollTop = clamp(scrollTop, 0, len(blocks)-1)
	if messageBlocksHeight(blocks) <= height {
		return 0
	}
	for scrollTop > 0 && messageBlocksHeight(blocks[scrollTop:]) < height {
		scrollTop--
	}
	return scrollTop
}

func messageBlocksHeight(blocks []messageBlock) int {
	total := 0
	for _, block := range blocks {
		total += len(block.lines)
	}
	return total
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

func sanitizeDisplayLine(value string) string {
	value = sanitizeDisplayText(value, false)
	return strings.Join(strings.Fields(value), " ")
}

func sanitizeDisplayBody(value string) string {
	return sanitizeDisplayText(value, true)
}

func sanitizeDisplayText(value string, allowNewlines bool) string {
	if value == "" {
		return ""
	}
	var out strings.Builder
	out.Grow(len(value))
	for _, r := range value {
		switch r {
		case '\n':
			if allowNewlines {
				out.WriteRune(r)
			} else {
				out.WriteRune(' ')
			}
		case '\r', '\t':
			out.WriteRune(' ')
		default:
			if unicode.IsControl(r) {
				continue
			}
			out.WriteRune(r)
		}
	}
	return out.String()
}

type searchSegment struct {
	text  string
	match bool
}

func splitSearchSegments(value, query string) []searchSegment {
	query = strings.TrimSpace(query)
	if value == "" || query == "" {
		return []searchSegment{{text: value}}
	}

	valueRunes := []rune(value)
	queryRunes := []rune(query)
	if len(queryRunes) == 0 || len(valueRunes) < len(queryRunes) {
		return []searchSegment{{text: value}}
	}

	var segments []searchSegment
	start := 0
	for i := 0; i <= len(valueRunes)-len(queryRunes); {
		candidate := string(valueRunes[i : i+len(queryRunes)])
		if strings.EqualFold(candidate, query) {
			if start < i {
				segments = append(segments, searchSegment{text: string(valueRunes[start:i])})
			}
			segments = append(segments, searchSegment{text: candidate, match: true})
			i += len(queryRunes)
			start = i
			continue
		}
		i++
	}
	if start < len(valueRunes) {
		segments = append(segments, searchSegment{text: string(valueRunes[start:])})
	}
	if len(segments) == 0 {
		return []searchSegment{{text: value}}
	}
	return segments
}

func containsSearchMatch(value, query string) bool {
	for _, segment := range splitSearchSegments(value, query) {
		if segment.match {
			return true
		}
	}
	return false
}

func renderSearchHighlightedText(value, query string, baseStyle lipgloss.Style, current bool) string {
	segments := splitSearchSegments(value, query)
	if len(segments) == 1 && !segments[0].match {
		return baseStyle.Render(value)
	}

	matchStyle := baseStyle.Italic(true).Underline(true).Foreground(searchMatchFG)
	currentMatchStyle := baseStyle.Italic(true).Underline(true).Foreground(searchCurrentMatchFG).Background(searchCurrentMatchBG)

	var out strings.Builder
	for _, segment := range segments {
		style := baseStyle
		if segment.match {
			if current {
				style = currentMatchStyle
			} else {
				style = matchStyle
			}
		}
		out.WriteString(style.Render(segment.text))
	}
	return out.String()
}

func (m Model) renderChats(width, height int) string {
	var lines []string
	headerText := "Chats"
	if m.unreadOnly {
		headerText += " unread"
	}
	if m.activeSearch != "" {
		headerText += fmt.Sprintf(" /%s", truncateDisplay(sanitizeDisplayLine(m.activeSearch), max(4, width-8)))
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

	visibleCells := visibleChatCellCount(height)
	if visibleCells == 0 {
		return strings.Join(lines, "\n")
	}
	start := adjustedChatScrollTop(m.chatScrollTop, m.activeChat, len(m.chats), visibleCells)
	for i := start; i < len(m.chats) && i < start+visibleCells; i++ {
		lines = append(lines, m.renderChatCell(m.chats[i], i == m.activeChat, width))
	}

	return strings.Join(lines, "\n")
}

func (m Model) renderChatCell(chat store.Chat, active bool, width int) string {
	border := borderColor
	previewFG := softFG
	if active {
		border = activeBorder
		previewFG = accentFG
	}

	style := lipgloss.NewStyle().
		Border(lipgloss.NormalBorder()).
		BorderForeground(border).
		Padding(0, 1)
	contentWidth := panelContentWidth(style, width)

	title := sanitizeDisplayLine(chat.DisplayTitle())
	if title == "" {
		title = "unknown"
	}
	avatar := chatAvatarBadge(title, chat.Kind)
	textWidth := max(1, contentWidth-lipgloss.Width(avatar)-1)
	chatSearchQuery := m.chatSearchQuery()
	titleLine := renderChatTitleLine(title, chatSuffix(chat), textWidth, chatSearchQuery, active)
	previewLine := truncateDisplay(m.chatPreview(chat), textWidth)

	avatarStyle := lipgloss.NewStyle().Foreground(border)
	previewStyle := lipgloss.NewStyle().Foreground(previewFG)
	content := strings.Join([]string{
		avatarStyle.Render(avatar) + " " + titleLine,
		avatarStyle.Render(strings.Repeat(" ", lipgloss.Width(avatar))) + " " + previewStyle.Render(previewLine),
	}, "\n")

	return style.Width(panelBoxWidth(style, width)).Render(content)
}

func renderChatTitleLine(title, suffix string, width int, query string, current bool) string {
	width = max(1, width)
	titleStyle := lipgloss.NewStyle().Foreground(softFG)
	if current {
		titleStyle = titleStyle.Foreground(primaryFG).Bold(true)
	}
	suffixStyle := titleStyle
	if suffix == "" {
		title = truncateDisplay(title, width)
		return renderSearchHighlightedText(title, query, titleStyle, current && containsSearchMatch(title, query))
	}

	suffixWidth := lipgloss.Width(suffix)
	if suffixWidth >= width {
		return suffixStyle.Render(truncateDisplay(suffix, width))
	}

	title = truncateDisplay(title, max(1, width-suffixWidth-1))
	gap := max(1, width-lipgloss.Width(title)-suffixWidth)
	highlightedTitle := renderSearchHighlightedText(title, query, titleStyle, current && containsSearchMatch(title, query))
	return highlightedTitle + strings.Repeat(" ", gap) + suffixStyle.Render(suffix)
}

func chatAvatarBadge(title, kind string) string {
	initials := chatInitials(title)
	if kind == "group" && initials == "" {
		initials = "#"
	}
	if initials == "" {
		initials = "?"
	}
	return "[" + truncateDisplay(initials, 2) + "]"
}

func chatInitials(title string) string {
	words := strings.Fields(title)
	if len(words) == 0 {
		return ""
	}
	var initials []rune
	for _, word := range words {
		for _, r := range word {
			if r == '[' || r == '(' {
				continue
			}
			initials = append(initials, []rune(strings.ToUpper(string(r)))[0])
			break
		}
		if len(initials) == 2 {
			break
		}
	}
	return string(initials)
}

func (m Model) chatPreview(chat store.Chat) string {
	preview := strings.TrimSpace(sanitizeDisplayBody(chat.LastPreview))
	if chat.HasDraft {
		if draft := strings.TrimSpace(sanitizeDisplayBody(m.draftsByChat[chat.ID])); draft != "" {
			preview = "draft: " + firstLine(draft)
		}
	}
	if preview == "" {
		return "no local messages"
	}
	return sanitizeDisplayLine(firstLine(preview))
}

func chatSuffix(chat store.Chat) string {
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

	suffix := ""
	if len(flags) > 0 {
		suffix = "[" + strings.Join(flags, "") + "]"
	}
	if chat.Unread > 0 {
		if suffix != "" {
			suffix += " "
		}
		suffix += fmt.Sprintf("%d", chat.Unread)
	}
	return suffix
}

func chatTitleLine(title, suffix string, width int) string {
	width = max(1, width)
	if suffix == "" {
		return truncateDisplay(title, width)
	}
	suffixWidth := lipgloss.Width(suffix)
	if suffixWidth >= width {
		return truncateDisplay(suffix, width)
	}

	title = truncateDisplay(title, max(1, width-suffixWidth-1))
	gap := max(1, width-lipgloss.Width(title)-suffixWidth)
	return truncateDisplay(title+strings.Repeat(" ", gap)+suffix, width)
}

func visibleChatCellCount(height int) int {
	bodyHeight := height - chatHeaderHeight
	if bodyHeight <= 0 {
		return 0
	}
	return max(1, bodyHeight/chatCellHeight)
}

func adjustedChatScrollTop(scrollTop, active, total, visible int) int {
	if total <= 0 || visible <= 0 {
		return 0
	}
	active = clamp(active, 0, total-1)
	maxTop := max(0, total-visible)
	scrollTop = clamp(scrollTop, 0, maxTop)
	if active < scrollTop {
		return active
	}
	if active >= scrollTop+visible {
		return clamp(active-visible+1, 0, maxTop)
	}
	return scrollTop
}

func (m Model) renderMessages(width, height int) string {
	chat := m.currentChat()
	title := sanitizeDisplayLine(chat.DisplayTitle())
	if title == "" {
		title = "Messages"
	}
	header := m.renderMessageHeader(title, width)

	if chat.ID == "" {
		return strings.Join(clipLinesSlice([]string{
			header,
			"",
			lipgloss.NewStyle().Foreground(softFG).Width(width).Render("No active chat."),
			lipgloss.NewStyle().Foreground(softFG).Width(width).Render("Use `vimwhat doctor` to confirm the database path and preview backend."),
		}, height), "\n")
	}

	messages := m.currentMessages()
	bodyHeight := max(1, height-1)
	footer := m.renderMessageFooter(max(1, width-2))
	footerHeight := min(countLines(footer), bodyHeight)
	messageHeight := max(1, bodyHeight-footerHeight)
	start, end := m.visibleMessageRange(len(messages), messageHeight)

	blocks := make([]messageBlock, 0, end-start)
	visibleOverlays := m.visibleOverlayIdentifiers()
	var lastDate string
	if start > 0 {
		lastDate = messageDate(messages[start-1])
	}
	for i := start; i < end; i++ {
		message := messages[i]
		date := messageDate(message)
		selected := m.mode == ModeVisual && i >= min(m.visualAnchor, m.messageCursor) && i <= max(m.visualAnchor, m.messageCursor)
		active := i == m.messageCursor
		bubble := m.renderMessageBubbleForViewport(message, width, active, selected, visibleOverlays)
		lines := strings.Split(alignMessageBubble(bubble, width, message.IsOutgoing), "\n")
		if date != "" && date != lastDate {
			lines = append([]string{renderDaySeparator(date, width)}, lines...)
			lastDate = date
		}
		blocks = append(blocks, messageBlock{lines: lines})
	}

	body := make([]string, 0, bodyHeight)
	if len(blocks) == 0 {
		body = append(body, lipgloss.NewStyle().Foreground(softFG).Render("No messages in current chat."))
	} else {
		localCursor := clamp(m.messageCursor-start, 0, len(blocks)-1)
		localScrollTop := localMessageViewportScrollTop(m.messageScrollTop, m.messageCursor, localCursor, len(blocks))
		body = append(body, messageViewport(
			blocks,
			localScrollTop,
			localCursor,
			messageHeight,
		)...)
	}
	if footerHeight > 0 {
		if len(blocks) == 0 || messageBlocksHeight(blocks) <= messageHeight {
			body = padLines(body, messageHeight)
		} else {
			body = padTopLines(body, messageHeight)
		}
		body = append(body, padLines(strings.Split(clipLines(footer, footerHeight), "\n"), footerHeight)...)
	}
	return strings.Join(clipLinesSlice(append([]string{header}, body...), height), "\n")
}

func localMessageViewportScrollTop(globalScrollTop, globalCursor, localCursor, blockCount int) int {
	if blockCount <= 0 {
		return 0
	}
	if globalScrollTop > globalCursor {
		return clamp(localCursor+1, 0, blockCount-1)
	}
	return 0
}

func (m Model) visibleMessageRange(count, height int) (int, int) {
	if count <= 0 {
		return 0, 0
	}
	window := min(count, max(24, min(maxMessageRenderWindow, max(1, height)*3)))
	cursor := clamp(m.messageCursor, 0, count-1)
	scrollTop := clamp(m.messageScrollTop, 0, count-1)

	if cursor == count-1 {
		return max(0, count-window), count
	}

	overscan := max(4, min(height, window/3))
	start := max(0, min(scrollTop, cursor)-overscan)
	end := min(count, start+window)
	if cursor >= end {
		end = min(count, cursor+overscan+1)
		start = max(0, end-window)
	}
	if scrollTop >= end {
		end = min(count, scrollTop+overscan+1)
		start = max(0, end-window)
	}
	return start, end
}

func countLines(content string) int {
	if content == "" {
		return 0
	}
	return len(strings.Split(content, "\n"))
}

func padTopLines(lines []string, height int) []string {
	height = max(1, height)
	if len(lines) >= height {
		return lines[len(lines)-height:]
	}
	padding := make([]string, height-len(lines))
	return append(padding, lines...)
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
	parts := []string{sanitizeDisplayLine(title)}
	if m.unreadOnly {
		parts = append(parts, "unread")
	}
	if m.activeSearch != "" {
		parts = append(parts, "/"+sanitizeDisplayLine(m.activeSearch))
	}
	if m.messageFilter != "" {
		parts = append(parts, "filter:"+sanitizeDisplayLine(m.messageFilter))
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
	return m.renderMessageBubbleForViewport(message, availableWidth, active, selected, nil)
}

func (m Model) renderMessageBubbleForViewport(message store.Message, availableWidth int, active, selected bool, visibleOverlays map[string]bool) string {
	border := incomingLine
	fg := primaryFG
	metaFG := softFG
	if message.IsOutgoing {
		border = outgoingLine
		fg = outgoingFG
	}
	if selected {
		border = selectedLine
		fg = primaryFG
		metaFG = warnFG
	}
	if active {
		border = focusedLine
		metaFG = primaryFG
	}

	body := strings.TrimSpace(sanitizeDisplayBody(message.Body))
	if body == "" && len(message.Media) == 0 {
		body = "(empty)"
	}
	meta := messageBubbleMeta(message)
	sender := ""
	if shouldShowMessageSender(m.currentChat(), message) {
		sender = sanitizeDisplayLine(message.Sender)
		if sender == "" {
			sender = "unknown"
		}
	}

	boxStyle := lipgloss.NewStyle().
		Border(lipgloss.RoundedBorder()).
		BorderForeground(border).
		Padding(0, 1)
	maxBubbleWidth := max(6, bubbleWidth(availableWidth))
	if m.messageUsesMediaBubbleWidth(message, active) {
		maxBubbleWidth = max(6, mediaBubbleWidth(availableWidth))
	}
	previewWidth, _ := m.previewDimensions()
	contentWidth := m.bubbleContentWidth(boxStyle, maxBubbleWidth, body, meta, sender, message, previewWidth)
	bodyStyle := lipgloss.NewStyle().Foreground(fg)
	messageSearchQuery := m.messageSearchQuery()

	var lines []string
	if sender != "" {
		lines = append(lines, lipgloss.NewStyle().Foreground(metaFG).Bold(true).Render(truncateDisplay(sender, contentWidth)))
	}
	for _, item := range message.Media {
		if preview, ok := m.mediaPreview(message, item, contentWidth); ok {
			lines = append(lines, renderPreviewLines(preview, contentWidth, overlayPreviewVisible(preview, visibleOverlays))...)
		} else {
			state := m.mediaAttachmentState(message, item)
			lines = append(lines, renderAttachmentLine(item, contentWidth, active || selected, state))
		}
	}
	if body != "" {
		for _, line := range strings.Split(wrapPlainText(body, contentWidth), "\n") {
			line = truncateDisplay(line, contentWidth)
			lines = append(lines, renderSearchHighlightedText(line, messageSearchQuery, bodyStyle, active && containsSearchMatch(body, messageSearchQuery)))
		}
	}
	if meta != "" {
		meta = truncateDisplay(meta, contentWidth)
		lines = append(lines, lipgloss.NewStyle().Foreground(metaFG).Render(alignDisplay(meta, contentWidth, true)))
	}

	return boxStyle.Width(bubbleBoxWidth(boxStyle, contentWidth)).Render(strings.Join(lines, "\n"))
}

func (m Model) mediaPreview(message store.Message, item store.MediaMetadata, width int) (media.Preview, bool) {
	if len(m.previewCache) == 0 || width <= 0 {
		return media.Preview{}, false
	}
	request, ok := m.previewRequestForMedia(message, item, 0, 0)
	if !ok {
		return media.Preview{}, false
	}
	preview, ok := m.previewCache[media.PreviewKey(request)]
	if !ok || !preview.Ready() {
		return media.Preview{}, false
	}
	return preview, true
}

func (m Model) chatSearchQuery() string {
	if strings.TrimSpace(m.activeSearch) == "" || m.lastSearchFocus != FocusChats {
		return ""
	}
	return m.activeSearch
}

func (m Model) messageSearchQuery() string {
	if strings.TrimSpace(m.activeSearch) == "" {
		return ""
	}
	if m.lastSearchFocus != FocusMessages && m.lastSearchFocus != FocusPreview {
		return ""
	}
	return m.activeSearch
}

func overlayPreviewVisible(preview media.Preview, visibleOverlays map[string]bool) bool {
	if preview.Display != media.PreviewDisplayOverlay {
		return false
	}
	if visibleOverlays == nil {
		return true
	}
	return visibleOverlays[overlayIdentifier(preview.Key)]
}

func renderPreviewLines(preview media.Preview, width int, overlayVisible bool) []string {
	if preview.Display != media.PreviewDisplayOverlay {
		return preview.Lines
	}
	if !overlayVisible && len(preview.Lines) > 0 {
		lines := append([]string{}, preview.Lines...)
		height := max(1, preview.Height)
		switch {
		case len(lines) > height:
			return lines[:height]
		case len(lines) < height:
			for len(lines) < height {
				lines = append(lines, "")
			}
		}
		return lines
	}
	height := max(1, preview.Height)
	lineWidth := min(width, max(1, preview.Width))
	lines := make([]string, height)
	for i := range lines {
		lines[i] = strings.Repeat(" ", lineWidth)
	}
	return lines
}

func bubbleWidth(available int) int {
	if available < 28 {
		return max(10, available-2)
	}
	return min(max(22, available*46/100), available-2)
}

func mediaBubbleWidth(available int) int {
	if available < 28 {
		return max(10, available-2)
	}
	return min(max(30, available*83/100), available-2)
}

func (m Model) messageUsesMediaBubbleWidth(message store.Message, active bool) bool {
	for _, item := range message.Media {
		if media.MediaKind(item.MIMEType, item.FileName) == media.KindUnsupported {
			continue
		}
		if active || m.mediaPreviewShouldReserve(message, item) {
			return true
		}
	}
	return false
}

func (m Model) bubbleContentWidth(style lipgloss.Style, maxBubbleWidth int, body, meta, sender string, message store.Message, previewWidth int) int {
	maxContentWidth := max(1, panelContentWidth(style, maxBubbleWidth))
	widest := max(lipgloss.Width(meta), lipgloss.Width(sender))
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
	for _, media := range message.Media {
		label := attachmentLabel(media)
		if state := m.mediaAttachmentState(message, media); state != "" {
			label += " - " + state
		}
		widest = max(widest, lipgloss.Width(label))
		if previewWidth > 0 && m.mediaPreviewShouldReserve(message, media) {
			widest = max(widest, min(previewWidth, maxContentWidth))
		}
	}
	return clamp(max(widest, 4), min(4, maxContentWidth), maxContentWidth)
}

func (m Model) mediaPreviewShouldReserve(message store.Message, item store.MediaMetadata) bool {
	request, ok := m.previewRequestForMedia(message, item, 0, 0)
	if !ok {
		return false
	}
	key := media.PreviewKey(request)
	if m.previewInflight != nil && m.previewInflight[key] {
		return true
	}
	preview, ok := m.previewCache[key]
	return ok && preview.Ready()
}

func (m Model) mediaAttachmentState(message store.Message, item store.MediaMetadata) string {
	kind := media.MediaKind(item.MIMEType, item.FileName)
	if kind == media.KindAudio {
		return m.audioAttachmentState(message, item)
	}
	if m.mediaDownloadInflight != nil && m.mediaDownloadInflight[mediaDownloadKey(message, item)] {
		return "downloading"
	}
	if kind == media.KindUnsupported {
		return ""
	}
	request, ok := m.previewRequestForMedia(message, item, 0, 0)
	if !ok {
		return ""
	}
	key := media.PreviewKey(request)
	if m.previewInflight != nil && m.previewInflight[key] {
		return "rendering preview"
	}
	if preview, ok := m.previewCache[key]; ok {
		if preview.Err != nil {
			return "preview failed"
		}
		if preview.Ready() {
			return ""
		}
	}
	if highQualityPreviewRequiresLocalFile(m.previewReport.Selected, kind) && strings.TrimSpace(item.LocalPath) == "" {
		if strings.TrimSpace(item.ThumbnailPath) != "" {
			return "thumbnail only"
		}
		return "enter download"
	}
	if strings.TrimSpace(item.LocalPath) == "" && strings.TrimSpace(item.ThumbnailPath) == "" {
		return "enter download"
	}
	if m.previewRequested != nil && m.previewRequested[mediaActivationKey(message, item)] {
		return "preview pending"
	}
	return "enter preview"
}

func (m Model) audioAttachmentState(message store.Message, item store.MediaMetadata) string {
	key := mediaActivationKey(message, item)
	if m.audioMediaKey == key {
		if m.audioProcess != nil {
			return "playing; enter stop"
		}
		return "starting"
	}
	if m.mediaDownloadInflight != nil && m.mediaDownloadInflight[mediaDownloadKey(message, item)] {
		return "downloading"
	}
	if strings.TrimSpace(item.LocalPath) == "" {
		if m.downloadMedia != nil {
			return "enter download"
		}
		return ""
	}
	return "enter play"
}

func renderAttachmentLine(media store.MediaMetadata, width int, active bool, state string) string {
	label := attachmentLabel(media)
	if state != "" {
		label += " - " + state
	}
	style := lipgloss.NewStyle().Foreground(softFG)
	if active {
		style = style.Foreground(primaryFG)
	}
	return style.Render(truncateDisplay(label, width))
}

func attachmentLabel(media store.MediaMetadata) string {
	name := strings.TrimSpace(sanitizeDisplayLine(media.FileName))
	if name == "" {
		name = strings.TrimSpace(sanitizeDisplayLine(media.LocalPath))
	}
	if name == "" {
		name = "attachment"
	}

	kind := attachmentKind(media.MIMEType, name)
	size := humanSize(media.SizeBytes)
	state := strings.TrimSpace(sanitizeDisplayLine(media.DownloadState))
	var parts []string
	parts = append(parts, kind, name)
	if size != "" {
		parts = append(parts, size)
	}
	if state != "" && state != "downloaded" {
		parts = append(parts, state)
	}
	return strings.Join(parts, " ")
}

func attachmentKind(mimeType, name string) string {
	mimeType = strings.ToLower(strings.TrimSpace(mimeType))
	switch {
	case strings.HasPrefix(mimeType, "image/"):
		return "[img]"
	case strings.HasPrefix(mimeType, "video/"):
		return "[vid]"
	case strings.HasPrefix(mimeType, "audio/") || media.MediaKind(mimeType, name) == media.KindAudio:
		return "[aud]"
	case strings.Contains(mimeType, "pdf") || strings.HasSuffix(strings.ToLower(name), ".pdf"):
		return "[pdf]"
	default:
		return "[file]"
	}
}

func emptyLabel(value string) string {
	value = strings.TrimSpace(value)
	if value == "" {
		return "none"
	}
	return value
}

func humanSize(size int64) string {
	if size <= 0 {
		return ""
	}
	const unit = 1024
	if size < unit {
		return fmt.Sprintf("%dB", size)
	}
	value := float64(size)
	units := []string{"KB", "MB", "GB"}
	for _, suffix := range units {
		value /= unit
		if value < unit {
			return fmt.Sprintf("%.1f%s", value, suffix)
		}
	}
	return fmt.Sprintf("%.1fTB", value/unit)
}

func bubbleBoxWidth(style lipgloss.Style, contentWidth int) int {
	return max(1, contentWidth+style.GetHorizontalPadding())
}

func shouldShowMessageSender(chat store.Chat, message store.Message) bool {
	if message.IsOutgoing {
		return false
	}
	return chat.Kind == "group" || strings.HasSuffix(chat.JID, "@g.us") || strings.HasSuffix(message.ChatJID, "@g.us")
}

func messageBubbleMeta(message store.Message) string {
	var parts []string
	if !message.Timestamp.IsZero() {
		parts = append(parts, message.Timestamp.Format("15:04"))
	}
	if message.IsOutgoing {
		if ticks := messageStatusTicks(message.Status); ticks != "" {
			parts = append(parts, ticks)
		}
	}
	return strings.Join(parts, " ")
}

func messageStatusTicks(status string) string {
	switch strings.ToLower(strings.TrimSpace(status)) {
	case "":
		return ""
	case "pending", "queued", "sending":
		return "…"
	case "sent", "server_ack", "server ack", "ack":
		return "✓"
	case "delivered", "read":
		return "✓✓"
	default:
		return "✓"
	}
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

func (m Model) renderInfo(width int) string {
	chat := m.currentChat()
	chatTitle := sanitizeDisplayLine(chat.DisplayTitle())
	if chatTitle == "" {
		chatTitle = "none"
	}
	message := ""
	var mediaLines []string
	if messages := m.currentMessages(); len(messages) > 0 {
		focused := messages[clamp(m.messageCursor, 0, len(messages)-1)]
		message = sanitizeDisplayBody(focused.Body)
		if item, ok := firstMessageMedia(focused); ok {
			mediaLines = append(mediaLines,
				"media:",
				lipgloss.NewStyle().Foreground(softFG).Width(width).Render(fmt.Sprintf("file: %s", mediaDisplayName(item))),
				lipgloss.NewStyle().Foreground(softFG).Width(width).Render(fmt.Sprintf("mime: %s", emptyLabel(item.MIMEType))),
				lipgloss.NewStyle().Foreground(softFG).Width(width).Render(fmt.Sprintf("state: %s", emptyLabel(item.DownloadState))),
			)
			if state := m.mediaAttachmentState(focused, item); state != "" {
				mediaLines = append(mediaLines, lipgloss.NewStyle().Foreground(softFG).Width(width).Render(fmt.Sprintf("preview: %s", state)))
			}
			if item.LocalPath != "" {
				mediaLines = append(mediaLines, lipgloss.NewStyle().Foreground(softFG).Width(width).Render(fmt.Sprintf("local: %s", item.LocalPath)))
			}
			if item.ThumbnailPath != "" {
				mediaLines = append(mediaLines, lipgloss.NewStyle().Foreground(softFG).Width(width).Render(fmt.Sprintf("thumb: %s", item.ThumbnailPath)))
			}
			if request, ok := m.previewRequestForMedia(focused, item, 0, 0); ok {
				if preview, ok := m.previewCache[media.PreviewKey(request)]; ok {
					if preview.Err != nil {
						mediaLines = append(mediaLines, lipgloss.NewStyle().Foreground(warnFG).Width(width).Render(fmt.Sprintf("error: %s", shortError(preview.Err))))
					} else if preview.Ready() {
						mediaLines = append(mediaLines,
							lipgloss.NewStyle().Foreground(softFG).Width(width).Render(fmt.Sprintf("rendered: %s %s %dx%d", preview.RenderedBackend, preview.SourceKind, preview.Width, preview.Height)),
							lipgloss.NewStyle().Foreground(softFG).Width(width).Render(fmt.Sprintf("source: %s", preview.SourcePath)),
						)
						if placement, ok := m.activeMediaPlacement(); ok {
							mediaLines = append(mediaLines,
								lipgloss.NewStyle().Foreground(softFG).Width(width).Render(fmt.Sprintf("placement: x=%d y=%d max=%dx%d", placement.X, placement.Y, placement.MaxWidth, placement.MaxHeight)),
								lipgloss.NewStyle().Foreground(softFG).Width(width).Render(fmt.Sprintf("overlay json: %s", media.OverlayAddCommandJSON(placement))),
							)
						}
					}
				}
			}
			mediaLines = append(mediaLines, "")
		}
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
	}
	lines = append(lines, mediaLines...)
	lines = append(lines,
		"paths:",
		lipgloss.NewStyle().Foreground(softFG).Width(width).Render(m.paths.DatabaseFile),
	)

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
	if chat := m.currentChat(); chat.DisplayTitle() != "" {
		chatTitle = sanitizeDisplayLine(chat.DisplayTitle())
	}
	left := strings.Join([]string{
		statusSegment(" "+mode+" ", uiTheme.BarBG, modeStatusColor(m.mode), true),
		statusSegment(" "+focus+" ", primaryFG, borderColor, false),
		m.renderConnectionStatus(),
		statusSegment(" "+truncateDisplay(chatTitle, max(8, m.width/5))+" ", primaryFG, "", false),
	}, "")

	search := ""
	if m.activeSearch != "" {
		search = " /" + truncateDisplay(sanitizeDisplayLine(m.activeSearch), 16)
	}
	messageFilter := ""
	if m.messageFilter != "" {
		messageFilter = " filter:" + truncateDisplay(sanitizeDisplayLine(m.messageFilter), 16)
	}
	center := " " + truncateDisplay(sanitizeDisplayLine(m.status), max(8, m.width/3)) + " "
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

func (m Model) renderConnectionStatus() string {
	if m.connectionState == "" {
		return ""
	}
	label := strings.ToUpper(strings.ReplaceAll(string(m.connectionState), "_", " "))
	return statusSegment(" WA:"+label+" ", connectionStatusColor(m.connectionState), borderColor, false)
}

func statusSegment(text string, fg, bg lipgloss.Color, bold bool) string {
	style := lipgloss.NewStyle().Foreground(fg).Bold(bold)
	if bg != "" {
		style = style.Background(bg)
	}
	return style.Render(text)
}

func connectionStatusColor(state ConnectionState) lipgloss.Color {
	switch state {
	case ConnectionOnline:
		return accentFG
	case ConnectionConnecting, ConnectionReconnecting, ConnectionPaired:
		return warnFG
	case ConnectionOffline, ConnectionLoggedOut:
		return warnFG
	default:
		return softFG
	}
}

func modeStatusColor(mode Mode) lipgloss.Color {
	switch mode {
	case ModeInsert:
		return uiTheme.InsertModeBG
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
		return ""
	}
}

func (m Model) renderPrompt(label, content, hint string) string {
	if hint != "" {
		content = content + "  " + hint
	}
	prefix := "[" + label + "] "
	content = truncateDisplay(prefix+sanitizeDisplayText(content, false), max(1, m.width-1))
	style := lipgloss.NewStyle().Foreground(softFG).Width(m.width)
	if !barsTransparent() {
		style = style.Background(uiTheme.BarBG)
	}
	return style.Render(" " + content)
}

func (m Model) renderComposer(width int) string {
	lines := []string{renderFooterHelpLine(width)}

	attachmentLines := m.composerAttachmentLines(width)
	lines = append(lines, attachmentLines...)

	bodyLines := composerLines(m.composer)
	maxBodyLines := max(1, m.composerHeight()-1-len(attachmentLines))
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

func (m Model) composerAttachmentLines(width int) []string {
	if len(m.attachments) == 0 {
		return nil
	}

	maxLines := min(len(m.attachments), 2)
	start := len(m.attachments) - maxLines
	lines := make([]string, 0, maxLines+1)
	if start > 0 {
		lines = append(lines, lipgloss.NewStyle().Foreground(softFG).Render(truncateDisplay(fmt.Sprintf("+%d more attachment(s)", start), width)))
	}
	for _, attachment := range m.attachments[start:] {
		media := store.MediaMetadata{
			MIMEType:      attachment.MIMEType,
			FileName:      attachment.FileName,
			SizeBytes:     attachment.SizeBytes,
			LocalPath:     attachment.LocalPath,
			DownloadState: attachment.DownloadState,
		}
		lines = append(lines, renderAttachmentLine(media, width, true, ""))
	}
	return lines
}

func (m Model) renderMessageFooter(width int) string {
	if m.mode == ModeCommand || m.mode == ModeSearch {
		return ""
	}
	if m.mode == ModeInsert {
		return m.renderComposer(width)
	}

	draft := strings.TrimSuffix(m.draftsByChat[m.currentChat().ID], "\n")
	if draft == "" {
		draft = m.composer
	}
	draft = firstLine(draft)
	input := "> " + draft
	lines := []string{
		renderFooterHelpLine(width),
		truncateDisplay(input, width),
	}
	style := lipgloss.NewStyle().Foreground(softFG).Width(width)
	if !barsTransparent() {
		style = style.Background(uiTheme.BarBG)
	}
	return style.Render(strings.Join(lines, "\n"))
}

func renderFooterHelpLine(width int) string {
	width = max(1, width)
	hint := "? help"
	if width <= lipgloss.Width(hint) {
		return lipgloss.NewStyle().Foreground(accentFG).Render(truncateDisplay(hint, width))
	}

	fill := strings.Repeat("-", max(0, width-lipgloss.Width(hint)-1))
	return lipgloss.NewStyle().Foreground(borderColor).Render(fill+" ") +
		lipgloss.NewStyle().Foreground(accentFG).Render(hint)
}

func (m Model) footerChatTitle() string {
	chat := m.currentChat().DisplayTitle()
	if chat == "" {
		return "no chat"
	}
	return chat
}

func (m Model) inputHeight() int {
	switch m.mode {
	case ModeCommand, ModeSearch:
		return 1
	default:
		return 0
	}
}

func (m Model) composerHeight() int {
	if m.mode != ModeInsert {
		return 0
	}
	return min(7, max(3, len(composerLines(m.composer))+len(m.attachments)+2))
}

func (m Model) renderHelp(width int) string {
	lines := []string{
		lipgloss.NewStyle().Bold(true).Foreground(accentFG).Render("vimwhat help"),
		"",
		"normal:  j/k move    5j count    g/G top/bottom    h/l pane    tab cycle",
		"         enter preview/open  o open media  <leader>s save  <leader>hf unload previews",
		"         i insert    v visual    / search    : command    u unread    p sort",
		"         n/N next search   ? help      q quit",
		"insert:  enter send  ctrl+j newline  ctrl+f attach  ctrl+x remove attachment  esc save draft",
		"visual:  j/k extend  y yank clipboard  esc normal",
		"command: clear-search  filter unread/all  filter messages <text>  filter clear",
		"         sort pinned/recent  preview  media-preview  media-open  media-save  media-hide",
		"         history fetch  preview-backend <name>  clear-preview-cache",
		"         attach <path>  delete-message",
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
