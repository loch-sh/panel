package functions

// Defaults and bounds of the FUNCTIONS_* variables (read by internal/config).
const (
	DefaultMax             = 50
	DefaultMaxMemoryMB     = 3008 // Lambda's historical maximum; the spike measured 256 MB enough for an image resize
	DefaultMaxCodeBytes    = 50 << 20
	DefaultDeploymentsKept = 3
	DefaultTmpSizeMB       = 512 // Lambda's default ephemeral storage

	MinMemoryMB = 128 // Lambda's minimum
	MaxMemoryMB = 10240
	MinTmpMB    = 16
	MaxTmpMB    = 10240
	MinCodeMB   = 1
	MaxCodeMB   = 250 // never above the extracted-size cap
)

// Defaults of a new function.
const (
	DefaultMemoryMB       = 512
	DefaultCPUs           = 1.0
	DefaultTimeoutSeconds = 30
	MaxTimeoutSeconds     = 900 // Lambda's maximum
	MinCPUs               = 0.1
	MaxCPUs               = 16
)

// Config configures the functions module.
type Config struct {
	Enabled bool
	// DataDir (FUNCTIONS_DATA_DIRECTORY) holds the deployments, the runtime
	// binaries and the private directory; same path on the host and in the
	// API container, outside StacksDir.
	DataDir string
	// StacksDir is HOSTING_STACKS_DIRECTORY (compose projects fn-<name>).
	StacksDir       string
	Max             int   // number of functions
	MaxMemoryMB     int   // memory limit of one function
	MaxCodeBytes    int64 // size of an uploaded zip
	DeploymentsKept int   // deployments kept for a rollback (the active one included)
	TmpSizeMB       int   // default size of the /tmp tmpfs
}

// Paths returns the paths of the module.
func (c Config) Paths() Paths { return Paths{DataDir: c.DataDir, StacksDir: c.StacksDir} }
