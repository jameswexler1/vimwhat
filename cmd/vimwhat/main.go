// vimwhat - vim-modal WhatsApp TUI
// Copyright (C) 2026 James Wexler
// SPDX-License-Identifier: GPL-3.0-or-later

package main

import (
	"os"

	"vimwhat/internal/app"
)

func main() {
	os.Exit(app.Main(os.Args[1:]))
}
