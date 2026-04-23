package ui

import (
	"crypto/sha1"
	"encoding/hex"
	"strings"

	"github.com/charmbracelet/lipgloss"

	"vimwhat/internal/media"
	"vimwhat/internal/store"
)

type messageLineRef struct {
	target string
	row    int
}

type messageLayoutBlock struct {
	lines []string
	refs  []messageLineRef
}

type mediaPlacementCandidate struct {
	message  store.Message
	preview  media.Preview
	active   bool
	selected bool
}

func (m Model) activeMediaPlacement() (media.Placement, bool) {
	message, item, ok := m.focusedMedia()
	if !ok {
		return media.Placement{}, false
	}
	preview, ok := m.mediaPreview(message, item, m.messagePaneContentWidth())
	if !ok || preview.Display != media.PreviewDisplayOverlay || !preview.Ready() {
		return media.Placement{}, false
	}
	identifier := overlayIdentifier(preview.Key)
	for _, placement := range m.visibleMediaPlacements() {
		if placement.Identifier == identifier {
			return placement, true
		}
	}
	return media.Placement{}, false
}

func (m Model) visibleOverlayIdentifiers() map[string]bool {
	placements := m.visibleMediaPlacements()
	visible := make(map[string]bool, len(placements))
	for _, placement := range placements {
		visible[placement.Identifier] = true
	}
	return visible
}

func (m Model) visibleMediaPlacements() []media.Placement {
	if m.width <= 0 || m.height <= 0 {
		return nil
	}
	geometry, ok := m.messagePaneGeometry()
	if !ok {
		return nil
	}
	candidates, viewportRefs := m.mediaPlacementRefs(geometry.width, geometry.height)
	if len(candidates) == 0 || len(viewportRefs) == 0 {
		return nil
	}

	rowsByTarget := make(map[string]map[int]int, len(candidates))
	firstRows := make(map[string]int, len(candidates))
	for identifier := range candidates {
		firstRows[identifier] = -1
	}
	for y, ref := range viewportRefs {
		if ref.target == "" {
			continue
		}
		if rowsByTarget[ref.target] == nil {
			rowsByTarget[ref.target] = map[int]int{}
		}
		rowsByTarget[ref.target][ref.row] = y
		if ref.row == 0 {
			firstRows[ref.target] = y
		}
	}

	placements := make([]media.Placement, 0, len(candidates))
	for identifier, candidate := range candidates {
		preview := candidate.preview
		firstRow := firstRows[identifier]
		if firstRow == -1 || len(rowsByTarget[identifier]) < preview.Height {
			continue
		}
		xOffset := m.mediaXOffset(geometry.width, candidate)
		yOffset := 1 + firstRow
		placements = append(placements, media.Placement{
			Identifier: identifier,
			X:          geometry.x + xOffset,
			Y:          geometry.y + yOffset,
			MaxWidth:   min(preview.Width, max(1, geometry.width-xOffset)),
			MaxHeight:  min(preview.Height, max(1, geometry.height-yOffset)),
			Path:       preview.SourcePath,
			Scaler:     "fit_contain",
		})
	}
	return placements
}

type paneGeometry struct {
	x      int
	y      int
	width  int
	height int
}

func (m Model) messagePaneGeometry() (paneGeometry, bool) {
	inputHeight := m.inputHeight()
	bodyHeight := m.height - 1 - inputHeight
	if bodyHeight < 1 {
		bodyHeight = 1
	}

	style := m.panelStyle(FocusMessages)
	panelX := 0
	panelWidth := m.width
	if !m.compactLayout {
		chatWidth := max(24, m.width/4)
		previewWidth := max(26, m.width/4)
		panelX = chatWidth
		panelWidth = m.width - chatWidth
		if m.infoPaneVisible {
			panelWidth -= previewWidth
		}
	}
	if panelWidth <= 0 {
		return paneGeometry{}, false
	}
	return paneGeometry{
		x:      panelX + 1 + style.GetPaddingLeft(),
		y:      1,
		width:  panelContentWidth(style, panelWidth),
		height: panelContentHeight(style, bodyHeight),
	}, true
}

