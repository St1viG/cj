package main

import (
	"context"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"strings"
	"time"

	"github.com/spf13/cobra"
)

// newHealthcheckCmd probes /healthz, for the compose healthcheck: the
// distroless image has no shell or curl.
func newHealthcheckCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "healthcheck",
		Short: "Exit 0 if the local API answers /healthz (API up, database reachable)",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			url, _ := cmd.Flags().GetString("url")
			if url == "" {
				url = localHealthURL(os.Getenv("KONDUKTER_API_ADDR"))
			}
			ctx, cancel := context.WithTimeout(cmd.Context(), 3*time.Second)
			defer cancel()
			req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil) //nolint:gosec // G704: the operator's --url, or loopback
			if err != nil {
				return err
			}
			res, err := http.DefaultClient.Do(req) //nolint:gosec // G704: same URL
			if err != nil {
				return err
			}
			defer res.Body.Close()
			body, _ := io.ReadAll(io.LimitReader(res.Body, 256))
			if res.StatusCode != http.StatusOK {
				return fmt.Errorf("%s: %s: %s", url, res.Status, strings.TrimSpace(string(body)))
			}
			return nil
		},
	}
	cmd.Flags().String("url", "", "health URL (default: /healthz on the port of KONDUKTER_API_ADDR, else 8080)")
	return cmd
}

// localHealthURL is /healthz on loopback at the port of the API listen
// address, e.g. ":8080" or "0.0.0.0:9000".
func localHealthURL(apiAddr string) string {
	port := "8080"
	if _, p, err := net.SplitHostPort(apiAddr); err == nil && p != "" {
		port = p
	}
	return "http://" + net.JoinHostPort("127.0.0.1", port) + "/healthz"
}
