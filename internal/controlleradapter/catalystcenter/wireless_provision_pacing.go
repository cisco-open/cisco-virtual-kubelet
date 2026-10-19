// Copyright © 2026 Cisco Systems Inc.
// Licensed under the Apache License, Version 2.0 (the "License");
package catalystcenter

import (
	configapi "github.com/cisco/virtual-kubelet-cisco/api/config/v1alpha1"
	cvk "github.com/cisco/virtual-kubelet-cisco/api/v1alpha1"
	"strings"
	"time"
)

const wirelessManagedAPSettleDelay = 10 * time.Second

// Uses persisted verification time, never process uptime or task submission.
// Missing legacy timestamps do not prove the dependency's settling interval.
func wirelessProvisionDependenciesReady(nc *cvk.NetworkController, config *configapi.NetworkControllerConfig, source *configSourceSnapshot, resources []string, now time.Time) error {
	if nc == nil || config == nil || now.IsZero() {
		return errConfigurationStale
	}
	sourceUID, sourceVersion := "", ""
	if source != nil {
		sourceUID, sourceVersion = string(source.uid), source.version
	}
	seen := map[string]bool{}
	for _, resource := range resources {
		if resource == "" || seen[resource] || !strings.Contains(resource, "managed-ap:") {
			return errConfigurationTransition
		}
		seen[resource] = true
		if nc.Status.Configuration == nil {
			return errConfigurationBusy
		}
		var match *cvk.NetworkControllerConfigurationActionApplication
		for i := range nc.Status.Configuration.ActionApplications {
			receipt := &nc.Status.Configuration.ActionApplications[i]
			if receipt.Section != "inventory" || receipt.Resource != resource {
				continue
			}
			if match != nil {
				return errConfigurationOwned
			}
			match = receipt
		}
		if match == nil {
			return errConfigurationBusy
		}
		op := &match.Operation
		if op.Phase != "Verified" || op.Resource != resource || op.Section != "inventory" || !configurationOperationID.MatchString(op.ID) || op.ConfigUID != string(config.UID) || op.ConfigName != config.Name || op.ConfigGeneration != config.Generation || op.ControllerGeneration != nc.Generation || op.SourceUID != sourceUID || op.SourceVersion != sourceVersion || op.Action != "Update" || !validAPIID(op.TargetID) || op.TargetSerial == "" || !validAPIID(op.TaskID) || op.SubmittedAt == nil || op.SubmittedAt.IsZero() || op.SubmittedAt.After(now) {
			return errConfigurationStale
		}
		if match.VerifiedAt == nil {
			return errConfigurationBusy
		}
		if match.VerifiedAt.IsZero() || match.VerifiedAt.After(now) || match.VerifiedAt.Before(op.SubmittedAt) {
			return errConfigurationStale
		}
		// metav1.Time persists whole seconds; add one second to avoid releasing
		// up to a second early after timestamp truncation.
		if now.Before(match.VerifiedAt.Add(wirelessManagedAPSettleDelay + time.Second)) {
			return errConfigurationBusy
		}
	}
	return nil
}
