// Copyright © 2026 Cisco Systems Inc.
// Licensed under the Apache License, Version 2.0 (the "License");
package catalystcenter

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"slices"
	"strconv"
	"strings"

	ciskov1 "github.com/cisco/virtual-kubelet-cisco/api/v1alpha1"
)

const ssidHistoryResourcePrefix = "ssid-credential-history-"

func ssidHistoryPrefix(history []ciskov1.NetworkControllerConfigurationSSIDCredentialHistory) string {
	raw, _ := json.Marshal(history)
	sum := sha256.Sum256(raw)
	return ssidHistoryResourcePrefix + hex.EncodeToString(sum[:])
}

// The resource's non-secret history digest makes pruning even a retired entry
// observable after restart. Legacy credentials retain their original semantics.
func validateSSIDCredentialHistory(section, resource string, versions []ciskov1.NetworkControllerConfigurationCredentialVersion, history []ciskov1.NetworkControllerConfigurationSSIDCredentialHistory) error {
	marked := strings.HasPrefix(resource, ssidHistoryResourcePrefix)
	if len(history) == 0 {
		if marked {
			return errConfigurationSecret
		}
		return nil
	}
	parts := strings.SplitN(resource, ":", 3)
	if section != "wireless" || !marked || len(parts) != 3 || parts[0] != ssidHistoryPrefix(history) || !validAPIID(parts[1]) || parts[2] == "" || len(history) > 5 {
		return errConfigurationSecret
	}
	floors := map[int64]int64{}
	last := int64(-1)
	for _, h := range history {
		if h.Priority <= last || h.Priority > 4 || h.Version < 1 {
			return errConfigurationSecret
		}
		last = h.Priority
		floors[h.Priority] = h.Version
	}
	if len(versions) == 0 {
		return errConfigurationSecret
	}
	if _, err := wirelessSSIDCredentialRotation(versions, versions); err != nil {
		return err
	}
	for _, v := range versions {
		if v.Key == "passphrase" {
			continue
		}
		priority, _ := strconv.ParseInt(strings.TrimPrefix(v.Key, "multi-psk:"), 10, 64)
		if floors[priority] != v.Version {
			return errConfigurationSecret
		}
	}
	return nil
}

// Equivalence preserves native SSID identity when migrating a legacy receipt
// or advancing a history digest. Duplicate aliases remain an ownership error.
func configurationCredentialResourceSame(section, a, b string) bool {
	if a == b {
		return true
	}
	if !isWirelessSSIDCredentialResource(section, a) || !isWirelessSSIDCredentialResource(section, b) {
		return false
	}
	left, right := strings.SplitN(a, ":", 3), strings.SplitN(b, ":", 3)
	return len(left) == 3 && len(right) == 3 && left[1] == right[1] && left[2] == right[2]
}

// History stores no password/source for a retired priority. Reintroduction must
// explicitly advance beyond its high-water mark, even if its alias is unchanged.
func wirelessSSIDCredentialTransition(prior *ciskov1.NetworkControllerConfigurationCredentialApplication, desired []ciskov1.NetworkControllerConfigurationCredentialVersion, ownsSet bool) (bool, []ciskov1.NetworkControllerConfigurationSSIDCredentialHistory, error) {
	if _, err := wirelessSSIDCredentialRotation(desired, desired); err != nil {
		return false, nil, err
	}
	floors := map[int64]int64{}
	oldActive := map[string]ciskov1.NetworkControllerConfigurationCredentialVersion{}
	if prior != nil {
		if err := validateSSIDCredentialHistory(prior.Section, prior.Resource, prior.Versions, prior.SSIDCredentialHistory); err != nil {
			return false, nil, err
		}
		if _, err := wirelessSSIDCredentialRotation(prior.Versions, prior.Versions); err != nil {
			return false, nil, err
		}
		for _, h := range prior.SSIDCredentialHistory {
			floors[h.Priority] = h.Version
		}
		for _, v := range prior.Versions {
			oldActive[v.Key] = v
			if v.Key != "passphrase" {
				p, _ := strconv.ParseInt(strings.TrimPrefix(v.Key, "multi-psk:"), 10, 64)
				floors[p] = v.Version
			}
		}
	}
	retained := []ciskov1.NetworkControllerConfigurationCredentialVersion{}
	seen := map[string]bool{}
	for _, v := range desired {
		seen[v.Key] = true
		if old, ok := oldActive[v.Key]; ok {
			retained = append(retained, old)
		}
		if v.Key == "passphrase" {
			continue
		}
		p, _ := strconv.ParseInt(strings.TrimPrefix(v.Key, "multi-psk:"), 10, 64)
		_, active := oldActive[v.Key]
		if v.Version < floors[p] || !active && floors[p] > 0 && v.Version <= floors[p] {
			return false, nil, errConfigurationSecret
		}
		floors[p] = v.Version
	}
	removed := false
	for key := range oldActive {
		if !seen[key] {
			if key == "passphrase" || !ownsSet {
				return false, nil, errConfigurationSecret
			}
			removed = true
		}
	}
	rotate, err := wirelessSSIDCredentialRotation(retained, desired)
	if err != nil {
		return false, nil, err
	}
	history := []ciskov1.NetworkControllerConfigurationSSIDCredentialHistory{}
	for p, v := range floors {
		history = append(history, ciskov1.NetworkControllerConfigurationSSIDCredentialHistory{Priority: p, Version: v})
	}
	slices.SortFunc(history, func(a, b ciskov1.NetworkControllerConfigurationSSIDCredentialHistory) int {
		return int(a.Priority - b.Priority)
	})
	if len(history) == 0 {
		history = nil
	}
	return rotate || removed, history, nil
}

// Recovery observes an already retained native task. It never grants dispatch,
// infers a task from metadata, or widens other executors' Unknown semantics.
func ssidKnownTaskRecoverySupported(op *ciskov1.NetworkControllerConfigurationOperation) bool {
	if op == nil || op.Phase != "Unknown" || !isWirelessSSIDCredentialResource(op.Section, op.Resource) || !validAPIID(op.TaskID) || op.SubmittedAt == nil || op.SubmittedAt.IsZero() || len(op.CredentialVersions) == 0 || !validAPIID(op.ParentID) || op.TargetSerial != "" || op.PhysicalGroup != nil || op.CancellationRequestedAt != nil || op.CreatedResourceID != "" || op.DependencyVersions != nil {
		return false
	}
	if op.Action != "Create" && op.Action != "Update" || op.Action == "Create" && op.TargetID != "" || op.Action == "Update" && !validAPIID(op.TargetID) {
		return false
	}
	if err := validateSSIDCredentialHistory(op.Section, op.Resource, op.CredentialVersions, op.SSIDCredentialHistory); err != nil {
		return false
	}
	_, err := wirelessSSIDCredentialRotation(op.CredentialVersions, op.CredentialVersions)
	return err == nil
}
