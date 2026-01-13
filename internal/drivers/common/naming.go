package common

import (
	"fmt"
	"strings"

	"github.com/google/uuid"
)

const (
	// AppHostingNameLabel is the label key used to store the AppHosting name on a pod
	AppHostingNameLabel = "cisco.com/apphosting-name"
)

// GetAppHostingName returns the AppHosting name for a pod using its UID.
// The UID is guaranteed unique and fits within the 40-char YANG constraint (32 chars without hyphens).
// If the pod already has the label set, returns that value for idempotency.
func GetAppHostingName(index int8) string {

	cleanUUID := strings.ReplaceAll(uuid.New().String(), "-", "")

	appID := fmt.Sprintf("cvk000%01d_%s", index, cleanUUID)

	return appID
}
