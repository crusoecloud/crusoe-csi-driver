package driver

type Config struct {
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
//
//nolint:gochecknoglobals // we will use these global vars to identify the name and version of the CSI
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

func (d *Config) GetName() string {
	return d.VendorName
}

func (d *Config) GetVendorVersion() string {
	return d.VendorVersion
}

func (d *Config) GetNodeID() string {
	return d.NodeID
}

func (d *Config) GetNodeProject() string {
	return d.NodeProject
}

func (d *Config) GetNodeLocation() string {
	return d.NodeLocation
}
