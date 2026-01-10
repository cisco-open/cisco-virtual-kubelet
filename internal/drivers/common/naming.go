package common

import (
	"crypto/sha256"
	"encoding/hex"
	"strings"
)

const (
	// MaxAppHostingNameLength is the maximum length allowed by Cisco AppHosting YANG model
	MaxAppHostingNameLength = 40
)

// K8sToAppHostingName converts a Kubernetes pod identifier to a valid Cisco AppHosting name.
// Kubernetes naming (RFC 1123): lowercase alphanumeric + hyphen, max 63 chars
// Cisco AppHosting YANG: alphanumeric + underscore only, max 40 chars
//
// Format: {sanitized_name}_{short_namespace_hash} if namespace != "default"
// Format: {sanitized_name} if namespace == "default" or empty
func K8sToAppHostingName(namespace, name string) string {
	// Replace hyphens with underscores (K8s uses -, Cisco allows _)
	sanitized := strings.ReplaceAll(name, "-", "_")

	// For non-default namespaces, add a short hash suffix for uniqueness
	if namespace != "" && namespace != "default" {
		hash := sha256.Sum256([]byte(namespace))
		suffix := "_" + hex.EncodeToString(hash[:])[:6] // 6 char hash

		// Ensure total length <= 40
		maxBase := MaxAppHostingNameLength - len(suffix)
		if len(sanitized) > maxBase {
			sanitized = sanitized[:maxBase]
		}
		return sanitized + suffix
	}

	// Truncate if needed for default namespace
	if len(sanitized) > MaxAppHostingNameLength {
		sanitized = sanitized[:MaxAppHostingNameLength]
	}

	return sanitized
}

// AppHostingToK8sName converts a Cisco AppHosting name back to a Kubernetes-style name.
// This handles simple cases (default namespace). For namespaced lookups with hash suffixes,
// the original pod name should be retrieved from the pod annotation or a mapping.
func AppHostingToK8sName(appName string) string {
	// Remove namespace hash suffix if present (last 7 chars: _XXXXXX where X is hex)
	if len(appName) > 7 && appName[len(appName)-7] == '_' {
		suffix := appName[len(appName)-6:]
		if isHexString(suffix) {
			appName = appName[:len(appName)-7]
		}
	}
	return strings.ReplaceAll(appName, "_", "-")
}

// isHexString returns true if s contains only hexadecimal characters
func isHexString(s string) bool {
	for _, c := range s {
		if !((c >= '0' && c <= '9') || (c >= 'a' && c <= 'f') || (c >= 'A' && c <= 'F')) {
			return false
		}
	}
	return true
}