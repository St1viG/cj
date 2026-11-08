// Command kondukter-agent runs on a partner node, dials out to Kondukter and
// reconciles desired state against the local hypervisor (SPEC §7).
package main

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/signal"
	"path/filepath"
	"syscall"
	"time"

	"github.com/spf13/cobra"
	"google.golang.org/protobuf/encoding/protojson"

	"github.com/St1viG/cj/internal/agent"
	"github.com/St1viG/cj/internal/agent/config"
	"github.com/St1viG/cj/internal/agent/enroll"
	"github.com/St1viG/cj/internal/agent/inventory"
	"github.com/St1viG/cj/internal/agent/store"
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
	root.AddCommand(newRunCmd(), newEnrollCmd(), newInventoryCmd(), newVersionCmd())
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
		Long: "Generates a key, sends a CSR with the token to Kondukter, verifies and stores the " +
			"returned certificate, and writes server and gateway into the config file.",
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			path, _ := cmd.Flags().GetString("config")
			cfg, err := config.Load(path)
			if err != nil {
				return err
			}
			server, _ := cmd.Flags().GetString("server")
			if server == "" {
				server = cfg.Server
			}
			token, _ := cmd.Flags().GetString("token")
			force, _ := cmd.Flags().GetBool("force")
			st, err := store.Open(filepath.Join(cfg.DataDir, store.FileName))
			if err != nil {
				return err
			}
			defer func() { _ = st.Close() }()
			hostname, _ := os.Hostname()
			res, err := enroll.Run(cmd.Context(), enroll.Options{
				Server: server, Token: token, Store: st, Hostname: hostname,
				AgentVersion: version.Get(), Force: force,
			})
			if err != nil {
				return err
			}
			if err := config.SetKeys(path, map[string]string{"server": server, "gateway": res.GatewayAddress}); err != nil {
				return fmt.Errorf("enrolled as node %s, but updating %s failed: %w", res.NodeID, path, err)
			}
			fmt.Fprintf(cmd.OutOrStdout(), "enrolled as node %s; gateway %s; certificate valid until %s\n",
				res.NodeID, res.GatewayAddress, res.NotAfter.UTC().Format(time.RFC3339))
			return nil
		},
	}
	cmd.Flags().String("server", "", "Kondukter API URL (default: server from the config)")
	cmd.Flags().String("token", "", "one-time enrollment token")
	cmd.Flags().Bool("force", false, "replace an existing identity")
	return cmd
}

func newInventoryCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "inventory",
		Short: "Discover this host and print the Inventory the agent would report",
		Long: "Discovers CPUs, memory, disk and NVIDIA GPUs (with passthrough health) " +
			"and prints them as JSON. Reservations and labels come from the config if it loads.",
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			path, _ := cmd.Flags().GetString("config")
			cfg, err := config.Load(path)
			if err != nil {
				cfg = config.Default()
				fmt.Fprintf(cmd.ErrOrStderr(), "note: %v; showing totals without reservations\n", err)
			}
			if d, _ := cmd.Flags().GetString("data-dir"); d != "" {
				cfg.DataDir = d
			}
			inv, err := inventory.Discover(cmd.Context(), inventory.Options{DataDir: existingDir(cfg.DataDir)})
			if err != nil {
				return err
			}
			out, err := protojson.MarshalOptions{Multiline: true, UseProtoNames: true, EmitUnpopulated: true}.
				Marshal(agent.InventoryProto(inv, cfg))
			if err != nil {
				return err
			}
			fmt.Fprintln(cmd.OutOrStdout(), string(out))
			return nil
		},
	}
	cmd.Flags().String("data-dir", "", "directory whose filesystem holds instance disks (default: data_dir from the config)")
	return cmd
}

// existingDir walks up to the nearest existing directory, so disk size can
// be measured before the data dir is created.
func existingDir(dir string) string {
	for d := dir; ; d = filepath.Dir(d) {
		if fi, err := os.Stat(d); err == nil && fi.IsDir() {
			return d
		}
		if parent := filepath.Dir(d); parent == d {
			return d
		}
	}
}
