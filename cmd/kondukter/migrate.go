package main

import (
	"fmt"
	"text/tabwriter"

	"github.com/pressly/goose/v3"
	"github.com/spf13/cobra"

	"github.com/St1viG/cj/internal/store"
)

func newMigrateCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "migrate",
		Short: "Apply or roll back the embedded database migrations",
	}
	cmd.AddCommand(
		migrateSub("up", "Apply every pending migration", func(cmd *cobra.Command, m *store.Migrator) error {
			res, err := m.Up(cmd.Context())
			printResults(cmd, res...)
			return err
		}),
		migrateSub("down", "Roll back the most recent migration", func(cmd *cobra.Command, m *store.Migrator) error {
			res, err := m.Down(cmd.Context())
			if res != nil {
				printResults(cmd, res)
			}
			return err
		}),
		migrateSub("reset", "Roll back every migration (drops all data)", func(cmd *cobra.Command, m *store.Migrator) error {
			res, err := m.Reset(cmd.Context())
			printResults(cmd, res...)
			return err
		}),
		migrateSub("status", "List migrations and whether each is applied", func(cmd *cobra.Command, m *store.Migrator) error {
			st, err := m.Status(cmd.Context())
			if err != nil {
				return err
			}
			w := tabwriter.NewWriter(cmd.OutOrStdout(), 0, 4, 2, ' ', 0)
			fmt.Fprintln(w, "VERSION\tSTATE\tAPPLIED AT\tFILE")
			for _, s := range st {
				at := "-"
				if s.State == goose.StateApplied {
					at = s.AppliedAt.UTC().Format("2006-01-02 15:04:05Z")
				}
				fmt.Fprintf(w, "%d\t%s\t%s\t%s\n", s.Source.Version, s.State, at, s.Source.Path)
			}
			return w.Flush()
		}),
	)
	return cmd
}

func migrateSub(use, short string, run func(*cobra.Command, *store.Migrator) error) *cobra.Command {
	return &cobra.Command{
		Use:   use,
		Short: short,
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			u, err := databaseURL(cmd)
			if err != nil {
				return err
			}
			m, err := store.NewMigrator(u)
			if err != nil {
				return err
			}
			defer func() { _ = m.Close() }()
			return run(cmd, m)
		},
	}
}

func printResults(cmd *cobra.Command, res ...*goose.MigrationResult) {
	if len(res) == 0 {
		fmt.Fprintln(cmd.OutOrStdout(), "no migrations to apply")
	}
	for _, r := range res {
		fmt.Fprintf(cmd.OutOrStdout(), "%-4s %s (%s)\n", r.Direction, r.Source.Path, r.Duration.Round(1e6))
	}
}
