package main

import (
	"crusoe-csi-driver/internal"
	"fmt"
	"os"
	"strings"

	"github.com/spf13/cobra"
	"github.com/spf13/viper"
	"github.com/thediveo/enumflag/v2"
)

// rootCmd represents the base command when called without any subcommands
var rootCmd = &cobra.Command{
	Use:   "crusoe-csi-rewrite",
	Short: "A brief description of your application",
	Long: `A longer description that spans multiple lines and likely contains
examples and usage of using your application. For example:

Cobra is a CLI library for Go that empowers applications.
This application is a tool to generate the needed files
to quickly create a Cobra application.`,
	RunE: internal.RunMain,
}

// Execute adds all child commands to the root command and sets flags appropriately.
// This is called by main.main(). It only needs to happen once to the rootCmd.
func Execute() {
	err := rootCmd.Execute()
	if err != nil {
		os.Exit(1)
	}
}

func init() {
	var err error
	viper.AutomaticEnv()

	// Use underscores in env var names
	replacer := strings.NewReplacer("-", "_")
	viper.SetEnvKeyReplacer(replacer)

	rootCmd.Flags().String(internal.CrusoeAPIEndpointFlag, internal.CrusoeAPIEndpointDefault, "help for api endpoint")
	rootCmd.Flags().String(internal.CrusoeAccessKeyFlag, "", "help for access key")
	rootCmd.Flags().String(internal.CrusoeSecretKeyFlag, "", "help for secret key")
	rootCmd.Flags().String(internal.CrusoeProjectIDFlag, "", "help for project id")
	rootCmd.Flags().Var(enumflag.New(&internal.SelectedCSIDriverType, "driver", internal.CSIDriverTypeNames, true), "driver", "help for driver type")
	rootCmd.Flags().Var(enumflag.NewSlice(&internal.Services, "service", internal.ServiceTypeNames, true), "service", "help for services")
	rootCmd.Flags().String(internal.NodeNameFlag, "", "help for kubernetes node name")
	rootCmd.Flags().String(internal.SocketAddressFlag, internal.SocketAddressDefault, "help for socket address")

	err = viper.BindPFlags(rootCmd.Flags())

	if err != nil {
		_, _ = fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

func main() {
	Execute()
}
