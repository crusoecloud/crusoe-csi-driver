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
	Use:   "crusoe-csi-driver",
	Short: "Crusoe Container Storage Interface (CSI) driver",
	RunE:  internal.RunMain,
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

	rootCmd.Flags().String(internal.CrusoeAPIEndpointFlag, internal.CrusoeAPIEndpointDefault, "help for api endpoint")
	rootCmd.Flags().String(internal.CrusoeAccessKeyFlag, "", "help for access key")
	rootCmd.Flags().String(internal.CrusoeSecretKeyFlag, "", "help for secret key")
	rootCmd.Flags().String(internal.CrusoeProjectIDFlag, "", "help for project id")
	rootCmd.Flags().Var(
		enumflag.New(&internal.SelectedCSIDriverType,
			internal.CSIDriverTypeFlag,
			internal.CSIDriverTypeNames,
			true),
		internal.CSIDriverTypeFlag,
		"help for driver type")
	rootCmd.Flags().Var(
		enumflag.NewSlice(&internal.Services,
			internal.ServicesFlag,
			internal.ServiceTypeNames,
			true),
		internal.ServicesFlag,
		"help for services")
	rootCmd.Flags().String(internal.NodeNameFlag, "", "help for kubernetes node name")
	rootCmd.Flags().String(internal.SocketAddressFlag, internal.SocketAddressDefault, "help for socket address")

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
