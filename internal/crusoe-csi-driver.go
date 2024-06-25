package internal

import (
	"context"
	"fmt"
	"net"
	"net/url"
	"os"
	"os/signal"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/spf13/cobra"
	"google.golang.org/grpc"
	"k8s.io/klog/v2"

	crusoeapi "github.com/crusoecloud/client-go/swagger/v1alpha5"
	"github.com/crusoecloud/crusoe-csi-driver/internal/config"
	"github.com/crusoecloud/crusoe-csi-driver/internal/driver"
)

const (
	maxRetries           = 10
	retryIntervalSeconds = 5
	identityArg          = "identity"
	controllerArg        = "controller"
	nodeArg              = "node"
)

type service interface {
	Init(apiClient *crusoeapi.APIClient, driver *driver.DriverConfig, services []driver.Service) error
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

	accessKey := driver.GetCrusoeAccessKey()
	if accessKey == "" {
		return fmt.Errorf("access key is empty")
	}
	secretKey := driver.GetCrusoeSecretKey()
	if secretKey == "" {
		return fmt.Errorf("secret key is empty")
	}

	services, err := cmd.Flags().GetStringSlice(config.ServicesFlag)
	if err != nil {
		return err
	}
	requestedServices := []driver.Service{}
	for _, reqService := range services {
		switch reqService {
		case identityArg:
			requestedServices = append(requestedServices, driver.IdentityService)
		case controllerArg:
			requestedServices = append(requestedServices, driver.ControllerService)
		case nodeArg:
			requestedServices = append(requestedServices, driver.NodeService)
		default:
			return fmt.Errorf("received unknown service type: %s", reqService)
		}
	}
	socketAddress, err := cmd.Flags().GetString(config.SocketAddressFlag)
	if err != nil {
		return err
	}
	apiEndpoint, err := cmd.Flags().GetString(config.ApiEndpointFlag)
	if err != nil {
		return err
	}

	// get endpoint from flags
	endpointURL, err := url.Parse(socketAddress)
	if err != nil {
		return err
	}

	var listener net.Listener

	tryCount := 0
	for {
		tryListener, listenErr := net.Listen(endpointURL.Scheme, endpointURL.Path)
		if listenErr != nil {
			// if old pods are being terminated, they might not have closed the gRPC server listening on the socket
			// let's wait and try again
			if strings.Contains(listenErr.Error(), "bind: address already in use") {
				klog.Infof("Address (%s//%s) already in use, retrying...", endpointURL.Scheme, endpointURL.Path)
				time.Sleep(retryIntervalSeconds * time.Second)

				if tryCount == maxRetries {
					return listenErr
				}
				tryCount++

				continue
			}

			return listenErr
		}
		listener = tryListener

		break
	}

	klog.Infof("Started listener on: %s (scheme: %s)", endpointURL.Path, endpointURL.Scheme)

	srv := grpc.NewServer()

	grpcServers := []service{}
	for _, grpcSrvc := range requestedServices {
		switch grpcSrvc {
		case driver.ControllerService:
			grpcServers = append(grpcServers, driver.NewControllerServer())
		case driver.NodeService:
			grpcServers = append(grpcServers, driver.NewNodeServer())
		case driver.IdentityService:
			grpcServers = append(grpcServers, driver.NewIdentityServer())
		}
	}

	if len(grpcServers) == 0 {
		return fmt.Errorf("cannot initialize CSI driver with no services")
	}

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
		initErr := server.Init(apiClient, crusoeDriver, requestedServices)
		if initErr != nil {
			return initErr
		}

		registerErr := server.RegisterServer(srv)
		if registerErr != nil {
			return registerErr
		}
	}

	go func() {
		err = srv.Serve(listener)
	}()

	wg.Wait()

	srv.GracefulStop()

	return nil
}
