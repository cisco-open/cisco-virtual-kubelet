package common

import (
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
func GetAppHostingName(pod *v1.Pod) string {
	if name, ok := pod.Labels[AppHostingNameLabel]; ok {
		return name
	}
	return strings.ReplaceAll(string(pod.UID), "-", "")
}
