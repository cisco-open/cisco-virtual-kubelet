// Copyright © 2026 Cisco Systems Inc.
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//     http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.

package common

import "time"

// NodeResourceStats contains resource usage statistics for a node
type NodeResourceStats struct {
	// Timestamp when the stats were collected
	Timestamp time.Time

	// CPU usage in nanocores
	CPUUsageNanoCores uint64

	// Memory usage in bytes
	MemoryUsageBytes uint64
	// Memory available in bytes
	MemoryAvailableBytes uint64
	// Memory working set in bytes (used for OOM decisions)
	MemoryWorkingSetBytes uint64

	// Filesystem usage in bytes
	FsUsedBytes uint64
	// Filesystem capacity in bytes
	FsCapacityBytes uint64
	// Filesystem available in bytes
	FsAvailableBytes uint64

	// Network stats
	NetworkRxBytes uint64
	NetworkTxBytes uint64
}

// ContainerResourceStats contains resource usage statistics for a container
type ContainerResourceStats struct {
	// Container name (maps to AppHosting app name)
	Name string

	// Timestamp when the stats were collected
	Timestamp time.Time

	// CPU usage in nanocores
	CPUUsageNanoCores uint64

	// Memory usage in bytes
	MemoryUsageBytes uint64
	// Memory working set in bytes
	MemoryWorkingSetBytes uint64

	// Filesystem usage in bytes (rootfs)
	FsUsedBytes uint64
	// Filesystem capacity in bytes
	FsCapacityBytes uint64

	// Network stats (if available per-container)
	NetworkRxBytes uint64
	NetworkTxBytes uint64
}

// PodResourceStats contains resource usage statistics for a pod
type PodResourceStats struct {
	// Pod namespace
	Namespace string
	// Pod name
	Name string
	// Pod UID
	UID string

	// Timestamp when the stats were collected
	Timestamp time.Time

	// Container stats
	Containers []ContainerResourceStats

	// Aggregate CPU usage in nanocores
	CPUUsageNanoCores uint64

	// Aggregate memory usage in bytes
	MemoryUsageBytes uint64
	// Aggregate memory working set in bytes
	MemoryWorkingSetBytes uint64

	// Network stats
	NetworkRxBytes uint64
	NetworkTxBytes uint64
}
