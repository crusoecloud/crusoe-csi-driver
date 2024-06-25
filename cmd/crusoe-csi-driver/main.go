package main

import (
	"fmt"
	"os"

	"github.com/spf13/cobra"

	"github.com/crusoecloud/crusoe-csi-driver/internal"
	"github.com/crusoecloud/crusoe-csi-driver/internal/config"
)

// Start executing the Crusoe CSI driver.
func main() {
	rootCmd := &cobra.Command{
		Use:   "crusoe-csi-driver",
		Short: "Crusoe Container Storage Interface (CSI) driver",
		Args:  cobra.NoArgs,
		RunE:  internal.RunDriver,
	}
	config.AddFlags(rootCmd)
	if err := rootCmd.Execute(); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}
