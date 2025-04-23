package internal

import (
	"context"
	"fmt"
	"os"
	"os/signal"
	"syscall"

	"k8s.io/klog/v2"

	"github.com/crusoecloud/crusoe-csi-driver/internal/common"
	"github.com/spf13/cobra"
	"github.com/spf13/viper"
	"github.com/thediveo/enumflag/v2"
)

type ServiceType enumflag.Flag

const (
	ServiceTypeIdentity ServiceType = iota
	ServiceTypeController
	ServiceTypeNode
)

var ServiceTypeNames = map[ServiceType][]string{ //nolint:gochecknoglobals  // can't construct const map
	ServiceTypeIdentity:   {"identity"},
	ServiceTypeController: {"controller"},
	ServiceTypeNode:       {"node"},
}

var Services = []ServiceType{ServiceTypeIdentity} //nolint:gochecknoglobals // flag variable

type CSIDriverType enumflag.Flag

const (
	CSIDriverTypeSSD CSIDriverType = iota
	CSIDriverTypeFS
)

var CSIDriverTypeNames = map[CSIDriverType][]string{ //nolint:gochecknoglobals  // can't construct const map
	CSIDriverTypeSSD: {"ssd"},
	CSIDriverTypeFS:  {"fs"},
}

var SelectedCSIDriverType = CSIDriverTypeSSD //nolint:gochecknoglobals // flag variable

const (
	CrusoeAPIEndpointFlag = "crusoe-api-endpoint"
	CrusoeAccessKeyFlag   = "crusoe-csi-access-key"
	CrusoeSecretKeyFlag   = "crusoe-csi-secret-key" //nolint:gosec // false positive, this is a flag name
	CrusoeProjectIDFlag   = "crusoe-project-id"
	CSIDriverTypeFlag     = "crusoe-csi-driver-type"
	ServicesFlag          = "services"
	NodeNameFlag          = "node-name"
	SocketAddressFlag     = "socket-address"
)

const (
	CrusoeAPIEndpointDefault = "https://api.crusoecloud.com/v1alpha5"
	SocketAddressDefault     = "unix:/tmp/csi.sock"
)

func SetPluginVariables() {
	switch SelectedCSIDriverType {
	case CSIDriverTypeSSD:
		common.PluginName = common.SSDPluginName
		common.PluginDiskType = common.DiskTypeSSD
	case CSIDriverTypeFS:
		common.PluginName = common.FSPluginName
		common.PluginDiskType = common.DiskTypeFS
	default:
		// Switch is intended to be exhaustive, reaching this case is a bug
		panic(fmt.Sprintf(
			"Switch is intended to be exhaustive, %s is not a valid switch case",
			viper.GetString(CSIDriverTypeFlag)))
	}
}

func RunMain(_ *cobra.Command, _ []string) error {
	// Set plugin variables based on driver type flag
	SetPluginVariables()

	// Create root context
	rootCtx, rootCtxCancel := context.WithCancel(context.Background())

	// Handle interrupts
	interruptChan := make(chan os.Signal, 1)
	signal.Notify(interruptChan, os.Interrupt)
	signal.Notify(interruptChan, syscall.SIGTERM)

	klog.Infof("Initializing driver %s %s", common.PluginName, common.PluginVersion)

	// Serve CSI gRPC server
	return Serve(rootCtx, rootCtxCancel, interruptChan)
}
