package model

// ContainerStats holds the resource usage snapshot of a single container.
type ContainerStats struct {
	ID       string  `json:"id"`
	Name     string  `json:"name"`
	CPUPct   float64 `json:"cpuPct"`
	MemUsage uint64  `json:"memUsage"`
	MemLimit uint64  `json:"memLimit"`
	MemPct   float64 `json:"memPct"`
	NetRx    uint64  `json:"netRx"`
	NetTx    uint64  `json:"netTx"`
	PIDs     uint64  `json:"pids"`
}
