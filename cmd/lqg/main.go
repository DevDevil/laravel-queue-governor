package main

import (
	"fmt"
	"os"

	"github.com/spf13/cobra"
)

func main() {
	rootCmd := &cobra.Command{
		Use:   "lqg",
		Short: "lqg — load-aware queue governor for Laravel",
		// A runtime error (daemon unreachable, invalid config) isn't a
		// usage mistake — print just the error, not the flag help.
		SilenceUsage:  true,
		SilenceErrors: true,
	}

	rootCmd.AddCommand(newRunCmd())
	rootCmd.AddCommand(newStatusCmd())
	rootCmd.AddCommand(newWatchCmd())
	rootCmd.AddCommand(newValidateCmd())

	if err := rootCmd.Execute(); err != nil {
		fmt.Fprintln(os.Stderr, "Error:", err)
		os.Exit(1)
	}
}
