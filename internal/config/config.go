package config

import "github.com/spf13/cobra"

const (
	ApiEndpointFlag      = "api-endpoint"
	ApiEndpointDefault   = "https://api.crusoecloud.com/v1alpha5"
	SocketAddressFlag    = "socket-address"
	SocketAddressDefault = "unix:/tmp/csi.sock"
	ServicesFlag         = "services"
)

// AddFlags attaches the CLI flags the CSI Driver needs to the provided command.
func AddFlags(cmd *cobra.Command) {
	cmd.Flags().String(ApiEndpointFlag, ApiEndpointDefault,
		"Crusoe API Endpoint")
	cmd.Flags().String(SocketAddressFlag, SocketAddressDefault,
		"Socket which the gRPC server will listen on")
	cmd.Flags().StringSlice(ServicesFlag, []string{},
		"CSI Driver services to return")
}
