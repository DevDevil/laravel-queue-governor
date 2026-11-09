package main

import (
	"fmt"
	"os"

	"github.com/spf13/cobra"
)

func newValidateCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "validate [config]",
		Short: "Parse and validate a config file, exit non-zero on error",
		Args:  cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			path := "/etc/lqg/config.yaml"
			if len(args) == 1 {
				path = args[0]
			}

			if _, err := loadAndValidate(path, os.Stdout, "warning"); err != nil {
				return err
			}

			fmt.Printf("%s: OK\n", path)
			return nil
		},
	}
}
