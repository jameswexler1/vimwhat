package ui

func (m Model) syncBlocksUI() bool {
	return m.syncOverlay.Visible && !m.backgroundSync
}
