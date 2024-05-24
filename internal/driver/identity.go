package driver

import (
	"context"
	"github.com/container-storage-interface/spec/lib/go/csi"
	crusoeapi "github.com/crusoecloud/client-go/swagger/v1alpha5"
	"google.golang.org/grpc"
)

type IdentityServer struct {
	apiClient *crusoeapi.APIClient
	driver    *DriverConfig
}

func NewIdentityServer() *IdentityServer {
	return &IdentityServer{}
}

func (i *IdentityServer) Init(apiClient *crusoeapi.APIClient, driver *DriverConfig) error {
	i.driver = driver
	i.apiClient = apiClient

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
		Capabilities: []*csi.PluginCapability{
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
		},
	}, nil
}

func (i *IdentityServer) Probe(ctx context.Context, req *csi.ProbeRequest) (*csi.ProbeResponse, error) {
	return &csi.ProbeResponse{}, nil
}
