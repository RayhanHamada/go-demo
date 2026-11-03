// Command server starts the HTTP API. All wiring lives in internal/app.
//
// The Swagger documentation is generated from annotations in this file and in
// internal/adapter/httpapi/handlers. Regenerate after changing a handler:
//
//	go tool swag init -g cmd/server/main.go -o internal/adapter/httpapi/docs --parseInternal --outputTypes go,json,yaml
//
// @title gin-clean-arch-demo
// @version 1.0.0
// @description A user CRUD service demonstrating clean architecture: handlers in `internal/adapter/httpapi/handlers`, application services in `internal/usecase`, invariants in `internal/domain`, persistence in `internal/adapter/sqlite`.
// @description
// @description Identifiers are UUID v7, so they sort by creation time. Emails are normalised to lower case and unique. Reads are served through a cache that is invalidated on write.
// @description
// @description Every response carries an `X-Request-ID` header: send your own to have it echoed, otherwise one is generated. Either way it appears in the server logs, so a failing request is traceable end to end.
// @termsOfService https://opensource.org/licenses/MIT
// @license.name MIT
// @BasePath /
// @schemes http https
package main

import (
	"os"

	"gin-clean-arch-demo/internal/app"
)

func main() {
	os.Exit(app.Main())
}
