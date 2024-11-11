package internal

import "github.com/thediveo/enumflag/v2"

type ServiceType enumflag.Flag

const (
	ServiceTypeIdentity ServiceType = iota
	ServiceTypeController
	ServiceTypeNode
)

var ServiceTypeNames = map[ServiceType][]string{
	ServiceTypeIdentity:   {"identity"},
	ServiceTypeController: {"controller"},
	ServiceTypeNode:       {"node"},
}

var Services = []ServiceType{ServiceTypeIdentity}

type CSIDriverType enumflag.Flag

const (
	CSIDriverTypeSSD CSIDriverType = iota
	CSIDriverTypeFS
)

var CSIDriverTypeNames = map[CSIDriverType][]string{
	CSIDriverTypeSSD: {"ssd"},
	CSIDriverTypeFS:  {"fs"},
}

var SelectedCSIDriverType = CSIDriverTypeSSD

const (
	CrusoeAPIEndpointFlag = "crusoe-api-endpoint"
	CrusoeAccessKeyFlag   = "crusoe-csi-access-key"
	CrusoeSecretKeyFlag   = "crusoe-csi-secret-key"
	CrusoeProjectIDFlag   = "crusoe-project-id"
	CSIDriverTypeFlag     = "crusoe-csi-driver-type"
	NodeNameFlag          = "node-name"
	SocketAddressFlag     = "socket-address"
)

const (
	CrusoeAPIEndpointDefault = "https://api.crusoecloud.com/v1alpha5"
	SocketAddressDefault     = "unix:/tmp/csi.sock"
)
