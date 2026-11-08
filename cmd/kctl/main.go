// Command kctl is the customer, partner and operator CLI for the Kondukter API.
package main

import (
	"context"
	"os"
	"os/signal"

	"github.com/St1viG/cj/internal/kctl"
)

func main() {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt)
	code := kctl.Run(ctx, os.Args[1:], kctl.Options{})
	stop()
	os.Exit(code)
}
