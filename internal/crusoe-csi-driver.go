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

const DefaultApiEndpoint = "https://api.crusoecloud.site/v1alpha5"
const DefaultUnixSocket = "unix:/tmp/csi.sock"

// TODO: flags we need
// - endpoint
// - run controller?
// - run node?
// - run identity
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

	// get endpoint from flags
	endpoint := DefaultUnixSocket
	endpointURL, err := url.Parse(endpoint)
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

	crusoeDriver := &driver.DriverConfig{
		VendorName:    driver.GetVendorName(),
		VendorVersion: driver.GetVendorVersion(),
	}

	// TODO:
	// - Get version from Docker
	// - set default API endpoint
	apiClient := driver.NewAPIClient(DefaultApiEndpoint, accessKey, secretKey,
		fmt.Sprintf("%s/%s", crusoeDriver.GetName(), crusoeDriver.GetVendorVersion()))

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

	// TODO: what other clean up needs to be done?
	srv.GracefulStop()

	return nil
}
