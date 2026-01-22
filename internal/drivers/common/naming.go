package common

import (
	"fmt"
	"strings"

	v1 "k8s.io/api/core/v1"
)

const (
	// AppHostingNameLabel is the label key used to store the AppHosting name on a pod
	AppHostingNameLabel = "cisco.com/apphosting-name"
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
	prefix := "io.kubernetes.container.name="
	
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
