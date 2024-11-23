package internal

import (
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
