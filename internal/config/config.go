package config

import "github.com/spf13/cobra"

const ApiEndpointFlag = "api-endpoint"
const ApiEndpointDefault = "https://api.crusoecloud.com/v1alpha5"
const SocketAddressFlag = "socket-address"
const SocketAddressDefault = "unix:/tmp/csi.sock"
const ServicesFlag = "services"

// AddFlags attaches the CLI flags the CSI Driver needs to the provided command.
func AddFlags(cmd *cobra.Command) {
	cmd.Flags().String(ApiEndpointFlag, ApiEndpointDefault,
		"Crusoe API Endpoint")
	cmd.Flags().String(SocketAddressFlag, SocketAddressDefault,
		"Socket which the gRPC server will listen on")
	cmd.Flags().StringSlice(ServicesFlag, []string{},
		"CSI Driver services to return")
}
