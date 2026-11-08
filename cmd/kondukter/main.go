// Command kondukter is the control plane: public Connect API, agent gateway,
// scheduler and controllers in one binary (SPEC §6).
package main

import (
	"context"
	"fmt"
	"os"
	"os/signal"
	"syscall"

	"github.com/spf13/cobra"

	"github.com/St1viG/cj/internal/version"
)

func main() {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	err := newRootCmd().ExecuteContext(ctx)
	stop()
	if err != nil {
		fmt.Fprintln(os.Stderr, "kondukter:", err)
		os.Exit(1)
	}
}

func newRootCmd() *cobra.Command {
	root := &cobra.Command{
		Use:           "kondukter",
		Short:         "Kondukter control plane",
		Version:       version.Get(),
		SilenceUsage:  true,
		SilenceErrors: true,
	}
	root.SetVersionTemplate(version.String("kondukter") + "\n")
	root.PersistentFlags().String("database-url", os.Getenv("KONDUKTER_DATABASE_URL"),
		"Postgres connection URL (env KONDUKTER_DATABASE_URL)")
	root.AddCommand(newVersionCmd(), newMigrateCmd(), newServeCmd(), newSeedCmd(), newTokenCmd())
	return root
}

func newVersionCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "version",
		Short: "Print the version",
		Args:  cobra.NoArgs,
		Run: func(cmd *cobra.Command, _ []string) {
			fmt.Fprintln(cmd.OutOrStdout(), version.String("kondukter"))
		},
	}
}

func databaseURL(cmd *cobra.Command) (string, error) {
	u, _ := cmd.Flags().GetString("database-url")
	if u == "" {
		return "", fmt.Errorf("no database: set --database-url or KONDUKTER_DATABASE_URL")
	}
	return u, nil
}
