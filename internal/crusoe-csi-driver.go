package internal

import (
	"context"
	"fmt"
	crusoeapi "github.com/crusoecloud/client-go/swagger/v1alpha5"
	"github.com/crusoecloud/crusoe-csi-driver/internal/driver"
	"github.com/spf13/cobra"
	"google.golang.org/grpc"
	"net"
	"net/url"
	"os"
	"os/signal"
	"sync"
	"syscall"
)

const apiEndpointFlag = "api-endpoint"
const apiEndpointDefault = "https://api.crusoecloud.site/v1alpha5"
const socketAddressFlag = "socket-address"
const socketAddressDefault = "unix:/tmp/csi.sock"
const servicesFlag = "services"

// AddFlags attaches the CLI flags the CSI Driver needs to the provided command.
func AddFlags(cmd *cobra.Command) {
	cmd.Flags().String(apiEndpointFlag, apiEndpointDefault,
		"Crusoe API Endpoint")
	cmd.Flags().String(socketAddressFlag, socketAddressDefault,
		"Socket which the gRPC server will listen on")
	cmd.Flags().StringSlice(servicesFlag, []string{},
		"CSI Driver services to return")
}

// TODO: flags we need
type service interface {
	Init(apiClient *crusoeapi.APIClient, driver *driver.DriverConfig) error
	RegisterServer(srv *grpc.Server) error
}

// RunDriver starts up and runs the Crusoe Cloud CSI Driver.
func RunDriver(cmd *cobra.Command, _ /*args*/ []string) error {
	// Listen for interrupt signals.
	interrupt := make(chan os.Signal, 1)
	// Ctrl-C
	signal.Notify(interrupt, os.Interrupt)
	// this is what docker sends when shutting down a container
	signal.Notify(interrupt, syscall.SIGTERM)
	var wg sync.WaitGroup
	wg.Add(1)
	ctx, cancel := context.WithCancel(context.Background())
	go func() {
		select {
		case <-ctx.Done():
			return

		case <-interrupt:
			wg.Done()
			cancel()
		}
	}()

	accessKey, err := driver.GetCrusoeAccessKey()
	if err != nil {
		return err
	}
	secretKey, err := driver.GetCrusoeSecretKey()
	if err != nil {
		return err
	}

	services, err := cmd.Flags().GetStringSlice(servicesFlag)
	if err != nil {
		return err
	}
	socketAddress, err := cmd.Flags().GetString(socketAddressFlag)
	if err != nil {
		return err
	}
	apiEndpoint, err := cmd.Flags().GetString(apiEndpointFlag)
	if err != nil {
		return err
	}

	// get endpoint from flags
	endpointURL, err := url.Parse(socketAddress)
	if err != nil {
		return err
	}

	listener, listenErr := net.Listen(endpointURL.Scheme, endpointURL.Path)
	if listenErr != nil {
		return listenErr
	}

	srv := grpc.NewServer()

	grpcServers := []service{
		driver.NewIdentityServer(),
	}
	for _, grpcService := range services {
		switch grpcService {
		case "identity":
			grpcServers = append(grpcServers, driver.NewIdentityServer())
		case "controller":
			grpcServers = append(grpcServers, driver.NewControllerServer())
		case "node":
			grpcServers = append(grpcServers, driver.NewNodeServer())
		default:
			return fmt.Errorf("received unknown service type: %s", grpcService)
		}

	}

	// TODO:
	// - Get version from Docker
	apiClient := driver.NewAPIClient(apiEndpoint, accessKey, secretKey,
		fmt.Sprintf("%s/%s", driver.GetVendorName(), driver.GetVendorVersion()))

	instanceID, projectID, location, err := driver.GetInstanceID(ctx, apiClient)
	if err != nil {
		return err
	}
	crusoeDriver := &driver.DriverConfig{
		VendorName:    driver.GetVendorName(),
		VendorVersion: driver.GetVendorVersion(),
		NodeID:        instanceID,
		NodeProject:   projectID,
		NodeLocation:  location,
	}

	// Initialize gRPC services and register with the gRPC servers
	for _, server := range grpcServers {
		err := server.Init(apiClient, crusoeDriver)
		if err != nil {
			return err
		}

		err = server.RegisterServer(srv)
		if err != nil {
			return err
		}
	}

	go func() {
		err = srv.Serve(listener)
	}()

	wg.Wait()

	srv.GracefulStop()

	return nil
}
