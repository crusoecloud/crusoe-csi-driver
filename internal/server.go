package internal

import (
	"context"
	"errors"
	"fmt"
	ioFs "io/fs"
	"net"
	"net/http"
	"net/url"
	"os"
	"strings"
	"time"

	"github.com/crusoecloud/crusoe-csi-driver/internal/node/fs"
	"github.com/crusoecloud/crusoe-csi-driver/internal/node/ssd"
	"github.com/google/uuid"

	"github.com/crusoecloud/crusoe-csi-driver/internal/controller"

	"github.com/crusoecloud/crusoe-csi-driver/internal/common"

	"k8s.io/mount-utils"
	"k8s.io/utils/exec"

	"github.com/crusoecloud/crusoe-csi-driver/internal/crusoe"
	"github.com/crusoecloud/crusoe-csi-driver/internal/identity"

	"github.com/antihax/optional"
	"github.com/container-storage-interface/spec/lib/go/csi"
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

	gracefulTimeoutDuration = 10 * time.Second
)

var (
	errInstanceNotFound  = errors.New("instance not found")
	errMultipleInstances = errors.New("multiple instances found")
	errVMIDReadFailed    = fmt.Errorf("failed to read %s for VM ID", vmIDFilePath)
	errVMIDParseFailed   = fmt.Errorf("failed to parse %s for VM ID", vmIDFilePath)
	errProjectIDNotFound = fmt.Errorf("project ID not found in %s env var or %s node label",
		projectIDEnvKey, projectIDLabelKey)
)

