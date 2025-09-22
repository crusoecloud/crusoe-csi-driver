package internal

import (
	"context"
	"errors"
	"fmt"
	"os"

	"github.com/crusoecloud/crusoe-csi-driver/internal/controller"
	"github.com/crusoecloud/crusoe-csi-driver/internal/node/fs"
	"github.com/crusoecloud/crusoe-csi-driver/internal/node/ssd"

	"github.com/crusoecloud/crusoe-csi-driver/internal/common"

	"k8s.io/mount-utils"
	"k8s.io/utils/exec"

	"github.com/crusoecloud/crusoe-csi-driver/internal/identity"

	"github.com/container-storage-interface/spec/lib/go/csi"
	"github.com/spf13/viper"
	"google.golang.org/grpc"
	"k8s.io/klog/v2"

	crusoeapi "github.com/crusoecloud/client-go/swagger/v1alpha5"
)

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
