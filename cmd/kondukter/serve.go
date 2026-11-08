package main

import (
	"os"
	"strings"

	"github.com/spf13/cobra"

	"github.com/St1viG/cj/internal/controlplane/server"
	"github.com/St1viG/cj/internal/logging"
)

func newServeCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "serve",
		Short: "Run the control plane: public API and agent gateway",
		Long: "Migrates the database, then serves the Connect API (plain HTTP and h2c) and the " +
			"mTLS agent gateway. Settings come from KONDUKTER_* environment variables; flags override them.",
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			cfg := server.ConfigFromEnv()
			if v, _ := cmd.Flags().GetString("database-url"); v != "" {
				cfg.DatabaseURL = v
			}
			for flag, dst := range map[string]*string{
				"api-addr": &cfg.APIAddr, "gateway-addr": &cfg.GatewayAddr,
				"gateway-advertise": &cfg.GatewayAdvertise, "public-url": &cfg.PublicURL,
			} {
				if v, _ := cmd.Flags().GetString(flag); v != "" {
					*dst = v
				}
			}
			if v, _ := cmd.Flags().GetString("gateway-hosts"); v != "" {
				cfg.GatewayHosts = strings.Split(v, ",")
			}
			level, _ := cmd.Flags().GetString("log-level")
			log, err := logging.New(os.Stderr, level)
			if err != nil {
				return err
			}
			srv, err := server.New(cmd.Context(), cfg, log)
			if err != nil {
				return err
			}
			defer srv.Close()
			return srv.Run(cmd.Context())
		},
	}
	f := cmd.Flags()
	f.String("api-addr", "", "API listen address (env KONDUKTER_API_ADDR, default :8080)")
	f.String("gateway-addr", "", "gateway listen address (env KONDUKTER_GATEWAY_ADDR, default :8443)")
	f.String("gateway-hosts", "", "comma-separated names/IPs for the gateway certificate (env KONDUKTER_GATEWAY_HOSTS)")
	f.String("gateway-advertise", "", "host:port agents dial (env KONDUKTER_GATEWAY_ADVERTISE)")
	f.String("public-url", "", "API URL used in enroll commands (env KONDUKTER_PUBLIC_URL)")
	f.String("log-level", "info", "debug, info, warn or error")
	return cmd
}
