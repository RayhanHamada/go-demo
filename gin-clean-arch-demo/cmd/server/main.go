// Command server starts the HTTP API. All wiring lives in internal/app.
package main

import (
	"os"

	"gin-clean-arch-demo/internal/app"
)

func main() {
	os.Exit(app.Main())
}
