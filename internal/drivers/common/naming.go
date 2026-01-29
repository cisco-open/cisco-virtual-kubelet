// Copyright © 2026 Cisco Systems, Inc.
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

import (
	"fmt"
	"strings"

	v1 "k8s.io/api/core/v1"
)

const (
	// Kubernetes standard labels used for pod and container identification
	LabelPodName       = "io.kubernetes.pod.name"
	LabelPodNamespace  = "io.kubernetes.pod.namespace"
	LabelPodUID        = "io.kubernetes.pod.uid"
	LabelContainerName = "io.kubernetes.container.name"
)

// GetAppHostingName returns the AppHosting name for a pod using its UID.
// The UID is guaranteed unique and fits within the 40-char YANG constraint (32 chars without hyphens).
// If the pod already has the label set, returns that value for idempotency.
func GetAppHostingName(pod *v1.Pod, index int8) string {

	cleanUUID := strings.ReplaceAll(string(pod.UID), "-", "")

	appID := fmt.Sprintf("cvk000%01d_%s", index, cleanUUID)

	return appID
}

// GenerateContainerAppIDs generates an appID for each container in the pod.
// Returns a map with container name as key and generated appID as value.
func GenerateContainerAppIDs(pod *v1.Pod) map[string]string {
	appIDs := make(map[string]string)

	for i, container := range pod.Spec.Containers {
		appID := GetAppHostingName(pod, int8(i))
		appIDs[container.Name] = appID
	}

	return appIDs
}

// ExtractContainerNameFromLabels extracts the container name from RunOpts labels.
// Returns the container name if found, empty string otherwise.
func ExtractContainerNameFromLabels(runOptsLine string) string {
	// Look for the label: io.kubernetes.container.name=<name>
	prefix := LabelContainerName + "="

	startIdx := strings.Index(runOptsLine, prefix)
	if startIdx == -1 {
		return ""
	}

	// Move past the prefix
	startIdx += len(prefix)

	// Find the end of the container name (space or end of string)
	endIdx := strings.Index(runOptsLine[startIdx:], " ")
	if endIdx == -1 {
		// Container name is at the end of the line
		return runOptsLine[startIdx:]
	}

	return runOptsLine[startIdx : startIdx+endIdx]
}
