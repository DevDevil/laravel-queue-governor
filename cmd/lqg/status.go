package main

import (
	"github.com/spf13/cobra"

	"github.com/DevDevil/laravel-queue-governor/internal/cli"
)

func newStatusCmd() *cobra.Command {
	var socketPath string

	cmd := &cobra.Command{
		Use:   "status",
		Short: "One-shot snapshot of every app's state",
		RunE: func(cmd *cobra.Command, args []string) error {
			snap, err := cli.FetchStatus(socketPath)
			if err != nil {
				return err
			}
			cli.RenderStatusTable(cmd.OutOrStdout(), snap)
			return nil
		},
	}

	cmd.Flags().StringVar(&socketPath, "socket", "/run/lqg/lqg.sock", "path to the daemon's unix socket")
	return cmd
}
