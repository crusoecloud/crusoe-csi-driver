package main

import (
	"fmt"
	"github.com/crusoecloud/crusoe-csi-driver/internal"
	"github.com/spf13/cobra"
	"os"
)

// Start executing the Crusoe CSI driver.
func main() {
	rootCmd := &cobra.Command{
		Use:   "crusoe-csi-driver",
		Short: "Crusoe Container Storage Interface (CSI) driver",
		Args:  cobra.NoArgs,
		RunE:  internal.RunDriver,
	}
	internal.AddFlags(rootCmd)
	if err := rootCmd.Execute(); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}