func (m Model) mediaPlacementRefs(width, height int) (map[string]mediaPlacementCandidate, []messageLineRef) {
	if width <= 0 || height <= 1 {
		return nil, nil
	}

	messages := m.currentMessages()
	bodyHeight := max(1, height-1)
	footer := m.renderMessageFooter(max(1, width-2))
	footerHeight := min(countLines(footer), bodyHeight)
	messageHeight := max(1, bodyHeight-footerHeight)
	start, end := m.visibleMessageRange(len(messages), messageHeight)

	blocks := make([]messageLayoutBlock, 0, end-start)
	candidates := map[string]mediaPlacementCandidate{}
	var lastDate string
	if start > 0 {
		lastDate = messageDate(messages[start-1])
	}
	for i := start; i < end; i++ {
		message := messages[i]
		date := messageDate(message)
		selected := m.mode == ModeVisual && i >= min(m.visualAnchor, m.messageCursor) && i <= max(m.visualAnchor, m.messageCursor)
		active := i == m.messageCursor
		bubble := m.renderMessageBubble(message, width, active, selected)
		bubbleLines := strings.Split(alignMessageBubble(bubble, width, message.IsOutgoing), "\n")
		refs := make([]messageLineRef, len(bubbleLines))

		for _, item := range message.Media {
			preview, ok := m.mediaPreview(message, item, width)
			if !ok || preview.Display != media.PreviewDisplayOverlay || !preview.Ready() {
				continue
			}
			lineOffset, ok := m.previewLineOffset(message, item, preview)
			if !ok {
				continue
			}
			identifier := overlayIdentifier(preview.Key)
			candidates[identifier] = mediaPlacementCandidate{
				message:  message,
				preview:  preview,
				active:   active,
				selected: selected,
			}
			for row := 0; row < preview.Height && lineOffset+row < len(refs); row++ {
				refs[lineOffset+row] = messageLineRef{target: identifier, row: row}
			}
		}

		lines := bubbleLines
		if date != "" && date != lastDate {
			lines = append([]string{renderDaySeparator(date, width)}, lines...)
			refs = append([]messageLineRef{{}}, refs...)
			lastDate = date
		}
		blocks = append(blocks, messageLayoutBlock{lines: lines, refs: refs})
	}

	viewportRefs := messageViewportRefs(
		blocks,
		clamp(m.messageScrollTop-start, 0, max(0, len(blocks)-1)),
		clamp(m.messageCursor-start, 0, max(0, len(blocks)-1)),
		messageHeight,
	)
	return candidates, viewportRefs
}

func (m Model) mediaXOffset(width int, candidate mediaPlacementCandidate) int {
	bubble := m.renderMessageBubble(candidate.message, width, candidate.active, candidate.selected)
	bubbleWidth := maxRenderedWidth(bubble)
	indent := 0
	if candidate.message.IsOutgoing {
		indent = max(0, width-bubbleWidth-3)
	}
	return indent + 2
}

func (m Model) previewLineOffset(message store.Message, item store.MediaMetadata, preview media.Preview) (int, bool) {
	senderLines := 0
	if shouldShowMessageSender(m.currentChat(), message) {
		senderLines = 1
	}
	offset := 1 + senderLines
	for _, candidate := range message.Media {
		if mediaActivationKey(message, candidate) == mediaActivationKey(message, item) {
			return offset, true
		}
		if existing, ok := m.mediaPreview(message, candidate, preview.Width); ok {
			offset += len(renderPreviewLines(existing, preview.Width, true))
		} else {
			offset++
		}
	}
	return 0, false
}

func messageViewportRefs(blocks []messageLayoutBlock, scrollTop, cursor, height int) []messageLineRef {
	if len(blocks) == 0 {
		return nil
	}
	plainBlocks := make([]messageBlock, len(blocks))
	for i, block := range blocks {
		plainBlocks[i] = messageBlock{lines: block.lines}
	}

	spans := messageViewportSpans(plainBlocks, scrollTop, cursor, height)
	var out []messageLineRef
	for _, span := range spans {
		if span.index < 0 || span.index >= len(blocks) {
			continue
		}
		refs := blocks[span.index].refs
		before := len(out)
		start := clamp(span.start, 0, len(refs))
		end := clamp(span.end, start, len(refs))
		out = append(out, refs[start:end]...)
		for len(out)-before < span.end-span.start {
			out = append(out, messageLineRef{})
		}
	}
	return out
}

func maxRenderedWidth(value string) int {
	width := 0
	for _, line := range strings.Split(value, "\n") {
		width = max(width, lipgloss.Width(line))
	}
	return width
}

func overlayIdentifier(key string) string {
	sum := sha1.Sum([]byte(key))
	return "vimwhat-" + hex.EncodeToString(sum[:8])
}
