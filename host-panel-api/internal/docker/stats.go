package docker

import (
	"context"
	"encoding/json"

	"host-panel-api/internal/model"

	"github.com/moby/moby/client"
)

// dockerStats mirrors the JSON structure returned by the Docker stats API.
type dockerStats struct {
	CPUStats    cpuStats                `json:"cpu_stats"`
	PreCPUStats cpuStats                `json:"precpu_stats"`
	MemoryStats memoryStats             `json:"memory_stats"`
	Networks    map[string]networkStats `json:"networks"`
	PidsStats   struct {
		Current uint64 `json:"current"`
	} `json:"pids_stats"`
}

type cpuStats struct {
	CPUUsage struct {
		TotalUsage uint64 `json:"total_usage"`
	} `json:"cpu_usage"`
	SystemCPUUsage uint64 `json:"system_cpu_usage"`
	OnlineCPUs     uint64 `json:"online_cpus"`
}

type memoryStats struct {
	Usage uint64 `json:"usage"`
	Limit uint64 `json:"limit"`
	Stats struct {
		InactiveFile uint64 `json:"inactive_file"`
	} `json:"stats"`
}

type networkStats struct {
	RxBytes uint64 `json:"rx_bytes"`
	TxBytes uint64 `json:"tx_bytes"`
}

// ContainerStatsOneShot fetches a single stats snapshot for a container.
// Uses stream=false so the Docker daemon returns one JSON object and closes.
func (c *Client) ContainerStatsOneShot(ctx context.Context, id string) (*model.ContainerStats, error) {
	resp, err := c.cli.ContainerStats(ctx, id, client.ContainerStatsOptions{Stream: false, IncludePreviousSample: true})
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	var ds dockerStats
	if err := json.NewDecoder(resp.Body).Decode(&ds); err != nil {
		return nil, err
	}

	// CPU percentage: delta usage / delta system × number of CPUs × 100
	cpuPct := 0.0
	cpuDelta := float64(ds.CPUStats.CPUUsage.TotalUsage - ds.PreCPUStats.CPUUsage.TotalUsage)
	sysDelta := float64(ds.CPUStats.SystemCPUUsage - ds.PreCPUStats.SystemCPUUsage)
	if sysDelta > 0 && ds.CPUStats.OnlineCPUs > 0 {
		cpuPct = (cpuDelta / sysDelta) * float64(ds.CPUStats.OnlineCPUs) * 100.0
	}

	// Memory: usage minus cache (inactive_file)
	memUsage := ds.MemoryStats.Usage
	if ds.MemoryStats.Stats.InactiveFile > 0 && memUsage > ds.MemoryStats.Stats.InactiveFile {
		memUsage -= ds.MemoryStats.Stats.InactiveFile
	}

	// When no memory limit is set, Docker reports the host's total RAM.
	// Suppress it to avoid leaking host info — treat limits > 1 TB as "no limit".
	memLimit := ds.MemoryStats.Limit
	if memLimit > 1<<40 {
		memLimit = 0
	}

	memPct := 0.0
	if memLimit > 0 {
		memPct = float64(memUsage) / float64(memLimit) * 100.0
	}

	// Network: sum all interfaces
	var netRx, netTx uint64
	for _, iface := range ds.Networks {
		netRx += iface.RxBytes
		netTx += iface.TxBytes
	}

	return &model.ContainerStats{
		ID:       id,
		Name:     "", // set by caller via ContainerStatsOneShotByName
		CPUPct:   cpuPct,
		MemUsage: memUsage,
		MemLimit: memLimit,
		MemPct:   memPct,
		NetRx:    netRx,
		NetTx:    netTx,
		PIDs:     ds.PidsStats.Current,
	}, nil
}

// ContainerStatsOneShotByName is a convenience wrapper that also sets the container name.
func (c *Client) ContainerStatsOneShotByName(ctx context.Context, id, name string) (*model.ContainerStats, error) {
	stats, err := c.ContainerStatsOneShot(ctx, id)
	if err != nil {
		return nil, err
	}
	stats.Name = name
	return stats, nil
}
