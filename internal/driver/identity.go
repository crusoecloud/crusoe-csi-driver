package driver

import (
	"context"

	"github.com/container-storage-interface/spec/lib/go/csi"
	"google.golang.org/grpc"

	crusoeapi "github.com/crusoecloud/client-go/swagger/v1alpha5"
)

type IdentityServer struct {
	apiClient    *crusoeapi.APIClient
	driver       *DriverConfig
	capabilities []*csi.PluginCapability
}

func NewIdentityServer() *IdentityServer {
	return &IdentityServer{}
}

func (i *IdentityServer) Init(apiClient *crusoeapi.APIClient, driver *DriverConfig, services []Service) error {
	i.driver = driver
	i.apiClient = apiClient
	i.capabilities = []*csi.PluginCapability{
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
	for _, service := range services {
		if service == ControllerService {
			i.capabilities = append(i.capabilities, &csi.PluginCapability{
				Type: &csi.PluginCapability_Service_{
					Service: &csi.PluginCapability_Service{
						Type: csi.PluginCapability_Service_CONTROLLER_SERVICE,
					},
				},
			})
		}
	}

	return nil
}

func (i *IdentityServer) RegisterServer(srv *grpc.Server) error {
	csi.RegisterIdentityServer(srv, i)

	return nil
}

func (i *IdentityServer) GetPluginInfo(ctx context.Context, req *csi.GetPluginInfoRequest) (*csi.GetPluginInfoResponse, error) {
	return &csi.GetPluginInfoResponse{
		Name:          i.driver.GetName(),
		VendorVersion: i.driver.GetVendorVersion(),
	}, nil
}

func (i *IdentityServer) GetPluginCapabilities(ctx context.Context, req *csi.GetPluginCapabilitiesRequest) (*csi.GetPluginCapabilitiesResponse, error) {
	return &csi.GetPluginCapabilitiesResponse{
		Capabilities: i.capabilities,
	}, nil
}

func (i *IdentityServer) Probe(ctx context.Context, req *csi.ProbeRequest) (*csi.ProbeResponse, error) {
	return &csi.ProbeResponse{}, nil
}
