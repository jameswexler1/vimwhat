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

	blocks := make([]messageLayoutBlock, 0, len(m.currentMessages()))
	candidates := map[string]mediaPlacementCandidate{}
	var lastDate string
	for i, message := range m.currentMessages() {
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

	bodyHeight := max(1, height-1)
	footer := m.renderMessageFooter(max(1, width-2))
	footerHeight := min(countLines(footer), bodyHeight)
	messageHeight := max(1, bodyHeight-footerHeight)
	viewportRefs := messageViewportRefs(blocks, m.messageScrollTop, clamp(m.messageCursor, 0, len(blocks)-1), messageHeight)
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
	height = max(1, height)
	if len(blocks) == 0 {
		return nil
	}

	selected := clamp(cursor, 0, len(blocks)-1)
	if selected == len(blocks)-1 {
		return bottomMessageViewportRefs(blocks, height)
	}
	plainBlocks := make([]messageBlock, len(blocks))
	for i, block := range blocks {
		plainBlocks[i] = messageBlock{lines: block.lines}
	}
	scrollTop = adjustedMessageScrollTop(plainBlocks, scrollTop, selected, height)
	scrollTop = clampMessageScrollTop(plainBlocks, scrollTop, height)
	var out []messageLineRef
	used := 0

	for i := scrollTop; i < len(blocks) && used < height; i++ {
		block := blocks[i]
		if used+len(block.lines) > height {
			remaining := height - used
			if remaining > 0 {
				if i == selected || i == len(blocks)-1 {
					out = append(out, tailRefs(block.refs, remaining)...)
				} else {
					out = append(out, block.refs[:remaining]...)
				}
			}
			break
		}
		out = append(out, block.refs...)
		used += len(block.lines)
	}

	if len(out) > height {
		return tailRefs(out, height)
	}
	return out
}

func bottomMessageViewportRefs(blocks []messageLayoutBlock, height int) []messageLineRef {
	height = max(1, height)
	plainBlocks := make([]messageBlock, len(blocks))
	for i, block := range blocks {
		plainBlocks[i] = messageBlock{lines: block.lines}
	}
	if messageBlocksHeight(plainBlocks) <= height {
		var out []messageLineRef
		for _, block := range blocks {
			out = append(out, block.refs...)
		}
		return out
	}

	var out []messageLineRef
	used := 0
	for i := len(blocks) - 1; i >= 0; i-- {
		block := blocks[i]
		if used+len(block.lines) > height {
			remaining := height - used
			if remaining > 0 {
				out = append(tailRefs(block.refs, remaining), out...)
			}
			break
		}
		out = append(block.refs, out...)
		used += len(block.lines)
	}
	return out
}

func tailRefs(refs []messageLineRef, height int) []messageLineRef {
	height = max(1, height)
	if len(refs) <= height {
		return refs
	}
	return refs[len(refs)-height:]
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
