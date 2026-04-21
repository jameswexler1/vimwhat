package main

import (
	"os"

	"maybewhats/internal/app"
)

func main() {
	os.Exit(app.Main(os.Args[1:]))
}