//nolint:cyclop // function is already fairly clean
func getHostInstance(ctx context.Context) (*crusoeapi.InstanceV1Alpha5, error) {
	crusoeClient := crusoe.NewCrusoeClient(
		viper.GetString(CrusoeAPIEndpointFlag),
		viper.GetString(CrusoeAccessKeyFlag),
		viper.GetString(CrusoeSecretKeyFlag),
		fmt.Sprintf("%s/%s", common.PluginName, common.PluginVersion),
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
			if !errors.Is(removeErr, ioFs.ErrNotExist) {
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

func newCrusoeClientWithViperConfig() *crusoeapi.APIClient {
	return crusoe.NewCrusoeClient(
		viper.GetString(CrusoeAPIEndpointFlag),
		viper.GetString(CrusoeAccessKeyFlag),
		viper.GetString(CrusoeSecretKeyFlag),
		common.UserAgent(),
	)
}

func newCrusoeHTTPClientWithViperConfig() *http.Client {
	return crusoe.NewCrusoeHTTPClient(viper.GetString(CrusoeAccessKeyFlag), viper.GetString(CrusoeSecretKeyFlag))
}

func registerIdentity(grpcServer *grpc.Server, serveController bool) {
	capabilities := common.BaseIdentityCapabilities

	if serveController {
		capabilities = append(capabilities, &common.PluginCapabilityControllerService)
	}
	switch common.PluginDiskType {
	case common.DiskTypeFS:
		capabilities = append(capabilities, &common.PluginCapabilityVolumeExpansionOnline)
	case common.DiskTypeSSD:
		capabilities = append(capabilities, &common.PluginCapabilityVolumeExpansionOffline)
	default:
		// Switch is intended to be exhaustive, reaching this case is a bug
		panic(fmt.Sprintf(
			"Switch is intended to be exhaustive, %s is not a valid switch case", common.PluginDiskType))
	}
	csi.RegisterIdentityServer(grpcServer, &identity.Service{
		Capabilities:  capabilities,
		PluginName:    common.PluginName,
		PluginVersion: common.PluginVersion,
	})
}

func registerController(grpcServer *grpc.Server, hostInstance *crusoeapi.InstanceV1Alpha5) {
	capabilities := common.BaseControllerCapabilities

	csi.RegisterControllerServer(grpcServer, &controller.DefaultController{
		CrusoeClient:  newCrusoeClientWithViperConfig(),
		HostInstance:  hostInstance,
		Capabilities:  capabilities,
		DiskType:      common.PluginDiskType,
		PluginName:    common.PluginName,
		PluginVersion: common.PluginVersion,
	})
}

func registerNode(grpcServer *grpc.Server, hostInstance *crusoeapi.InstanceV1Alpha5) {
	// TODO: Add NodeExpandVolume capability once SSD online expansion is supported upstream
	capabilities := common.BaseNodeCapabilities
	var maxVolumesPerNode int64
	var nodeServer csi.NodeServer

	switch common.PluginDiskType {
	case common.DiskTypeSSD:
		maxVolumesPerNode = common.MaxSSDVolumesPerNode - 1 // Subtract 1 to allow for the OS/boot disk
		nodeServer = &ssd.Node{
			CrusoeClient:      newCrusoeClientWithViperConfig(),
			CrusoeHTTPClient:  newCrusoeHTTPClientWithViperConfig(),
			CrusoeAPIEndpoint: viper.GetString(CrusoeAPIEndpointFlag),
			HostInstance:      hostInstance,
			Capabilities:      capabilities,
			MaxVolumesPerNode: maxVolumesPerNode,
			Mounter:           mount.NewSafeFormatAndMount(mount.New(""), exec.New()),
			Resizer:           mount.NewResizeFs(exec.New()),
			DiskType:          common.PluginDiskType,
			PluginName:        common.PluginName,
			PluginVersion:     common.PluginVersion,
		}
	case common.DiskTypeFS:
		maxVolumesPerNode = common.MaxFSVolumesPerNode
		nodeServer = &fs.Node{
			CrusoeClient:      newCrusoeClientWithViperConfig(),
			CrusoeHTTPClient:  newCrusoeHTTPClientWithViperConfig(),
			CrusoeAPIEndpoint: viper.GetString(CrusoeAPIEndpointFlag),
			HostInstance:      hostInstance,
			Capabilities:      capabilities,
			MaxVolumesPerNode: maxVolumesPerNode,
			Mounter:           mount.NewSafeFormatAndMount(mount.New(""), exec.New()),
			Resizer:           mount.NewResizeFs(exec.New()),
			DiskType:          common.PluginDiskType,
			PluginName:        common.PluginName,
			PluginVersion:     common.PluginVersion,
		}
	default:
		// Switch is intended to be exhaustive, reaching this case is a bug
		panic(fmt.Sprintf(
			"Switch is intended to be exhaustive, %s is not a valid switch case", common.PluginDiskType))
	}

	csi.RegisterNodeServer(grpcServer, nodeServer)
}

func registerServices(grpcServer *grpc.Server, hostInstance *crusoeapi.InstanceV1Alpha5) {
	serveIdentity := false
	serveController := false
	serveNode := false

	for _, service := range Services {
		switch service {
		case ServiceTypeIdentity:
			serveIdentity = true
		case ServiceTypeController:
			serveController = true
		case ServiceTypeNode:
			serveNode = true
		default:
			panic(fmt.Sprintf("Switch is intended to be exhaustive, %v is not a valid switch case", service))
		}
	}

	if serveIdentity {
		registerIdentity(grpcServer, serveController)
	}

	if serveController {
		registerController(grpcServer, hostInstance)
	}

	if serveNode {
		registerNode(grpcServer, hostInstance)
	}
}

func gracefulStopWithTimeout(grpcServer *grpc.Server, timeout time.Duration) {
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()
	doneCh := make(chan struct{}, 1)

	go func() {
		grpcServer.GracefulStop()
		close(doneCh)
	}()

	select {
	case <-doneCh:
		break
	case <-ctx.Done():
		klog.Infof("Graceful stop timeout exceeded, forcing stop")
		grpcServer.Stop()
	}
}

func Serve(rootCtx context.Context, rootCtxCancel context.CancelFunc, interruptChan <-chan os.Signal) error {
	hostInstance, err := getHostInstance(rootCtx)
	if err != nil {
		return fmt.Errorf("failed to get host instance: %w", err)
	}

	klog.Infof("Crusoe host instance ID: %v", hostInstance.Id)

	srv := grpc.NewServer(grpc.ConnectionTimeout(gracefulTimeoutDuration))
	registerServices(srv, hostInstance)
	listener, err := listen()
	if err != nil {
		return err
	}

	klog.Infof("Listening on socket: %s", listener.Addr())
	gRPCErrChan := make(chan error, 1)

	go func() {
		klog.Infof("Starting driver %s version %s", common.PluginName, common.PluginVersion)
		err = srv.Serve(listener)
		gRPCErrChan <- err
	}()

	select {
	case <-rootCtx.Done():
		klog.Infof("Root context cancelled")
	case <-interruptChan:
		klog.Infof("Received interrupt signal, shutting down")
		rootCtxCancel()
	case gRPCErr := <-gRPCErrChan:
		rootCtxCancel()
		if gRPCErr != nil {
			if errors.Is(gRPCErr, grpc.ErrServerStopped) {
				klog.Infof("gRPC server stopped")
				gracefulStopWithTimeout(srv, gracefulTimeoutDuration)
				klog.Infof("Stopped driver %s version %s", common.PluginName, common.PluginVersion)

				return nil
			}
		}

		// An error has occurred, attempt to gracefully stop the gRPC server
		klog.Errorf("Received error from gRPC server: %s", gRPCErr)
		gracefulStopWithTimeout(srv, gracefulTimeoutDuration)
		klog.Infof("Stopped driver %s version %s", common.PluginName, common.PluginVersion)

		return gRPCErr
	}

	// Normal termination flow
	klog.Infof("Gracefully stopping driver %s version %s", common.PluginName, common.PluginVersion)
	gracefulStopWithTimeout(srv, gracefulTimeoutDuration)
	klog.Infof("Stopped driver %s version %s", common.PluginName, common.PluginVersion)

	return nil
}
