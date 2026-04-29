package ui

import (
	"fmt"
	"strings"
	"time"
	"unicode"

	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/x/ansi"
	"github.com/rivo/uniseg"

	"vimwhat/internal/config"
	"vimwhat/internal/media"
	"vimwhat/internal/store"
	"vimwhat/internal/textmatch"
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
	if m.syncOverlay.Visible {
		return m.renderSyncOverlay(m.width, height)
	}
	if m.inlineFallbackPrompt {
		return m.renderInlineFallbackPrompt(m.width, height)
	}
	if m.mode == ModeForward {
		return m.renderForwardPicker(m.width, height)
	}
	if m.helpVisible {
		style := m.panelStyle(m.focus)
		return m.renderPanel(m.focus, m.width, height, m.renderHelp(panelContentWidth(style, m.width)))
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

func (m Model) renderSyncOverlay(width, height int) string {
	panelWidth := min(max(44, width*2/3), max(1, width-4))
	if width < 48 {
		panelWidth = max(1, width)
	}
	style := lipgloss.NewStyle().
		Border(lipgloss.RoundedBorder()).
		BorderForeground(activeBorder).
		Padding(1, 2).
		Width(max(1, panelWidth-2))

	contentWidth := max(1, panelWidth-style.GetHorizontalPadding()-style.GetHorizontalBorderSize())
	title := "Syncing WhatsApp updates"
	subtitle := "New chats and messages are being applied locally."
	if m.syncOverlay.Completed {
		title = "Sync complete"
		subtitle = "Latest WhatsApp data was applied."
	}

	lines := []string{
		lipgloss.NewStyle().Foreground(accentFG).Bold(true).Render(truncateDisplay(title, contentWidth)),
		lipgloss.NewStyle().Foreground(softFG).Render(wrapPlainText(subtitle, contentWidth)),
		"",
		m.renderSyncProgressBar(contentWidth),
		m.renderSyncProgressText(contentWidth),
	}
	if counts := m.renderSyncCounts(contentWidth); counts != "" {
		lines = append(lines, "", counts)
	}
	if m.syncOverlay.Active {
		lines = append(lines, "", lipgloss.NewStyle().Foreground(warnFG).Render(truncateDisplay("Input is paused while sync finishes.", contentWidth)))
	}

	panel := style.Render(strings.Join(lines, "\n"))
	return lipgloss.Place(
		width,
		height,
		lipgloss.Center,
		lipgloss.Center,
		panel,
		lipgloss.WithWhitespaceChars(" "),
	)
}

func (m Model) renderSyncProgressBar(width int) string {
	width = max(1, width)
	barWidth := min(40, max(10, width-2))
	processed := max(0, m.syncOverlay.Processed)
	total := max(0, m.syncOverlay.Total)
	filled := 0
	if total > 0 {
		filled = processed * barWidth / total
		if processed > 0 && filled == 0 {
			filled = 1
		}
		filled = clamp(filled, 0, barWidth)
	} else if m.syncOverlay.Active {
		filled = min(3, barWidth)
	}
	bar := "[" + strings.Repeat("#", filled) + strings.Repeat("-", max(0, barWidth-filled)) + "]"
	return lipgloss.NewStyle().Foreground(accentFG).Render(truncateDisplay(bar, width))
}

func (m Model) renderSyncProgressText(width int) string {
	processed := max(0, m.syncOverlay.Processed)
	total := max(0, m.syncOverlay.Total)
	if total > 0 {
		percent := processed * 100 / total
		return lipgloss.NewStyle().Foreground(primaryFG).Render(truncateDisplay(fmt.Sprintf("%d/%d events (%d%%)", processed, total, percent), width))
	}
	if processed > 0 {
		return lipgloss.NewStyle().Foreground(primaryFG).Render(truncateDisplay(fmt.Sprintf("%d events processed", processed), width))
	}
	return lipgloss.NewStyle().Foreground(primaryFG).Render(truncateDisplay("Waiting for sync details", width))
}

func (m Model) renderSyncCounts(width int) string {
	var parts []string
	if m.syncOverlay.Messages > 0 {
		parts = append(parts, fmt.Sprintf("%d messages", m.syncOverlay.Messages))
	}
	if m.syncOverlay.Receipts > 0 {
		parts = append(parts, fmt.Sprintf("%d receipts", m.syncOverlay.Receipts))
	}
	if m.syncOverlay.Notifications > 0 {
		parts = append(parts, fmt.Sprintf("%d notifications", m.syncOverlay.Notifications))
	}
	if m.syncOverlay.AppDataChanges > 0 {
		parts = append(parts, fmt.Sprintf("%d app updates", m.syncOverlay.AppDataChanges))
	}
	if len(parts) == 0 {
		return ""
	}
	return lipgloss.NewStyle().Foreground(softFG).Render(truncateDisplay(strings.Join(parts, " | "), width))
}

func (m Model) renderInlineFallbackPrompt(width, height int) string {
	panelWidth := min(max(48, width*2/3), max(1, width-4))
	if width < 52 {
		panelWidth = max(1, width)
	}
	style := lipgloss.NewStyle().
		Border(lipgloss.RoundedBorder()).
		BorderForeground(warnFG).
		Padding(1, 2).
		Width(max(1, panelWidth-2))

	contentWidth := max(1, panelWidth-style.GetHorizontalPadding()-style.GetHorizontalBorderSize())
	lines := []string{
		lipgloss.NewStyle().Foreground(warnFG).Bold(true).Render(truncateDisplay("Sixel image rendering unavailable", contentWidth)),
		lipgloss.NewStyle().Foreground(softFG).Render(wrapPlainText("For sharper inline images on Windows, run vimwhat in WezTerm or another Sixel-capable terminal. Chafa can render a lower-resolution fallback now.", contentWidth)),
		"",
		lipgloss.NewStyle().Foreground(accentFG).Render(truncateDisplay("Enter use Chafa fallback  Esc keep external opener", contentWidth)),
	}
	panel := style.Render(strings.Join(lines, "\n"))
	return lipgloss.Place(
		width,
		height,
		lipgloss.Center,
		lipgloss.Center,
		panel,
		lipgloss.WithWhitespaceChars(" "),
	)
}

func (m Model) renderForwardPicker(width, height int) string {
	panelWidth := min(max(52, width*2/3), max(1, width-4))
	if width < 56 {
		panelWidth = max(1, width)
	}
	style := lipgloss.NewStyle().
		Border(lipgloss.RoundedBorder()).
		BorderForeground(activeBorder).
		Padding(1, 2).
		Width(max(1, panelWidth-2))

	contentWidth := max(1, panelWidth-style.GetHorizontalPadding()-style.GetHorizontalBorderSize())
	title := fmt.Sprintf("Forward %d message(s)", len(m.forwardSourceMessages))
	query := strings.TrimSpace(m.forwardQuery)
	if m.forwardSearchActive {
		query = "/" + query
	} else if query == "" {
		query = "all chats"
	}
	lines := []string{
		lipgloss.NewStyle().Foreground(accentFG).Bold(true).Render(truncateDisplay(title, contentWidth)),
		lipgloss.NewStyle().Foreground(softFG).Render(truncateDisplay("to: "+query, contentWidth)),
		lipgloss.NewStyle().Foreground(softFG).Render(truncateDisplay(fmt.Sprintf("%d selected", len(m.forwardSelected)), contentWidth)),
		"",
	}
	rows := m.forwardPickerRows(contentWidth, max(1, height-8))
	if len(rows) == 0 {
		lines = append(lines, lipgloss.NewStyle().Foreground(softFG).Render(truncateDisplay("No matching chats", contentWidth)))
	} else {
		lines = append(lines, rows...)
	}
	panel := style.Render(strings.Join(lines, "\n"))
	return lipgloss.Place(
		width,
		height,
		lipgloss.Center,
		lipgloss.Center,
		panel,
		lipgloss.WithWhitespaceChars(" "),
	)
}

func (m Model) forwardPickerRows(width, height int) []string {
	height = max(1, height)
	if len(m.forwardCandidates) == 0 {
		return nil
	}
	cursor := clamp(m.forwardCursor, 0, len(m.forwardCandidates)-1)
	start := clamp(cursor-height/2, 0, max(0, len(m.forwardCandidates)-height))
	end := min(len(m.forwardCandidates), start+height)
	rows := make([]string, 0, end-start)
	for i := start; i < end; i++ {
		chat := m.forwardCandidates[i]
		marker := " "
		if m.forwardSelected[chat.ID] {
			marker = "x"
		}
		prefix := " "
		style := lipgloss.NewStyle().Foreground(primaryFG)
		if i == cursor {
			prefix = ">"
			style = style.Foreground(accentFG).Bold(true)
		}
		title := m.sanitizeDisplayLine(chat.DisplayTitle())
		if title == "" {
			title = chat.ID
		}
		row := fmt.Sprintf("%s [%s] %s", prefix, marker, title)
		rows = append(rows, style.Render(truncateDisplay(row, width)))
	}
	return rows
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
			for displayWidth(word) > width {
				prefix, rest := splitDisplayWidth(word, width)
				if prefix == "" {
					break
				}
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
			if displayWidth(line)+1+displayWidth(word) <= width {
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
	graphemes := uniseg.NewGraphemes(value)
	for graphemes.Next() {
		cluster := graphemes.Str()
		clusterWidth := displayWidth(cluster)
		if cut > 0 && current+clusterWidth > width {
			break
		}
		current += clusterWidth
		cut += len(cluster)
		if current >= width {
			break
		}
	}
	if cut == 0 {
		graphemes := uniseg.NewGraphemes(value)
		if graphemes.Next() {
			cut = len(graphemes.Str())
		}
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
	scrollTop = adjustedMessageScrollTop(blocks, scrollTop, selected, height)
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
				used += end
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
		end := min(blockHeight, height)
		spans = append(spans, messageBlockSpan{index: selected, start: 0, end: end})
		used = end
	}
	for i := spans[0].index - 1; i >= 0 && used < height; i-- {
		blockHeight := len(blocks[i].lines)
		if blockHeight == 0 {
			continue
		}
		remaining := height - used
		if blockHeight > remaining {
			spans = append([]messageBlockSpan{{
				index: i,
				start: blockHeight - remaining,
				end:   blockHeight,
			}}, spans...)
			break
		}
		spans = append([]messageBlockSpan{{index: i, start: 0, end: blockHeight}}, spans...)
		used += blockHeight
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

func messageBlockSpansContain(spans []messageBlockSpan, index int) bool {
	for _, span := range spans {
		if span.index == index && span.start < span.end {
			return true
		}
	}
	return false
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

func displayWidth(value string) int {
	return ansi.StringWidth(value)
}

func alignDisplay(value string, width int, right bool) string {
	current := displayWidth(value)
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
	if displayWidth(value) <= width {
		return value
	}
	if width <= 1 {
		return ""
	}
	return ansi.Truncate(value, width, "~")
}

func (m Model) resolvedEmojiMode() string {
	return config.ResolveEmojiMode(m.config.EmojiMode)
}

func (m Model) sanitizeDisplayLine(value string) string {
	return sanitizeDisplayLineForMode(value, m.resolvedEmojiMode())
}

func (m Model) sanitizeDisplayBody(value string) string {
	return sanitizeDisplayTextForMode(value, true, m.resolvedEmojiMode())
}

func (m Model) sanitizeDisplayText(value string, allowNewlines bool) string {
	return sanitizeDisplayTextForMode(value, allowNewlines, m.resolvedEmojiMode())
}

func sanitizeDisplayLine(value string) string {
	return sanitizeDisplayLineForMode(value, config.ResolveEmojiMode(config.EmojiModeAuto))
}

func sanitizeDisplayLineForMode(value, emojiMode string) string {
	value = sanitizeDisplayTextForMode(value, false, emojiMode)
	return strings.Join(strings.Fields(value), " ")
}

func sanitizeDisplayBody(value string) string {
	return sanitizeDisplayTextForMode(value, true, config.ResolveEmojiMode(config.EmojiModeAuto))
}

func sanitizeDisplayText(value string, allowNewlines bool) string {
	return sanitizeDisplayTextForMode(value, allowNewlines, config.ResolveEmojiMode(config.EmojiModeAuto))
}

func sanitizeDisplayTextForMode(value string, allowNewlines bool, emojiMode string) string {
	if value == "" {
		return ""
	}
	compatEmoji := config.ResolveEmojiMode(emojiMode) == config.EmojiModeCompat
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
			if unicode.IsControl(r) ||
				(compatEmoji && terminalUnsafeZeroWidthRune(r)) ||
				(!compatEmoji && terminalUnsafeFormatRune(r)) {
				continue
			}
			out.WriteRune(r)
		}
	}
	return out.String()
}

func terminalUnsafeFormatRune(r rune) bool {
	if !unicode.Is(unicode.Cf, r) {
		return false
	}
	if r == '\u200d' {
		return false
	}
	if r >= 0xE0020 && r <= 0xE007F {
		return false
	}
	return true
}

func terminalUnsafeZeroWidthRune(r rune) bool {
	if unicode.Is(unicode.Cf, r) {
		return true
	}
	if r >= 0xFE00 && r <= 0xFE0F {
		return true
	}
	if r >= 0xE0100 && r <= 0xE01EF {
		return true
	}
	if r >= 0x1F3FB && r <= 0x1F3FF {
		return true
	}
	return false
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

	spans := textmatch.FindAll(value, query)
	if len(spans) == 0 {
		return []searchSegment{{text: value}}
	}

	var segments []searchSegment
	start := 0
	for _, span := range spans {
		if span.Start < start || span.End <= span.Start || span.End > len(value) {
			continue
		}
		if start < span.Start {
			segments = append(segments, searchSegment{text: value[start:span.Start]})
		}
		segments = append(segments, searchSegment{text: value[span.Start:span.End], match: true})
		start = span.End
	}
	if start < len(value) {
		segments = append(segments, searchSegment{text: value[start:]})
	}
	if len(segments) == 0 {
		return []searchSegment{{text: value}}
	}
	return segments
}

func graphemeClusters(value string) []string {
	if value == "" {
		return nil
	}
	graphemes := uniseg.NewGraphemes(value)
	clusters := make([]string, 0, len(value))
	for graphemes.Next() {
		clusters = append(clusters, graphemes.Str())
	}
	return clusters
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
		headerText += fmt.Sprintf(" /%s", truncateDisplay(m.sanitizeDisplayLine(m.activeSearch), max(4, width-8)))
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
	borderShape := lipgloss.NormalBorder()
	if active {
		border = focusedLine
		previewFG = accentFG
		borderShape = lipgloss.ThickBorder()
	}

	style := lipgloss.NewStyle().
		Border(borderShape).
		Padding(0, 1)
	style = highlightedItemBorderStyle(style, border, active, false)
	contentWidth := panelContentWidth(style, width)

	title := m.sanitizeDisplayLine(chat.DisplayTitle())
	if title == "" {
		title = "unknown"
	}
	avatarLines := m.renderChatAvatarLines(chat, title, border)
	avatarWidth := displayWidth(avatarLines[0])
	textWidth := max(1, contentWidth-avatarWidth-1)
	chatSearchQuery := m.chatSearchQuery()
	titleLine := renderChatTitleLine(title, chatFlagSuffix(chat), unreadBadgeText(chat.Unread), textWidth, chatSearchQuery, active)
	previewLine := truncateDisplay(m.chatPreview(chat), textWidth)

	previewStyle := lipgloss.NewStyle().Foreground(previewFG)
	content := strings.Join([]string{
		avatarLines[0] + " " + titleLine,
		avatarLines[1] + " " + previewStyle.Render(previewLine),
	}, "\n")

	return style.Width(panelBoxWidth(style, width)).Render(content)
}

func (m Model) renderChatAvatarLines(chat store.Chat, title string, border lipgloss.Color) []string {
	if preview, ok := m.chatAvatarPreview(chat); ok {
		if preview.Display == media.PreviewDisplayOverlay && m.chatAvatarOverlayVisible(preview) {
			return blankChatAvatarLines(max(chatAvatarPreviewWidth, preview.Width))
		}
		if len(preview.Lines) > 0 {
			return normalizeChatAvatarLines(preview.Lines, max(chatAvatarPreviewWidth, preview.Width))
		}
	}

	badge := chatAvatarBadge(title, chat.Kind)
	width := lipgloss.Width(badge)
	style := lipgloss.NewStyle().Foreground(border)
	return []string{
		style.Render(badge),
		style.Render(strings.Repeat(" ", width)),
	}
}

func normalizeChatAvatarLines(source []string, width int) []string {
	lines := append([]string{}, source...)
	if len(lines) > chatAvatarPreviewHeight {
		lines = lines[:chatAvatarPreviewHeight]
	}
	for _, line := range lines {
		width = max(width, displayWidth(line))
	}
	if width <= 0 {
		width = chatAvatarPreviewWidth
	}
	for len(lines) < chatAvatarPreviewHeight {
		lines = append(lines, "")
	}
	for i := range lines {
		lines[i] = padDisplay(lines[i], width)
	}
	return lines
}

func blankChatAvatarLines(width int) []string {
	width = max(1, width)
	lines := make([]string, chatAvatarPreviewHeight)
	for i := range lines {
		lines[i] = strings.Repeat(" ", width)
	}
	return lines
}

func (m Model) chatAvatarOverlayVisible(preview media.Preview) bool {
	if preview.Display != media.PreviewDisplayOverlay {
		return false
	}
	identifier := overlayIdentifier(preview.Key)
	if strings.TrimSpace(m.overlaySignature) != "" && overlaySignatureContainsIdentifier(m.overlaySignature, identifier) {
		return true
	}
	if !m.avatarOverlayPaused {
		return false
	}
	for _, placement := range m.visibleChatAvatarPlacements() {
		if placement.Identifier == identifier {
			return true
		}
	}
	return false
}

func renderChatTitleLine(title, flagSuffix, unreadBadge string, width int, query string, current bool) string {
	width = max(1, width)
	titleStyle := lipgloss.NewStyle().Foreground(softFG)
	if current {
		titleStyle = titleStyle.Foreground(primaryFG).Bold(true)
	}
	if flagSuffix == "" && unreadBadge == "" {
		title = truncateDisplay(title, width)
		return renderSearchHighlightedText(title, query, titleStyle, current && containsSearchMatch(title, query))
	}

	right := renderChatTitleSuffix(flagSuffix, unreadBadge, titleStyle)
	rightWidth := displayWidth(right)
	if rightWidth >= width {
		if unreadBadge != "" {
			return renderUnreadBadgeCompact(unreadBadge, width)
		}
		return titleStyle.Render(truncateDisplay(flagSuffix, width))
	}

	title = truncateDisplay(title, max(1, width-rightWidth-1))
	gap := max(1, width-lipgloss.Width(title)-rightWidth)
	highlightedTitle := renderSearchHighlightedText(title, query, titleStyle, current && containsSearchMatch(title, query))
	return highlightedTitle + strings.Repeat(" ", gap) + right
}

func renderChatTitleSuffix(flagSuffix, unreadBadge string, titleStyle lipgloss.Style) string {
	flagSuffix = strings.TrimSpace(flagSuffix)
	unreadBadge = strings.TrimSpace(unreadBadge)
	switch {
	case flagSuffix == "" && unreadBadge == "":
		return ""
	case flagSuffix == "":
		return renderUnreadBadge(unreadBadge)
	case unreadBadge == "":
		return titleStyle.Render(flagSuffix)
	default:
		return titleStyle.Render(flagSuffix) + " " + renderUnreadBadge(unreadBadge)
	}
}

func renderUnreadBadge(label string) string {
	label = strings.TrimSpace(label)
	if label == "" {
		return ""
	}
	return unreadBadgeStyle().Render(" " + label + " ")
}

func renderUnreadBadgeCompact(label string, width int) string {
	label = truncateDisplay(strings.TrimSpace(label), max(1, width))
	if label == "" {
		return ""
	}
	return unreadBadgeStyle().Render(label)
}

func unreadBadgeStyle() lipgloss.Style {
	return lipgloss.NewStyle().
		Foreground(uiTheme.BarBG).
		Background(accentFG).
		Bold(true)
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

func (m Model) chatAvatarPreview(chat store.Chat) (media.Preview, bool) {
	request, ok := m.chatAvatarPreviewRequest(chat)
	if !ok {
		return media.Preview{}, false
	}
	preview, ok := m.previewCache[media.PreviewKey(request)]
	if !ok || !preview.Ready() {
		return media.Preview{}, false
	}
	return preview, true
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
	if presence := m.chatPresenceText(chat.ID); presence != "" {
		return presence
	}
	preview := strings.TrimSpace(m.sanitizeDisplayBody(chat.LastPreview))
	if chat.HasDraft {
		if draft := strings.TrimSpace(m.sanitizeDisplayBody(m.draftsByChat[chat.ID])); draft != "" {
			preview = "draft: " + firstLine(draft)
		}
	}
	if preview == "" {
		return "no local messages"
	}
	return m.sanitizeDisplayLine(firstLine(preview))
}

func chatFlagSuffix(chat store.Chat) string {
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

	if len(flags) > 0 {
		return "[" + strings.Join(flags, "") + "]"
	}
	return ""
}

func unreadBadgeText(unread int) string {
	if unread <= 0 {
		return ""
	}
	if unread > 99 {
		return "99+"
	}
	return fmt.Sprintf("%d", unread)
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
	title := m.sanitizeDisplayLine(chat.DisplayTitle())
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

	blocks := m.messageBlocksForRange(messages, width, start, end, m.visibleOverlayIdentifiers())

	body := make([]string, 0, bodyHeight)
	if len(blocks) == 0 {
		body = append(body, lipgloss.NewStyle().Foreground(softFG).Render("No messages in current chat."))
	} else {
		localCursor := clamp(m.messageCursor-start, 0, len(blocks)-1)
		localScrollTop := clamp(m.messageScrollTop-start, 0, len(blocks)-1)
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

func (m Model) messageBlocksForRange(messages []store.Message, width, start, end int, visibleOverlays map[string]bool) []messageBlock {
	blocks := make([]messageBlock, 0, end-start)
	var lastDate string
	if start > 0 && start <= len(messages) {
		lastDate = messageDate(messages[start-1])
	}
	for i := start; i < end && i < len(messages); i++ {
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
	return blocks
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
	parts := []string{m.sanitizeDisplayLine(title)}
	if m.unreadOnly {
		parts = append(parts, "unread")
	}
	if m.activeSearch != "" {
		parts = append(parts, "/"+m.sanitizeDisplayLine(m.activeSearch))
	}
	if m.messageFilter != "" {
		parts = append(parts, "filter:"+m.sanitizeDisplayLine(m.messageFilter))
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

	body := strings.TrimSpace(m.sanitizeDisplayBody(message.Body))
	if body == "" && len(message.Media) == 0 {
		body = "(empty)"
	}
	meta := messageBubbleMeta(message)
	sender := ""
	if shouldShowMessageSender(m.currentChat(), message) {
		sender = m.sanitizeDisplayLine(message.Sender)
		if sender == "" {
			sender = "unknown"
		}
	}

	boxStyle := lipgloss.NewStyle().
		Border(messageBubbleBorder(active, selected)).
		Padding(0, 1)
	boxStyle = highlightedItemBorderStyle(boxStyle, border, active, selected)
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
	if quote := m.messageQuoteLine(message, contentWidth); quote != "" {
		lines = append(lines, lipgloss.NewStyle().Foreground(metaFG).Italic(true).Render(quote))
	}
	for _, item := range message.Media {
		if preview, ok := m.mediaPreview(message, item, contentWidth); ok {
			lines = append(lines, renderPreviewLines(preview, contentWidth, overlayPreviewVisible(preview, visibleOverlays))...)
		} else {
			state := m.mediaAttachmentState(message, item)
			lines = append(lines, m.renderAttachmentLine(item, contentWidth, active || selected, state))
		}
	}
	if body != "" {
		for _, line := range strings.Split(wrapPlainText(body, contentWidth), "\n") {
			line = truncateDisplay(line, contentWidth)
			lines = append(lines, renderSearchHighlightedText(line, messageSearchQuery, bodyStyle, active && containsSearchMatch(body, messageSearchQuery)))
		}
	}
	if reactions := m.messageReactionLine(message, contentWidth); reactions != "" {
		lines = append(lines, lipgloss.NewStyle().Foreground(metaFG).Render(reactions))
	}
	if meta != "" {
		meta = truncateDisplay(meta, contentWidth)
		lines = append(lines, lipgloss.NewStyle().Foreground(metaFG).Render(alignDisplay(meta, contentWidth, true)))
	}

	return boxStyle.Width(bubbleBoxWidth(boxStyle, contentWidth)).Render(strings.Join(lines, "\n"))
}

func messageBubbleBorder(active, selected bool) lipgloss.Border {
	if active || selected {
		return lipgloss.ThickBorder()
	}
	return lipgloss.RoundedBorder()
}

func highlightedItemBorderStyle(style lipgloss.Style, border lipgloss.Color, active, selected bool) lipgloss.Style {
	style = style.BorderForeground(border)
	if active {
		return style.
			BorderTopForeground(focusedLine).
			BorderLeftForeground(focusedLine).
			BorderRightForeground(activeBorder).
			BorderBottomForeground(activeBorder)
	}
	if selected {
		return style.
			BorderTopForeground(selectedLine).
			BorderLeftForeground(selectedLine).
			BorderRightForeground(borderColor).
			BorderBottomForeground(borderColor)
	}
	return style
}

func (m Model) messageQuoteLine(message store.Message, width int) string {
	quotedID := strings.TrimSpace(message.QuotedRemoteID)
	if quotedID == "" {
		return ""
	}
	label := "reply " + quotedID
	if quoted := m.messageByID(message.QuotedMessageID); quoted.ID != "" {
		sender := strings.TrimSpace(m.sanitizeDisplayLine(quoted.Sender))
		if sender == "" {
			sender = "message"
		}
		if body := strings.TrimSpace(m.sanitizeDisplayLine(firstLine(quoted.Body))); body != "" {
			label = "reply " + sender + ": " + body
		} else if summary := strings.TrimSpace(m.quotedMessageSummary(quoted)); summary != "" {
			label = "reply " + sender + ": " + summary
		} else {
			label = "reply " + sender
		}
	}
	return truncateDisplay(label, width)
}

func (m Model) quotedMessageSummary(message store.Message) string {
	if len(message.Media) == 0 {
		return ""
	}
	item := message.Media[0]
	if strings.EqualFold(strings.TrimSpace(item.Kind), "sticker") {
		if label := strings.TrimSpace(m.sanitizeDisplayLine(item.AccessibilityLabel)); label != "" {
			return "Sticker: " + label
		}
		return "Sticker"
	}
	return m.attachmentLabel(item)
}

func (m Model) messageByID(messageID string) store.Message {
	if strings.TrimSpace(messageID) == "" {
		return store.Message{}
	}
	for _, message := range m.currentMessages() {
		if message.ID == messageID {
			return message
		}
	}
	return store.Message{}
}

func (m Model) messageReactionLine(message store.Message, width int) string {
	if len(message.Reactions) == 0 {
		return ""
	}
	counts := map[string]int{}
	order := make([]string, 0, len(message.Reactions))
	for _, reaction := range message.Reactions {
		emoji := strings.TrimSpace(m.sanitizeDisplayLine(reaction.Emoji))
		if emoji == "" {
			continue
		}
		if counts[emoji] == 0 {
			order = append(order, emoji)
		}
		counts[emoji]++
	}
	if len(order) == 0 {
		return ""
	}
	if len(order) > 3 {
		order = order[:3]
	}
	parts := make([]string, 0, len(order))
	for _, emoji := range order {
		if counts[emoji] > 1 {
			parts = append(parts, fmt.Sprintf("%s x%d", emoji, counts[emoji]))
		} else {
			parts = append(parts, emoji)
		}
	}
	return truncateDisplay(strings.Join(parts, "  "), width)
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
	if quote := m.messageQuoteLine(message, maxContentWidth); quote != "" {
		widest = max(widest, lipgloss.Width(quote))
	}
	if reactions := m.messageReactionLine(message, maxContentWidth); reactions != "" {
		widest = max(widest, lipgloss.Width(reactions))
	}
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
		label := m.attachmentLabel(media)
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
	item, _ = normalizeManagedMediaMetadata(m.paths, item)
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
	inlineBackend, hasInlineBackend := m.inlinePreviewBackend()
	if !hasInlineBackend && m.previewReport.Selected == media.BackendExternal {
		if strings.TrimSpace(item.LocalPath) != "" {
			return displayBinding(m.config.Keymap.NormalOpen, m.config.LeaderKey) + " open"
		}
		return displayBinding(m.config.Keymap.NormalOpen, m.config.LeaderKey) + " download"
	}
	if highQualityPreviewRequiresLocalFile(inlineBackend, kind) && strings.TrimSpace(item.LocalPath) == "" {
		if strings.TrimSpace(item.ThumbnailPath) != "" {
			return "thumbnail only"
		}
		return displayBinding(m.config.Keymap.NormalOpen, m.config.LeaderKey) + " download"
	}
	if strings.TrimSpace(item.LocalPath) == "" && strings.TrimSpace(item.ThumbnailPath) == "" {
		return displayBinding(m.config.Keymap.NormalOpen, m.config.LeaderKey) + " download"
	}
	if m.previewRequested != nil && m.previewRequested[mediaActivationKey(message, item)] {
		return "preview pending"
	}
	return displayBinding(m.config.Keymap.NormalOpen, m.config.LeaderKey) + " preview"
}

func (m Model) audioAttachmentState(message store.Message, item store.MediaMetadata) string {
	item, _ = normalizeManagedMediaMetadata(m.paths, item)
	key := mediaActivationKey(message, item)
	if m.audioMediaKey == key {
		if m.audioProcess != nil {
			return "playing; " + displayBinding(m.config.Keymap.NormalOpen, m.config.LeaderKey) + " stop"
		}
		return "starting"
	}
	if m.mediaDownloadInflight != nil && m.mediaDownloadInflight[mediaDownloadKey(message, item)] {
		return "downloading"
	}
	if strings.TrimSpace(item.LocalPath) == "" {
		if m.downloadMedia != nil {
			return displayBinding(m.config.Keymap.NormalOpen, m.config.LeaderKey) + " download"
		}
		return ""
	}
	return displayBinding(m.config.Keymap.NormalOpen, m.config.LeaderKey) + " play"
}

func (m Model) renderAttachmentLine(media store.MediaMetadata, width int, active bool, state string) string {
	label := m.attachmentLabel(media)
	if state != "" {
		label += " - " + state
	}
	style := lipgloss.NewStyle().Foreground(softFG)
	if active {
		style = style.Foreground(primaryFG)
	}
	return style.Render(truncateDisplay(label, width))
}

func (m Model) attachmentLabel(media store.MediaMetadata) string {
	name := m.attachmentName(media)
	kind := attachmentKind(media)
	size := humanSize(media.SizeBytes)
	state := strings.TrimSpace(m.sanitizeDisplayLine(media.DownloadState))
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

func (m Model) attachmentName(media store.MediaMetadata) string {
	if strings.EqualFold(strings.TrimSpace(media.Kind), "sticker") {
		if label := strings.TrimSpace(m.sanitizeDisplayLine(media.AccessibilityLabel)); label != "" {
			return label
		}
		return "sticker"
	}
	name := strings.TrimSpace(m.sanitizeDisplayLine(media.FileName))
	if name == "" {
		name = strings.TrimSpace(m.sanitizeDisplayLine(media.LocalPath))
	}
	if name == "" {
		name = "attachment"
	}
	return name
}

func attachmentKind(item store.MediaMetadata) string {
	if strings.EqualFold(strings.TrimSpace(item.Kind), "sticker") {
		return "[stk]"
	}
	mimeType := item.MIMEType
	name := item.FileName
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
	if !message.EditedAt.IsZero() {
		parts = append(parts, "edited")
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
	case "failed", "error":
		return "!"
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
		indent := max(0, width-displayWidth(line)-3)
		lines[i] = truncateDisplay(strings.Repeat(" ", indent)+line, width)
	}
	return strings.Join(lines, "\n")
}

func (m Model) renderInfo(width int) string {
	chat := m.currentChat()
	chatTitle := m.sanitizeDisplayLine(chat.DisplayTitle())
	if chatTitle == "" {
		chatTitle = "none"
	}
	message := ""
	var mediaLines []string
	if messages := m.currentMessages(); len(messages) > 0 {
		focused := messages[clamp(m.messageCursor, 0, len(messages)-1)]
		message = m.sanitizeDisplayBody(focused.Body)
		if item, ok := firstMessageMedia(focused); ok {
			item, _ = normalizeManagedMediaMetadata(m.paths, item)
			mediaLines = append(mediaLines,
				"media:",
				lipgloss.NewStyle().Foreground(softFG).Width(width).Render(fmt.Sprintf("file: %s", m.mediaDisplayName(item))),
				lipgloss.NewStyle().Foreground(softFG).Width(width).Render(fmt.Sprintf("mime: %s", emptyLabel(item.MIMEType))),
				lipgloss.NewStyle().Foreground(softFG).Width(width).Render(fmt.Sprintf("state: %s", emptyLabel(item.DownloadState))),
			)
			if state := m.mediaAttachmentState(focused, item); state != "" {
				mediaLines = append(mediaLines, lipgloss.NewStyle().Foreground(softFG).Width(width).Render(fmt.Sprintf("preview: %s", state)))
			}
			if item.LocalPath != "" {
				mediaLines = append(mediaLines, lipgloss.NewStyle().Foreground(softFG).Width(width).Render(fmt.Sprintf("local: %s", m.sanitizeDisplayLine(item.LocalPath))))
			}
			if item.ThumbnailPath != "" {
				mediaLines = append(mediaLines, lipgloss.NewStyle().Foreground(softFG).Width(width).Render(fmt.Sprintf("thumb: %s", m.sanitizeDisplayLine(item.ThumbnailPath))))
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
		chatTitle = m.sanitizeDisplayLine(chat.DisplayTitle())
	}
	left := strings.Join([]string{
		statusSegment(" "+mode+" ", uiTheme.BarBG, m.modeStatusColor(m.mode), true),
		statusSegment(" "+focus+" ", primaryFG, borderColor, false),
		m.renderConnectionStatus(),
		statusSegment(" "+truncateDisplay(chatTitle, max(8, m.width/5))+" ", primaryFG, "", false),
	}, "")

	search := ""
	if m.activeSearch != "" {
		search = " " + m.searchStatusSegment(16)
	}
	messageFilter := ""
	if m.messageFilter != "" {
		messageFilter = " filter:" + truncateDisplay(m.sanitizeDisplayLine(m.messageFilter), 16)
	}
	centerStatus := m.sanitizeDisplayLine(m.status)
	if presence := m.currentPresenceText(); presence != "" {
		if centerStatus != "" && centerStatus != "ready" {
			centerStatus += " | " + presence
		} else {
			centerStatus = presence
		}
	}
	center := " " + truncateDisplay(centerStatus, max(8, m.width/3)) + " "
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
		return truncateDisplay(" "+mode+" "+focus+" "+m.sanitizeDisplayLine(m.status)+" "+rightText+rightCount, m.width)
	}
	spacer := spacerStyle.Render(strings.Repeat(" ", max(0, m.width-used)))
	return left + center + spacer + right
}

func (m Model) currentPresenceText() string {
	return m.chatPresenceText(m.currentChat().ID)
}

func (m Model) chatPresenceText(chatID string) string {
	if chatID == "" || len(m.presenceByChat) == 0 {
		return ""
	}
	presence, ok := m.presenceByChat[chatID]
	if !ok || !presence.Typing || (!presence.ExpiresAt.IsZero() && time.Now().After(presence.ExpiresAt)) {
		return ""
	}
	name := strings.TrimSpace(m.sanitizeDisplayLine(presence.Sender))
	if name == "" {
		name = strings.TrimSpace(m.sanitizeDisplayLine(presence.SenderJID))
	}
	if name == "" {
		name = "someone"
	}
	return name + " typing"
}

func (m Model) searchStatusSegment(queryWidth int) string {
	query := m.sanitizeDisplayLine(m.activeSearch)
	if query == "" {
		return ""
	}
	total := len(m.searchMatches)
	if total == 0 {
		return "/" + truncateDisplay(query, queryWidth) + " 0/0"
	}
	current := m.searchIndex
	if current < 0 || current >= total {
		target := -1
		switch m.lastSearchFocus {
		case FocusChats:
			target = m.activeChat
		case FocusMessages, FocusPreview:
			target = m.messageCursor
		}
		for i, match := range m.searchMatches {
			if match == target {
				current = i
				break
			}
		}
	}
	if current < 0 || current >= total {
		current = 0
	}
	return fmt.Sprintf("/%s %d/%d", truncateDisplay(query, queryWidth), current+1, total)
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

func (m Model) modeStatusColor(mode Mode) lipgloss.Color {
	value := strings.TrimSpace(modeIndicatorConfigValue(m.config, mode))
	if value == "" || strings.EqualFold(value, config.IndicatorPywal) {
		return defaultModeStatusColor(mode)
	}
	return lipgloss.Color(value)
}

func modeIndicatorConfigValue(cfg config.Config, mode Mode) string {
	switch mode {
	case ModeInsert:
		return cfg.IndicatorInsert
	case ModeVisual:
		return cfg.IndicatorVisual
	case ModeCommand:
		return cfg.IndicatorCommand
	case ModeSearch:
		return cfg.IndicatorSearch
	default:
		return cfg.IndicatorNormal
	}
}

func defaultModeStatusColor(mode Mode) lipgloss.Color {
	switch mode {
	case ModeInsert:
		return uiTheme.InsertModeBG
	case ModeVisual:
		return selectedLine
	case ModeCommand:
		return activeBorder
	case ModeSearch:
		return warnFG
	case ModeForward:
		return activeBorder
	case ModeConfirm:
		return warnFG
	default:
		return accentFG
	}
}

func (m Model) renderInput() string {
	keys := m.config.Keymap
	leader := m.config.LeaderKey
	switch m.mode {
	case ModeCommand:
		hint := fmt.Sprintf("%s run  %s cancel", displayBinding(keys.CommandRun, leader), displayBinding(keys.CommandCancel, leader))
		return m.renderPrompt(":"+m.commandLine, hint)
	case ModeSearch:
		hint := fmt.Sprintf("%s search  %s cancel  empty clears", displayBinding(keys.SearchRun, leader), displayBinding(keys.SearchCancel, leader))
		return m.renderPrompt("/"+m.searchLine, hint)
	case ModeConfirm:
		hint := fmt.Sprintf("%s confirm  %s cancel", displayBinding(keys.ConfirmRun, leader), displayBinding(keys.ConfirmCancel, leader))
		return m.renderPrompt("delete for everybody? type Y: "+m.confirmLine, hint)
	default:
		return ""
	}
}

func (m Model) renderPrompt(content, hint string) string {
	if hint != "" {
		content = content + "  " + hint
	}
	content = truncateDisplay(m.sanitizeDisplayText(content, false), max(1, m.width-1))
	style := lipgloss.NewStyle().Foreground(softFG).Width(m.width)
	if !barsTransparent() {
		style = style.Background(uiTheme.BarBG)
	}
	return style.Render(" " + content)
}

func (m Model) renderComposer(width int) string {
	lines := []string{m.renderFooterHelpLine(width)}

	if edit := m.editPreviewLine(width); edit != "" {
		lines = append(lines, lipgloss.NewStyle().Foreground(softFG).Italic(true).Render(edit))
	}
	if quote := m.replyPreviewLine(width); quote != "" {
		lines = append(lines, lipgloss.NewStyle().Foreground(softFG).Italic(true).Render(quote))
	}
	attachmentLines := m.composerAttachmentLines(width)
	lines = append(lines, attachmentLines...)
	lines = append(lines, m.mentionSuggestionLines(width)...)

	bodyLines := composerLines(m.composer)
	maxBodyLines := max(1, m.composerHeight()-1-len(attachmentLines)-m.mentionSuggestionHeight())
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

func (m Model) mentionSuggestionLines(width int) []string {
	if !m.mentionActive {
		return nil
	}
	width = max(1, width)
	query := strings.TrimSpace(m.mentionQuery)
	if query == "" {
		query = "@"
	} else {
		query = "@" + query
	}
	lines := []string{lipgloss.NewStyle().Foreground(softFG).Render(truncateDisplay("mention "+query, width))}
	if len(m.mentionCandidates) == 0 {
		return append(lines, lipgloss.NewStyle().Foreground(softFG).Render(truncateDisplay("  no matches", width)))
	}
	limit := min(5, len(m.mentionCandidates))
	cursor := clamp(m.mentionCursor, 0, len(m.mentionCandidates)-1)
	start := clamp(cursor-limit/2, 0, max(0, len(m.mentionCandidates)-limit))
	end := min(len(m.mentionCandidates), start+limit)
	for i := start; i < end; i++ {
		candidate := m.mentionCandidates[i]
		prefix := " "
		style := lipgloss.NewStyle().Foreground(primaryFG)
		if i == cursor {
			prefix = ">"
			style = style.Foreground(accentFG).Bold(true)
		}
		label := mentionDisplayName(candidate)
		lines = append(lines, style.Render(truncateDisplay(fmt.Sprintf("%s @%s", prefix, m.sanitizeDisplayLine(label)), width)))
	}
	return lines
}

func (m Model) mentionSuggestionHeight() int {
	if !m.mentionActive {
		return 0
	}
	if len(m.mentionCandidates) == 0 {
		return 2
	}
	return 1 + min(5, len(m.mentionCandidates))
}

func (m Model) editPreviewLine(width int) string {
	if m.editTarget == nil {
		return ""
	}
	body := strings.TrimSpace(m.sanitizeDisplayLine(firstLine(m.editTarget.Body)))
	if body == "" {
		body = strings.TrimSpace(m.editTarget.RemoteID)
	}
	if body == "" {
		body = strings.TrimSpace(m.editTarget.ID)
	}
	return truncateDisplay("edit: "+body, width)
}

func (m Model) replyPreviewLine(width int) string {
	if m.replyTo == nil {
		return ""
	}
	sender := strings.TrimSpace(m.sanitizeDisplayLine(m.replyTo.Sender))
	if sender == "" {
		sender = "message"
	}
	body := strings.TrimSpace(m.sanitizeDisplayLine(firstLine(m.replyTo.Body)))
	if body == "" {
		body = strings.TrimSpace(m.replyTo.RemoteID)
	}
	if body == "" {
		body = strings.TrimSpace(m.replyTo.ID)
	}
	return truncateDisplay("reply "+sender+": "+body, width)
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
		lines = append(lines, m.renderAttachmentLine(media, width, true, ""))
	}
	return lines
}

func (m Model) renderMessageFooter(width int) string {
	if m.mode == ModeCommand || m.mode == ModeSearch || m.mode == ModeConfirm {
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
		m.renderFooterHelpLine(width),
		truncateDisplay(input, width),
	}
	style := lipgloss.NewStyle().Foreground(softFG).Width(width)
	if !barsTransparent() {
		style = style.Background(uiTheme.BarBG)
	}
	return style.Render(strings.Join(lines, "\n"))
}

func (m Model) renderFooterHelpLine(width int) string {
	notice := ""
	chatID := m.currentChat().ID
	if m.hasNewMessagesBelow(chatID) {
		notice = "↓ new messages below"
	}
	return renderFooterHelpLine(width, notice)
}

func renderFooterHelpLine(width int, notice string) string {
	width = max(1, width)
	hint := "? help"
	notice = strings.TrimSpace(notice)
	if notice == "" && width <= lipgloss.Width(hint) {
		return lipgloss.NewStyle().Foreground(accentFG).Render(truncateDisplay(hint, width))
	}
	if notice != "" {
		noticeWidth := lipgloss.Width(notice)
		hintWidth := lipgloss.Width(hint)
		if width <= noticeWidth+hintWidth+1 {
			return lipgloss.NewStyle().Foreground(warnFG).Render(truncateDisplay(notice, width))
		}
		fill := strings.Repeat("-", max(0, width-noticeWidth-hintWidth-2))
		return lipgloss.NewStyle().Foreground(warnFG).Render(notice) +
			lipgloss.NewStyle().Foreground(borderColor).Render(" "+fill+" ") +
			lipgloss.NewStyle().Foreground(accentFG).Render(hint)
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
	if m.syncOverlay.Visible || m.inlineFallbackPrompt {
		return 0
	}
	switch m.mode {
	case ModeCommand, ModeSearch, ModeConfirm:
		return 1
	default:
		return 0
	}
}

func (m Model) composerHeight() int {
	if m.mode != ModeInsert {
		return 0
	}
	extra := len(m.attachments) + 2 + m.mentionSuggestionHeight()
	if m.replyTo != nil {
		extra++
	}
	if m.editTarget != nil {
		extra++
	}
	return min(12, max(3, len(composerLines(m.composer))+extra))
}

func (m Model) renderHelp(width int) string {
	keys := m.config.Keymap
	leader := m.config.LeaderKey

	key := func(binding string) string {
		return displayBinding(binding, leader)
	}
	keysFor := func(bindings ...string) string {
		return displayUniqueBindings(leader, bindings...)
	}

	quick := []helpRow{
		{Key: keysFor(keys.NormalMoveDown, keys.NormalMoveUp), Action: "move"},
		{Key: key(keys.NormalOpen), Action: "open/preview"},
		{Key: key(keys.NormalReply), Action: "reply"},
		{Key: key(keys.NormalSearch), Action: "search"},
		{Key: key(keys.NormalCommand), Action: "command"},
		{Key: keysFor(keys.HelpClose, keys.HelpCloseAlt), Action: "close help"},
	}

	navigation := helpSection{
		Title: "Navigation",
		Rows: []helpRow{
			{Key: keysFor(keys.NormalMoveDown, keys.NormalMoveUp), Action: "move selection; count like 5" + key(keys.NormalMoveDown)},
			{Key: keysFor(keys.NormalGoTop, keys.NormalGoBottom), Action: "jump top / bottom"},
			{Key: keysFor(keys.NormalFocusLeft, keys.NormalFocusRightOrReply), Action: "switch pane / edge reply"},
			{Key: keysFor(keys.NormalFocusNext, keys.NormalFocusPrevious), Action: "cycle focus"},
			{Key: keysFor(keys.NormalSearchNext, keys.NormalSearchPrevious), Action: "next / previous search match"},
			{Key: keysFor(keys.NormalToggleUnread, keys.NormalTogglePinned), Action: "unread filter / pinned sort"},
		},
	}
	mediaActions := helpSection{
		Title: "Messages and Media",
		Rows: []helpRow{
			{Key: key(keys.NormalOpen), Action: "preview or open selected row"},
			{Key: key(keys.NormalOpenMedia), Action: "open selected media"},
			{Key: key(keys.NormalYankMessage), Action: "yank selected message"},
			{Key: key(keys.NormalEditMessage), Action: "edit outgoing text"},
			{Key: key(keys.NormalPickSticker), Action: "pick recent sticker"},
			{Key: keysFor(keys.NormalSaveMedia, keys.NormalCopyImage), Action: "save media / copy image"},
			{Key: key(keys.NormalUnloadPreviews), Action: "hide previews"},
			{Key: keysFor(keys.NormalReply, keys.NormalFocusRightOrReply), Action: "reply / right-edge reply"},
			{Key: key(keys.NormalRetryFailedMedia), Action: "retry failed media"},
			{Key: key(keys.NormalDeleteForEverybody), Action: "delete for everyone"},
		},
	}
	modes := helpSection{
		Title: "Modes",
		Rows: []helpRow{
			{Key: key(keys.NormalInsert), Action: "compose in insert mode"},
			{Key: keysFor(keys.InsertSend, keys.CommandRun, keys.SearchRun), Action: "send or run current prompt"},
			{Key: keysFor(keys.InsertNewline, keys.InsertNewlineAlt), Action: "insert newline"},
			{Key: keysFor(keys.InsertAttach, keys.InsertPasteImage, keys.InsertRemoveAttachment), Action: "attach, paste image, remove"},
			{Key: keysFor(keys.NormalVisual, keys.VisualYank, keys.VisualCancel), Action: "select, yank, cancel"},
			{Key: keysFor(keys.VisualForward, keys.ForwardSearch, keys.ForwardToggle, keys.ForwardSend), Action: "forward selected messages"},
			{Key: key(keys.ConfirmRun), Action: "confirm only after uppercase Y"},
		},
	}
	commands := helpSection{
		Title: "Commands",
		Rows: []helpRow{
			{Key: "filter", Action: "unread/all/messages, clear-search"},
			{Key: "sort", Action: "pinned/recent"},
			{Key: "media", Action: "preview/open/save/hide, copy-image"},
			{Key: "chat", Action: "history fetch, mark-read, quote-jump"},
			{Key: "send", Action: "react <emoji>|clear, retry-message|retry"},
			{Key: "more", Action: "edit-message, sticker, preview-backend, attach, paste-image, delete-message-everybody"},
		},
	}

	lines := []string{
		lipgloss.NewStyle().Bold(true).Foreground(accentFG).Render("vimwhat help"),
		lipgloss.NewStyle().Foreground(softFG).Render("Key labels follow your config. Commands are typed after " + key(keys.NormalCommand) + "."),
	}
	lines = append(lines, renderHelpInlineRows(quick, width)...)
	lines = append(lines, "")
	if width >= 84 {
		lines = append(lines, renderHelpColumns(width, navigation, mediaActions)...)
		lines = append(lines, "")
		lines = append(lines, renderHelpColumns(width, modes, commands)...)
	} else {
		for _, section := range []helpSection{navigation, mediaActions, modes, commands} {
			lines = append(lines, strings.Split(renderHelpSection(section, width), "\n")...)
			lines = append(lines, "")
		}
		if len(lines) > 0 && lines[len(lines)-1] == "" {
			lines = lines[:len(lines)-1]
		}
	}
	lines = append(lines, lipgloss.NewStyle().Foreground(softFG).Render(fmt.Sprintf(
		"state: mode=%s focus=%s filter=%s sort=%s search=%q",
		m.mode,
		m.focus,
		boolLabel(m.unreadOnly, "unread", "all"),
		boolLabel(m.pinnedFirst, "pinned", "recent"),
		m.activeSearch,
	)))

	for i, line := range lines {
		lines[i] = truncateDisplay(line, width)
	}
	return strings.Join(lines, "\n")
}

type helpRow struct {
	Key    string
	Action string
}

type helpSection struct {
	Title string
	Rows  []helpRow
}

func renderHelpColumns(width int, left, right helpSection) []string {
	gap := 2
	leftWidth := (width - gap) / 2
	rightWidth := width - gap - leftWidth
	joined := lipgloss.JoinHorizontal(
		lipgloss.Top,
		renderHelpSection(left, leftWidth),
		strings.Repeat(" ", gap),
		renderHelpSection(right, rightWidth),
	)
	return strings.Split(joined, "\n")
}

func renderHelpSection(section helpSection, width int) string {
	width = max(1, width)
	lines := []string{
		truncateDisplay(lipgloss.NewStyle().Bold(true).Foreground(accentFG).Render(section.Title), width),
	}
	keyWidth := helpKeyWidth(section.Rows, width)
	for _, row := range section.Rows {
		lines = append(lines, renderHelpRow(row, keyWidth, width)...)
	}
	return strings.Join(lines, "\n")
}

func renderHelpRow(row helpRow, keyWidth, width int) []string {
	width = max(1, width)
	keyWidth = min(keyWidth, max(1, width-2))
	actionWidth := max(1, width-keyWidth-2)
	keyStyle := lipgloss.NewStyle().Bold(true).Foreground(warnFG)
	actionStyle := lipgloss.NewStyle().Foreground(primaryFG)
	softStyle := lipgloss.NewStyle().Foreground(softFG)

	actionLines := strings.Split(wrapPlainText(row.Action, actionWidth), "\n")
	lines := make([]string, 0, len(actionLines))
	for i, action := range actionLines {
		keyLabel := strings.Repeat(" ", keyWidth)
		if i == 0 {
			keyLabel = padDisplay(truncateDisplay(row.Key, keyWidth), keyWidth)
			keyLabel = keyStyle.Render(keyLabel)
		}
		lines = append(lines, truncateDisplay(
			keyLabel+softStyle.Render("  ")+actionStyle.Render(action),
			width,
		))
	}
	return lines
}

func renderHelpInlineRows(rows []helpRow, width int) []string {
	width = max(1, width)
	keyStyle := lipgloss.NewStyle().Bold(true).Foreground(warnFG)
	actionStyle := lipgloss.NewStyle().Foreground(primaryFG)
	softStyle := lipgloss.NewStyle().Foreground(softFG)
	separator := softStyle.Render("   ")

	var lines []string
	line := ""
	for _, row := range rows {
		segment := keyStyle.Render(row.Key) + softStyle.Render(" ") + actionStyle.Render(row.Action)
		if line == "" {
			line = segment
			continue
		}
		if displayWidth(line)+displayWidth(separator)+displayWidth(segment) <= width {
			line += separator + segment
			continue
		}
		lines = append(lines, truncateDisplay(line, width))
		line = segment
	}
	if line != "" {
		lines = append(lines, truncateDisplay(line, width))
	}
	return lines
}

func displayUniqueBindings(leader string, bindings ...string) string {
	seen := map[string]bool{}
	var labels []string
	for _, binding := range bindings {
		label := displayBinding(binding, leader)
		if label == "" || seen[label] {
			continue
		}
		seen[label] = true
		labels = append(labels, label)
	}
	return strings.Join(labels, "/")
}

func helpKeyWidth(rows []helpRow, width int) int {
	keyWidth := 1
	for _, row := range rows {
		keyWidth = max(keyWidth, displayWidth(row.Key))
	}
	return min(keyWidth, max(6, width/3))
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
	borderShape := lipgloss.NormalBorder()
	if m.focus == focus {
		border = activeBorder
		borderShape = lipgloss.ThickBorder()
	}

	return lipgloss.NewStyle().
		Border(borderShape).
		BorderForeground(border).
		Padding(0, 1)
}

func composerLines(value string) []string {
	if value == "" {
		return []string{""}
	}
	return strings.Split(value, "\n")
}
