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

package nxos

import (
	"context"
	"regexp"
	"strings"
)

type nxosAppHostingResources struct {
	CPUTotalMilli      int64
	CPUAvailableMilli  int64
	VCPUTotal          int64
	VCPUAvailable      int64
	MemoryTotalMB      int64
	MemoryAvailableMB  int64
	StorageTotalMB     int64
	StorageAvailableMB int64
}

func (r nxosAppHostingResources) hasCapacity() bool {
	return r.CPUTotalMilli > 0 || r.MemoryTotalMB > 0 || r.StorageTotalMB > 0
}

func (d *NXOSDriver) readAppHostingResources(ctx context.Context) (nxosAppHostingResources, error) {
	out, err := d.client.show(ctx, "show app-hosting resource")
	if err != nil {
		return nxosAppHostingResources{}, err
	}
	return parseAppHostingResources(out), nil
}

func parseAppHostingResources(out string) nxosAppHostingResources {
	var res nxosAppHostingResources
	for _, line := range strings.Split(out, "\n") {
		lower := strings.ToLower(line)
		numbers := numericFields(line)
		if len(numbers) == 0 {
			continue
		}
		total := numbers[0]
		available := numbers[len(numbers)-1]
		switch {
		case strings.Contains(lower, "vcpu"):
			res.VCPUTotal = total
			res.VCPUAvailable = available
		case strings.Contains(lower, "cpu"):
			res.CPUTotalMilli = total
			res.CPUAvailableMilli = available
		case strings.Contains(lower, "memory"):
			res.MemoryTotalMB = total
			res.MemoryAvailableMB = available
		case strings.Contains(lower, "storage") || strings.Contains(lower, "disk"):
			res.StorageTotalMB = total
			res.StorageAvailableMB = available
		}
	}
	return res
}

func numericFields(line string) []int64 {
	matches := regexp.MustCompile(`\b\d+\b`).FindAllString(line, -1)
	out := make([]int64, 0, len(matches))
	for _, match := range matches {
		out = append(out, parseInt64(match))
	}
	return out
}
