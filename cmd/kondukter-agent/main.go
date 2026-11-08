// Command kondukter-agent runs on a partner node, dials out to Kondukter and
// reconciles desired state against the local hypervisor (SPEC §7).
package main

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/signal"
	"syscall"

	"github.com/spf13/cobra"

	"github.com/St1viG/cj/internal/agent"
	"github.com/St1viG/cj/internal/agent/config"
	"github.com/St1viG/cj/internal/logging"
	"github.com/St1viG/cj/internal/version"
)

func main() {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	err := newRootCmd().ExecuteContext(ctx)
	stop()
	if err != nil {
		fmt.Fprintln(os.Stderr, "kondukter-agent:", err)
		os.Exit(1)
	}
}

func newRootCmd() *cobra.Command {
	root := &cobra.Command{
		Use:           "kondukter-agent",
		Short:         "Kondukter node agent",
		Version:       version.Get(),
		SilenceUsage:  true,
		SilenceErrors: true,
	}
	root.SetVersionTemplate(version.String("kondukter-agent") + "\n")
	root.PersistentFlags().String("config", config.DefaultPath(), "path to the agent config file")
	root.AddCommand(newRunCmd(), newEnrollCmd(), newVersionCmd())
	return root
}

func newVersionCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "version",
		Short: "Print the version",
		Args:  cobra.NoArgs,
		Run: func(cmd *cobra.Command, _ []string) {
			fmt.Fprintln(cmd.OutOrStdout(), version.String("kondukter-agent"))
		},
	}
}

func newRunCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "run",
		Short: "Run the agent until SIGINT or SIGTERM",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			path, _ := cmd.Flags().GetString("config")
			cfg, err := config.Load(path)
			if err != nil {
				return err
			}
			log, err := logging.New(cmd.ErrOrStderr(), cfg.LogLevel)
			if err != nil {
				return err
			}
			insecure, _ := cmd.Flags().GetBool("insecure-dev")
			a, err := agent.New(cfg, log, agent.Options{InsecureDev: insecure})
			if err != nil {
				return err
			}
			runErr := a.Run(cmd.Context())
			return errors.Join(runErr, a.Close())
		},
	}
	cmd.Flags().Bool("insecure-dev", false, "do not verify the gateway certificate (kdk-stubgw only)")
	return cmd
}

func newEnrollCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "enroll",
		Short: "Exchange a one-time token for this node's mTLS identity",
		Args:  cobra.NoArgs,
		RunE: func(*cobra.Command, []string) error {
			return errors.New("enroll is not implemented yet (roadmap P3.2)")
		},
	}
	cmd.Flags().String("server", "", "Kondukter API URL")
	cmd.Flags().String("token", "", "one-time enrollment token")
	return cmd
}
