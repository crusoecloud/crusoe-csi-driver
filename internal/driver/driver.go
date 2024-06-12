package driver

type DriverConfig struct {
	// These should be consistent regardless of which node the driver is running on.
	VendorName    string
	VendorVersion string
	// These are initialized on a per-node unique basis
	NodeID       string
	NodeLocation string
	NodeProject  string
}

type Service int

const (
	NodeService Service = iota
	IdentityService
	ControllerService
)

// Note: these are injected during build
// This name MUST correspond with the name provided to the storage class
// This is how Kubernetes knows to invoke our CSI.
var (
	name    string
	version string
)

func GetVendorName() string {
	return name
}

func GetVendorVersion() string {
	return version
}

func (d *DriverConfig) GetName() string {
	return d.VendorName
}

func (d *DriverConfig) GetVendorVersion() string {
	return d.VendorVersion
}

func (d *DriverConfig) GetNodeID() string {
	return d.NodeID
}

func (d *DriverConfig) GetNodeProject() string {
	return d.NodeProject
}

func (d *DriverConfig) GetNodeLocation() string {
	return d.NodeLocation
}
