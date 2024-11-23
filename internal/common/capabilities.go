package common

import "github.com/container-storage-interface/spec/lib/go/csi"

//nolint:gochecknoglobals // can't construct const slice
var BaseIdentityCapabilities = []*csi.PluginCapability{
	{
		Type: &csi.PluginCapability_Service_{
			Service: &csi.PluginCapability_Service{
				Type: csi.PluginCapability_Service_VOLUME_ACCESSIBILITY_CONSTRAINTS,
			},
		},
	},
}

//nolint:gochecknoglobals  // can't construct const slice
var BaseControllerCapabilities = []*csi.ControllerServiceCapability{
	{
		Type: &csi.ControllerServiceCapability_Rpc{
			Rpc: &csi.ControllerServiceCapability_RPC{
				Type: csi.ControllerServiceCapability_RPC_CREATE_DELETE_VOLUME,
			},
		},
	},
	{
		Type: &csi.ControllerServiceCapability_Rpc{
			Rpc: &csi.ControllerServiceCapability_RPC{
				Type: csi.ControllerServiceCapability_RPC_PUBLISH_UNPUBLISH_VOLUME,
			},
		},
	},
	{
		Type: &csi.ControllerServiceCapability_Rpc{
			Rpc: &csi.ControllerServiceCapability_RPC{
				Type: csi.ControllerServiceCapability_RPC_EXPAND_VOLUME,
			},
		},
	},
	{
		Type: &csi.ControllerServiceCapability_Rpc{
			Rpc: &csi.ControllerServiceCapability_RPC{
				Type: csi.ControllerServiceCapability_RPC_SINGLE_NODE_MULTI_WRITER,
			},
		},
	},
}

//nolint:gochecknoglobals  // can't construct const slice
var BaseNodeCapabilities = []*csi.NodeServiceCapability{
	{
		Type: &csi.NodeServiceCapability_Rpc{
			Rpc: &csi.NodeServiceCapability_RPC{
				Type: csi.NodeServiceCapability_RPC_SINGLE_NODE_MULTI_WRITER,
			},
		},
	},
}

//nolint:gochecknoglobals  // can't construct const struct
var PluginCapabilityControllerService = csi.PluginCapability{
	Type: &csi.PluginCapability_Service_{
		Service: &csi.PluginCapability_Service{
			Type: csi.PluginCapability_Service_CONTROLLER_SERVICE,
		},
	},
}

//nolint:gochecknoglobals  // can't construct const struct
var PluginCapabilityVolumeExpansionOnline = csi.PluginCapability{
	Type: &csi.PluginCapability_VolumeExpansion_{
		VolumeExpansion: &csi.PluginCapability_VolumeExpansion{
			Type: csi.PluginCapability_VolumeExpansion_ONLINE,
		},
	},
}

//nolint:gochecknoglobals  // can't construct const struct
var PluginCapabilityVolumeExpansionOffline = csi.PluginCapability{
	Type: &csi.PluginCapability_VolumeExpansion_{
		VolumeExpansion: &csi.PluginCapability_VolumeExpansion{
			Type: csi.PluginCapability_VolumeExpansion_OFFLINE,
		},
	},
}

//nolint:gochecknoglobals  // can't construct const struct
var NodeCapabilityExpandVolume = csi.NodeServiceCapability{
	Type: &csi.NodeServiceCapability_Rpc{
		Rpc: &csi.NodeServiceCapability_RPC{
			Type: csi.NodeServiceCapability_RPC_EXPAND_VOLUME,
		},
	},
}
