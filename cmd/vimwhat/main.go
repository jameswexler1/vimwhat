package main

import (
	"os"

	"vimwhat/internal/app"
)

func main() {
	os.Exit(app.Main(os.Args[1:]))
}
