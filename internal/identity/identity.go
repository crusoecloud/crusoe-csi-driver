package identity

import (
	"context"

	"github.com/container-storage-interface/spec/lib/go/csi"
)

type Service struct {
	csi.UnimplementedIdentityServer
	PluginName    string
	PluginVersion string
	Capabilities  []*csi.PluginCapability
}

func (s *Service) GetPluginInfo(_ context.Context,
	_ *csi.GetPluginInfoRequest,
) (*csi.GetPluginInfoResponse, error) {
	return &csi.GetPluginInfoResponse{
		Name:          s.PluginName,
		VendorVersion: s.PluginVersion,
	}, nil
}

func (s *Service) GetPluginCapabilities(_ context.Context,
	_ *csi.GetPluginCapabilitiesRequest,
) (*csi.GetPluginCapabilitiesResponse, error) {
	return &csi.GetPluginCapabilitiesResponse{
		Capabilities: s.Capabilities,
	}, nil
}

func (s *Service) Probe(_ context.Context,
	_ *csi.ProbeRequest,
) (*csi.ProbeResponse, error) {
	return &csi.ProbeResponse{}, nil
}
