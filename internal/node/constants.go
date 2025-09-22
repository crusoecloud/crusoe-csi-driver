package node

const (
	NewDirPerms          = 0o755 // this represents: rwxr-xr-x
	NewFilePerms         = 0o644 // this represents: rw-r--r--
	ExpectedTypeSegments = 2
	ReadOnlyMountOption  = "ro"
	NoLoadMountOption    = "noload"
)
