//go:generate swag init -g cmd/api/main.go -o docs --parseDependency

// @title			Reposeetory API
// @version			1.0
// @description		GitHub Release Notification API. Subscribe to repositories and get email notifications on new releases.
// @contact.name	ananaslegend
// @contact.url		https://github.com/ananaslegend/reposeetory
// @license.name	MIT
// @host			reposeetory.com
// @BasePath		/
package main

import (
	"context"
	"os/signal"
	"syscall"

	"github.com/ananaslegend/reposeetory/internal/app"
)

func main() {
	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	app.Run(ctx)
}
