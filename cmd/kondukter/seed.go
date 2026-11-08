package main

import (
	"fmt"
	"os"

	"github.com/spf13/cobra"

	"github.com/St1viG/cj/internal/controlplane/catalog"
	"github.com/St1viG/cj/internal/store"
)

func newSeedCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "seed",
		Short: "Insert demo data (idempotent)",
	}
	catalogCmd := &cobra.Command{
		Use:   "catalog",
		Short: "Upsert the demo regions, shapes, offers and images",
		Long:  "Upserts the built-in demo catalog (ROADMAP §1.3), or --file. ${VAR} in the file is expanded from the environment.",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			u, err := databaseURL(cmd)
			if err != nil {
				return err
			}
			raw := catalog.Demo
			if path, _ := cmd.Flags().GetString("file"); path != "" {
				if raw, err = os.ReadFile(path); err != nil { //nolint:gosec // operator-chosen file
					return err
				}
			}
			f, err := catalog.Parse(raw)
			if err != nil {
				return err
			}
			if err := store.MigrateUp(cmd.Context(), u); err != nil {
				return err
			}
			s, err := store.Open(cmd.Context(), u)
			if err != nil {
				return err
			}
			defer s.Close()
			rep, err := catalog.Seed(cmd.Context(), s, f)
			if err != nil {
				return err
			}
			out := cmd.OutOrStdout()
			fmt.Fprintf(out, "catalog: %d regions, %d shapes, %d offers, %d images\n", rep.Regions, rep.Shapes, rep.Offers, rep.Images)
			for _, sk := range rep.Skipped {
				fmt.Fprintf(out, "skipped image %s: no digest yet\n", sk)
			}
			return nil
		},
	}
	catalogCmd.Flags().String("file", "", "catalog YAML to seed instead of the built-in demo catalog")
	cmd.AddCommand(catalogCmd)
	return cmd
}
