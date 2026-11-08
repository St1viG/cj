package main

import (
	"errors"
	"fmt"

	"github.com/spf13/cobra"

	"github.com/St1viG/cj/internal/controlplane/enrollment"
	"github.com/St1viG/cj/internal/controlplane/pki"
	"github.com/St1viG/cj/internal/controlplane/server"
	"github.com/St1viG/cj/internal/store"
	"github.com/St1viG/cj/internal/store/db"
)

func newTokenCmd() *cobra.Command {
	cmd := &cobra.Command{Use: "token", Short: "Manage node enrollment tokens"}
	create := &cobra.Command{
		Use:   "create",
		Short: "Create a one-time enrollment token for a site",
		Long: "Prints a one-hour, single-use token and the command that installs and enrolls the agent with it. " +
			"Reads KONDUKTER_MASTER_KEY and KONDUKTER_PUBLIC_URL like `serve`. Partners get the same through " +
			"PartnerService.CreateEnrollmentToken.",
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			partnerName, _ := cmd.Flags().GetString("partner")
			siteName, _ := cmd.Flags().GetString("site")
			cfg := server.ConfigFromEnv()
			if cfg.MasterKey == "" {
				return errors.New("KONDUKTER_MASTER_KEY is required")
			}
			s, err := openMigrated(cmd)
			if err != nil {
				return err
			}
			defer s.Close()
			ctx := cmd.Context()
			site, err := s.GetSiteByName(ctx, db.GetSiteByNameParams{PartnerName: partnerName, SiteName: siteName})
			if err != nil {
				if errors.Is(store.NotFound(err), store.ErrNotFound) {
					return fmt.Errorf("no site %s/%s (run `kondukter seed accounts`)", partnerName, siteName)
				}
				return err
			}
			ca, err := pki.LoadOrCreate(ctx, s, cfg.MasterKey)
			if err != nil {
				return err
			}
			tok, err := enrollment.New(s, ca, cfg.GatewayAdvertise, cfg.PublicURL).CreateToken(ctx, site.ID, nil)
			if err != nil {
				return err
			}
			fmt.Fprintf(cmd.OutOrStdout(), "token   %s\nexpires %s\nrun     %s\n", tok.Plaintext, tok.ExpiresAt.Format("2006-01-02 15:04:05 MST"), tok.Command)
			return nil
		},
	}
	create.Flags().String("partner", "demo-dc", "partner name")
	create.Flags().String("site", "demo", "site name")
	cmd.AddCommand(create)
	return cmd
}
