package cloud

import "context"

// Catalog provides read-only discovery without coupling it to worker lifecycle
// operations or scan credentials.
type Catalog interface {
	Regions(context.Context) ([]Region, error)
	InstanceTypes(context.Context, string) ([]InstanceType, error)
}

type Region struct {
	ID, Name, Country, Continent string
}

type InstanceType struct {
	ID, Family, CPUType, CPUVendor, DiskType, StorageType string
	VCPUs, MemoryMB, DiskGB, DiskCount                    int
}
