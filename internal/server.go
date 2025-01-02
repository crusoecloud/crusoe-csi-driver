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
	"strings"
	"sync"
	"syscall"

	"github.com/google/uuid"

	"github.com/crusoecloud/crusoe-csi-driver/internal/controller"

	"github.com/crusoecloud/crusoe-csi-driver/internal/common"

	"github.com/crusoecloud/crusoe-csi-driver/internal/node"

	"k8s.io/mount-utils"
	"k8s.io/utils/exec"

	"github.com/crusoecloud/crusoe-csi-driver/internal/crusoe"
	"github.com/crusoecloud/crusoe-csi-driver/internal/identity"

	"github.com/antihax/optional"
	"github.com/container-storage-interface/spec/lib/go/csi"
	"github.com/spf13/cobra"
	"github.com/spf13/viper"
	"google.golang.org/grpc"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/rest"
	"k8s.io/klog/v2"

	crusoeapi "github.com/crusoecloud/client-go/swagger/v1alpha5"
)

const (
	projectIDEnvKey   = "CRUSOE_PROJECT_ID"
	projectIDLabelKey = "crusoe.ai/project.id"

	vmIDFilePath = "/sys/class/dmi/id/product_uuid"
)

var (
	errInstanceNotFound  = errors.New("instance not found")
	errMultipleInstances = errors.New("multiple instances found")
	errVMIDReadFailed    = fmt.Errorf("failed to read %s for VM ID", vmIDFilePath)
	errVMIDParseFailed   = fmt.Errorf("failed to parse %s for VM ID", vmIDFilePath)
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

//nolint:cyclop // function is already fairly clean
func getHostInstance(ctx context.Context) (*crusoeapi.InstanceV1Alpha5, error) {
	crusoeClient := crusoe.NewCrusoeClient(
		viper.GetString(CrusoeAPIEndpointFlag),
		viper.GetString(CrusoeAccessKeyFlag),
		viper.GetString(CrusoeSecretKeyFlag),
		"crusoe-csi-driver/0.0.1",
	)

	vmIDStringByteArray, err := os.ReadFile(vmIDFilePath)
	if err != nil {
		return nil, fmt.Errorf("%w: %w", errVMIDReadFailed, err)
	}

	vmIDString := strings.TrimSpace(string(vmIDStringByteArray))
	_, err = uuid.Parse(vmIDString)
	if err != nil {
		return nil, fmt.Errorf("%w: %w", errVMIDParseFailed, err)
	}

	var projectID string

	projectID = viper.GetString(CrusoeProjectIDFlag)
	if projectID == "" {
		var ok bool
		kubeClientConfig, configErr := rest.InClusterConfig()
		if configErr != nil {
			return nil, fmt.Errorf("could not get kube client config: %w", configErr)
		}

		kubeClient, clientErr := kubernetes.NewForConfig(kubeClientConfig)
		if clientErr != nil {
			return nil, fmt.Errorf("could not get kube client: %w", clientErr)
		}
		hostNode, nodeFetchErr := kubeClient.CoreV1().Nodes().Get(ctx, viper.GetString(NodeNameFlag), metav1.GetOptions{})
		if nodeFetchErr != nil {
			return nil, fmt.Errorf("could not fetch current node with kube client: %w", nodeFetchErr)
		}

		projectID, ok = hostNode.Labels[projectIDLabelKey]
		if !ok {
			return nil, errProjectIDNotFound
		}
	}

	instances, _, err := crusoeClient.VMsApi.ListInstances(ctx, projectID,
		&crusoeapi.VMsApiListInstancesOpts{
			Ids: optional.NewString(vmIDString),
		})
	if err != nil {
		return nil, fmt.Errorf("failed to list instances: %w", err)
	}

	if len(instances.Items) == 0 {
		return nil, fmt.Errorf("%w: %s", errInstanceNotFound, vmIDString)
	} else if len(instances.Items) > 1 {
		return nil, fmt.Errorf("%w: %s", errMultipleInstances, vmIDString)
	}

	return &instances.Items[0], nil
}

func listen() (net.Listener, error) {
	ep, err := url.Parse(viper.GetString(SocketAddressFlag))
	if err != nil {
		return nil, fmt.Errorf("failed to parse socket url: %w", err)
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

//nolint:gocritic // don't combine parameter types
func newCrusoeClientWithViperConfig(pluginName string, pluginVersion string) *crusoeapi.APIClient {
	return crusoe.NewCrusoeClient(
		viper.GetString(CrusoeAPIEndpointFlag),
		viper.GetString(CrusoeAccessKeyFlag),
		viper.GetString(CrusoeSecretKeyFlag),
		fmt.Sprintf("%s/%s", pluginName, pluginVersion),
	)
}

//nolint:funlen,cyclop // server instantiation is long
func registerServices(grpcServer *grpc.Server, hostInstance *crusoeapi.InstanceV1Alpha5) (
	diskType common.DiskType,
	pluginName string,
	pluginVersion string,
) {
	serveIdentity := false
	serveController := false
	serveNode := false

	switch SelectedCSIDriverType {
	case CSIDriverTypeSSD:
		diskType = common.DiskTypeSSD
		pluginName = common.SSDPluginName
		pluginVersion = common.SSDPluginVersion
	case CSIDriverTypeFS:
		diskType = common.DiskTypeFS
		pluginName = common.FSPluginName
		pluginVersion = common.FSPluginVersion
	default:
		panic(fmt.Sprintf("%s is not a valid driver type", viper.GetString(CSIDriverTypeFlag)))
	}

	for _, s := range Services {
		switch s {
		case ServiceTypeIdentity:
			serveIdentity = true
		case ServiceTypeController:
			serveController = true
		case ServiceTypeNode:
			serveNode = true
		}
	}

	if serveIdentity {
		capabilities := common.BaseIdentityCapabilities

		if serveController {
			capabilities = append(capabilities, &common.PluginCapabilityControllerService)
		}
		if diskType == common.DiskTypeFS {
			capabilities = append(capabilities, &common.PluginCapabilityVolumeExpansionOnline)
		}
		if diskType == common.DiskTypeSSD {
			capabilities = append(capabilities, &common.PluginCapabilityVolumeExpansionOffline)
		}

		csi.RegisterIdentityServer(grpcServer, &identity.Service{
			Capabilities:  capabilities,
			PluginName:    pluginName,
			PluginVersion: pluginVersion,
		})
	}

	if serveController {
		capabilities := common.BaseControllerCapabilities

		csi.RegisterControllerServer(grpcServer, &controller.DefaultController{
			CrusoeClient:  newCrusoeClientWithViperConfig(pluginName, pluginVersion),
			HostInstance:  hostInstance,
			Capabilities:  capabilities,
			DiskType:      diskType,
			PluginName:    pluginName,
			PluginVersion: pluginVersion,
		})
	}

	if serveNode {
		capabilities := common.BaseNodeCapabilities

		// TODO: Add NodeExpandVolume capability once SSD online expansion is supported upstream

		csi.RegisterNodeServer(grpcServer, &node.DefaultNode{
			CrusoeClient:  newCrusoeClientWithViperConfig(pluginName, pluginVersion),
			HostInstance:  hostInstance,
			Capabilities:  capabilities,
			Mounter:       mount.NewSafeFormatAndMount(mount.New(""), exec.New()),
			Resizer:       mount.NewResizeFs(exec.New()),
			DiskType:      diskType,
			PluginName:    pluginName,
			PluginVersion: pluginVersion,
		})
	}

	return diskType, pluginName, pluginVersion
}

func RunMain(_ *cobra.Command, _ []string) error {
	wg, ctx := interruptHandler()

	srv := grpc.NewServer()

	hostInstance, err := getHostInstance(ctx)
	if err != nil {
		return fmt.Errorf("failed to get host instance: %w", err)
	}
	klog.Infof("Crusoe host instance ID: %+v", hostInstance.Id)

	diskType, pluginName, pluginVersion := registerServices(srv, hostInstance)
	_ = diskType

	listener, err := listen()
	if err != nil {
		return err
	}

	klog.Infof("Listening on: %s", listener.Addr())

	go func() {
		klog.Infof("Starting driver name: %s version: %s", pluginName, pluginVersion)
		err = srv.Serve(listener)
		if !errors.Is(err, grpc.ErrServerStopped) {
			klog.Errorf("gRPC server stopped: %s", err)
		}
	}()

	wg.Wait()

	srv.GracefulStop()

	return nil
}
