package ui

func (m Model) syncBlocksUI() bool {
	return !m.backgroundSync && !m.browseDuringSync &&
		(m.syncOverlay.Visible || m.startupProgress.Active || m.startupProgress.Failed)
}
