package main

import (
	"fmt"
	"os"
	"strings"

	"github.com/crusoecloud/crusoe-csi-driver/internal"

	"github.com/spf13/cobra"
	"github.com/spf13/viper"
	"github.com/thediveo/enumflag/v2"
)

//nolint:gochecknoglobals  // Global command instance
var rootCmd = &cobra.Command{
	Use:          "crusoe-csi-driver",
	Short:        "Crusoe Container Storage Interface (CSI) driver",
	SilenceUsage: true, // Silence usage print if an error occurs
	RunE:         internal.RunMain,
}

// Execute adds all child commands to the root command and sets flags appropriately.
// This is called by main.main(). It only needs to happen once to the rootCmd.
func Execute() {
	err := rootCmd.Execute()
	if err != nil {
		os.Exit(1)
	}
}

func setFlags() {
	var err error
	viper.AutomaticEnv()

	// Use underscores in env var names
	replacer := strings.NewReplacer("-", "_")
	viper.SetEnvKeyReplacer(replacer)

	rootCmd.Flags().String(internal.CrusoeAPIEndpointFlag, internal.CrusoeAPIEndpointDefault, "Crusoe API endpoint")
	rootCmd.Flags().String(internal.CrusoeAccessKeyFlag, "", "Crusoe Access Key")
	rootCmd.Flags().String(internal.CrusoeSecretKeyFlag, "", "Crusoe Secret Key")
	rootCmd.Flags().String(internal.CrusoeProjectIDFlag, "", "Cluster Project ID")
	rootCmd.Flags().Var(
		enumflag.New(&internal.SelectedCSIDriverType,
			internal.CSIDriverTypeFlag,
			internal.CSIDriverTypeNames,
			true),
		internal.CSIDriverTypeFlag,
		"Crusoe CSI Driver type")
	rootCmd.Flags().Var(
		enumflag.NewSlice(&internal.Services,
			internal.ServicesFlag,
			internal.ServiceTypeNames,
			true),
		internal.ServicesFlag,
		"Crusoe CSI Driver services")
	rootCmd.Flags().String(internal.NodeNameFlag, "", "Kubernetes Node Name")
	rootCmd.Flags().String(internal.SocketAddressFlag, internal.SocketAddressDefault, "CSI Socket Address")
	rootCmd.Flags().String(internal.NFSRemotePortsFlag, internal.NFSRemotePortsDefault, "NFS Remote Ports")
	rootCmd.Flags().String(internal.NFSIPFlag, internal.NFSIPDefault, "NFS IP")

	err = viper.BindPFlags(rootCmd.Flags())
	if err != nil {
		_, _ = fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

func main() {
	setFlags()
	Execute()
}
