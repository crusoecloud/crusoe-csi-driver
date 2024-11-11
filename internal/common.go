package internal

import (
	"context"
	"crusoe-csi-driver/internal/crusoe"
	"crusoe-csi-driver/internal/identity"
	"errors"
	"fmt"
	"github.com/antihax/optional"
	"github.com/container-storage-interface/spec/lib/go/csi"
	"github.com/spf13/cobra"
	"github.com/spf13/viper"
	"google.golang.org/grpc"
	"io/fs"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/rest"
	"k8s.io/klog/v2"
	"net"
	"net/url"
	"os"
	"os/signal"
	"sync"
	"syscall"

	crusoeapi "github.com/crusoecloud/client-go/swagger/v1alpha5"
)

var commonCapabilities = []*csi.PluginCapability{
	{
		Type: &csi.PluginCapability_VolumeExpansion_{
			VolumeExpansion: &csi.PluginCapability_VolumeExpansion{
				Type: csi.PluginCapability_VolumeExpansion_OFFLINE,
			},
		},
	},
	{
		Type: &csi.PluginCapability_Service_{
			Service: &csi.PluginCapability_Service{
				Type: csi.PluginCapability_Service_VOLUME_ACCESSIBILITY_CONSTRAINTS,
			},
		},
	},
}

const (
	projectIDEnvKey   = "CRUSOE_PROJECT_ID"
	projectIDLabelKey = "crusoe.ai/project.id"
)

const (
	SSDPluginName    = "crusoe-csi-ssd"
	SSDPluginVersion = "0.1.0"
)

var (
	errUnableToGetOpRes = errors.New("failed to get result of operation")
	// fallback error presented to the user in unexpected situations.
	errUnexpected = errors.New("an unexpected error occurred, please try again, and if the problem persists, " +
		"contact support@crusoecloud.com")
	errProjectIDNotFound = fmt.Errorf("project ID not found in %s env var or %s node label",
		projectIDEnvKey, projectIDLabelKey)
)

func interruptHandler() (*sync.WaitGroup, context.Context) {
	// Handle interrupts
	interrupt := make(chan os.Signal, 1)
	signal.Notify(interrupt, os.Interrupt)
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

	return &wg, ctx
}

func getHostInstance(ctx context.Context) (*crusoeapi.InstanceV1Alpha5, error) {
	crusoeClient := NewCrusoeClient(
		viper.GetString("crusoe-api-endpoint"),
		viper.GetString("crusoe-csi-access-key"),
		viper.GetString("crusoe-csi-secret-key"),
		"crusoe-csi-driver/0.0.1",
	)

	nodeName := viper.GetString("node-name")

	var projectID string

	projectID = viper.GetString("crusoe-project-id")
	if projectID == "" {
		var ok bool
		kubeClientConfig, err := rest.InClusterConfig()
		if err != nil {
			return nil, fmt.Errorf("could not get kube client config: %w", err)
		}

		kubeClient, err := kubernetes.NewForConfig(kubeClientConfig)
		if err != nil {
			return nil, fmt.Errorf("could not get kube client: %w", err)
		}
		node, nodeFetchErr := kubeClient.CoreV1().Nodes().Get(ctx, nodeName, metav1.GetOptions{})
		if nodeFetchErr != nil {
			return nil, fmt.Errorf("could not fetch current node with kube client: %w", err)
		}

		projectID, ok = node.Labels[projectIDLabelKey]
		if !ok {
			return nil, errProjectIDNotFound
		}
	}

	instances, _, err := crusoeClient.VMsApi.ListInstances(ctx, projectID,
		&crusoeapi.VMsApiListInstancesOpts{
			Names: optional.NewString(nodeName),
		})
	if err != nil {
		return nil, fmt.Errorf("failed to list instances: %w", err)
	}

	if len(instances.Items) == 0 {
		return nil, fmt.Errorf("could not find instance with name '%s'", nodeName)
	} else if len(instances.Items) > 1 {
		return nil, fmt.Errorf("found multiple instances with name '%s'", nodeName)
	}

	return &instances.Items[0], nil
}

func listen() (net.Listener, error) {
	ep, err := url.Parse(viper.GetString("socket-address"))

	if err != nil {
		return nil, err
	}

	if ep.Scheme == "unix" {
		removeErr := os.Remove(ep.Path)
		if removeErr != nil {
			if !errors.Is(removeErr, fs.ErrNotExist) {
				return nil, fmt.Errorf("failed to remove socket file %s: %w", ep.Path, removeErr)
			}
		}
	}
	listener, listenErr := net.Listen(ep.Scheme, ep.Path)
	if listenErr != nil {
		return nil, fmt.Errorf("failed to start listener on provided socket url: %w", listenErr)
	}

	return listener, nil
}

func RunMain(_ *cobra.Command, _ []string) error {
	wg, ctx := interruptHandler()

	_ = ctx
	srv := grpc.NewServer()

	hostInstance, err := getHostInstance(ctx)
	if err != nil {
		return fmt.Errorf("failed to get host instance: %w", err)
	}

	serveIdentity := false
	serveController := false
	serveNode := false

	for _, s := range Services {
		switch s {
		case ServiceTypeIdentity:
			{
				serveIdentity = true
			}
		case ServiceTypeController:
			{
				serveController = true
			}
		case ServiceTypeNode:
			{
				serveNode = true
			}
		}
	}

	if serveIdentity {
		capabilities := commonCapabilities
		if serveController {
			capabilities = append(capabilities, &csi.PluginCapability{
				Type: &csi.PluginCapability_Service_{
					Service: &csi.PluginCapability_Service{
						Type: csi.PluginCapability_Service_CONTROLLER_SERVICE,
					},
				},
			})
		}
		// TODO: Support shared FS
		csi.RegisterIdentityServer(srv, &identity.Service{Capabilities: capabilities,
			PluginName: SSDPluginName, PluginVersion: SSDPluginVersion})
	}

	if serveController {
		csi.RegisterControllerServer(srv, &crusoe.DefaultController{
			CrusoeClient: NewCrusoeClient(
				viper.GetString(CrusoeAPIEndpointFlag),
				viper.GetString(CrusoeAccessKeyFlag),
				viper.GetString(CrusoeSecretKeyFlag),
				// TODO: Support shared FS
				fmt.Sprintf("%s/%s", SSDPluginName, SSDPluginName),
			),
			HostInstance: hostInstance,
		})
	}

	// TODO: add node server
	_ = serveNode
	//if serveNode {
	//	//csi.RegisterNodeServer(srv, &node.Service{})
	//}

	listener, err := listen()

	if err != nil {
		return err
	}

	klog.Infof("Listening on %s", listener.Addr())

	go func() {
		_ = srv.Serve(listener)
	}()

	wg.Wait()

	srv.GracefulStop()

	return nil
}
