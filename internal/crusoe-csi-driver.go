package internal

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"net"
	"net/url"
	"os"
	"os/signal"
	"sync"
	"syscall"

	"github.com/spf13/cobra"
	"google.golang.org/grpc"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/rest"
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

var (
	errAccessKeyEmpty     = errors.New("access key is empty")
	errSecretKeyEmpty     = errors.New("secret key is empty")
	errNoServicesProvided = errors.New("cannot initialize CSI driver with no services")
)

type service interface {
	Init(apiClient *crusoeapi.APIClient, driver *driver.Config, services []driver.Service) error
	RegisterServer(srv *grpc.Server) error
}

// RunDriver starts up and runs the Crusoe Cloud CSI Driver.
//
//nolint:funlen,cyclop // a lot statements here because all set up is done here, already factored
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

	requestedServices, accessKey, secretKey, socketAddress, apiEndpoint, parseErr := parseAndValidateArguments(cmd)
	if parseErr != nil {
		return fmt.Errorf("failed to parse arguments: %w", parseErr)
	}

	// get endpoint from flags
	endpointURL, err := url.Parse(socketAddress)
	if err != nil {
		return fmt.Errorf("failed to parse socket address (%s): %w", endpointURL, err)
	}

	listener, listenErr := startListener(endpointURL)
	if listenErr != nil {
		return fmt.Errorf("failed to start listener on provided socket url: %w", listenErr)
	}

	klog.Infof("Started listener on: %s (scheme: %s)", endpointURL.Path, endpointURL.Scheme)

	srv := grpc.NewServer()

	var grpcServers []service
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
		return errNoServicesProvided
	}

	apiClient := driver.NewAPIClient(apiEndpoint, accessKey, secretKey,
		fmt.Sprintf("%s/%s", driver.GetVendorName(), driver.GetVendorVersion()))

	kubeClientConfig, err := rest.InClusterConfig()
	if err != nil {
		return fmt.Errorf("could not get kube client config: %w", err)
	}

	kubeClient, err := kubernetes.NewForConfig(kubeClientConfig)
	if err != nil {
		return fmt.Errorf("could not get kube client: %w", err)
	}

	instanceID, projectID, location, err := driver.GetInstanceInfo(ctx, apiClient, kubeClient)
	if err != nil {
		return fmt.Errorf("failed to get instance id of node: %w", err)
	}
	klog.Infof("Found instance id of node: %s", instanceID)

	crusoeDriver := &driver.Config{
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
			return fmt.Errorf("failed to initialize server: %w", initErr)
		}

		registerErr := server.RegisterServer(srv)
		if registerErr != nil {
			return fmt.Errorf("failed to register server: %w", registerErr)
		}
	}

	go func() {
		err = srv.Serve(listener)
	}()

	wg.Wait()

	srv.GracefulStop()

	return nil
}

//nolint:gocritic,cyclop // there are a lot of returned variables here because we parse all args here
func parseAndValidateArguments(cmd *cobra.Command) (
	requestedServices []driver.Service,
	accessKey, secretKey, socketAddress, apiEndpoint string,
	err error,
) {
	accessKey = driver.GetCrusoeAccessKey()
	if accessKey == "" {
		return nil, "", "", "", "", errAccessKeyEmpty
	}
	secretKey = driver.GetCrusoeSecretKey()
	if secretKey == "" {
		return nil, "", "", "", "", errSecretKeyEmpty
	}

	services, err := cmd.Flags().GetStringSlice(config.ServicesFlag)
	if err != nil {
		return nil, "", "", "", "",
			fmt.Errorf("failed to get services flag: %w", err)
	}
	requestedServices = []driver.Service{}
	for _, reqService := range services {
		switch reqService {
		case identityArg:
			requestedServices = append(requestedServices, driver.IdentityService)
		case controllerArg:
			requestedServices = append(requestedServices, driver.ControllerService)
		case nodeArg:
			requestedServices = append(requestedServices, driver.NodeService)
		default:
			//nolint:goerr113 // use dynamic errors for more informative error handling
			return nil, "", "", "", "",
				fmt.Errorf("received unknown service type: %s", reqService)
		}
	}
	socketAddress, err = cmd.Flags().GetString(config.SocketAddressFlag)
	if err != nil {
		return nil, "", "", "", "",
			fmt.Errorf("failed to get socket address flag: %w", err)
	}
	apiEndpoint, err = cmd.Flags().GetString(config.APIEndpointFlag)
	if err != nil {
		return nil, "", "", "", "",
			fmt.Errorf("failed to get api endpoint flag: %w", err)
	}

	return requestedServices, accessKey, secretKey, socketAddress, apiEndpoint, nil
}

func startListener(endpointURL *url.URL) (net.Listener, error) {
	removeErr := os.Remove(endpointURL.Path)
	if removeErr != nil {
		if !errors.Is(removeErr, fs.ErrNotExist) {
			return nil, fmt.Errorf("failed to remove socket file %s: %w", endpointURL.Path, removeErr)
		}
	}
	listener, listenErr := net.Listen(endpointURL.Scheme, endpointURL.Path)
	if listenErr != nil {
		return nil, fmt.Errorf("failed to start listener on provided socket url: %w", listenErr)
	}

	return listener, nil
}
