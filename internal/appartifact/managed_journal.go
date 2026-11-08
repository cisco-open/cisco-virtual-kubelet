// Copyright 2026 Cisco Systems Inc.
// SPDX-License-Identifier: Apache-2.0
package appartifact

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	corev1 "k8s.io/api/core/v1"
)

const (
	JournalKindAnnotation  = "apphosting.cisco.com/journal"
	JournalDeviceNamespace = "apphosting.cisco.com/device-namespace"
	JournalDeviceName      = "apphosting.cisco.com/device-name"
	JournalDeviceUID       = "apphosting.cisco.com/device-uid"
)

// Managed journals belong to a device incarnation, and survive its deletion.
func ManagedJournalName(uid string) string {
	sum := sha256.Sum256([]byte(uid))
	return "cvk-artifacts-" + hex.EncodeToString(sum[:12])
}
func JournalAnnotations(namespace, name, uid string) map[string]string {
	return map[string]string{JournalKindAnnotation: "v1", JournalDeviceNamespace: namespace, JournalDeviceName: name, JournalDeviceUID: uid}
}
func ValidateManagedJournal(cm *corev1.ConfigMap, namespace, name, uid string) error {
	if uid == "" || cm.Namespace != namespace || cm.Name != ManagedJournalName(uid) || len(cm.OwnerReferences) != 0 || cm.DeletionTimestamp != nil || (cm.Immutable != nil && *cm.Immutable) {
		return fmt.Errorf("invalid managed artifact journal identity or lifecycle")
	}
	for key, value := range JournalAnnotations(namespace, name, uid) {
		if cm.Annotations[key] != value {
			return fmt.Errorf("managed artifact journal identity mismatch")
		}
	}
	return nil
}
