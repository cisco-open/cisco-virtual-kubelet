// Copyright 2026 Cisco Systems Inc.
// SPDX-License-Identifier: Apache-2.0
package catalystcenter

import (
	"reflect"
	"strings"
	"time"

	cvk "github.com/cisco/virtual-kubelet-cisco/api/v1alpha1"
	contract "github.com/cisco/virtual-kubelet-cisco/internal/controllerconfiguration"
)

// Reconstruct native observation inputs from the shared persisted group record.
// This does not create/advance the journal or establish section ownership; the
// caller must supply freshly resolved native and Kubernetes target evidence.
func pendingFabricEventJournalReceipt(want fabricSiteIntent, op *cvk.NetworkControllerConfigurationOperation, snapshot *fabricPendingEventsSnapshot, targets []contract.GroupTargetIdentity, now time.Time) (fabricPendingEventReceipt, error) {
	zero := fabricPendingEventReceipt{}
	if op == nil || snapshot == nil || op.Section != "fabric" || op.Action != "Update" || op.CancellationRequestedAt != nil || !validAPIID(op.TargetID) || !validAPIID(op.ParentID) || !validAPIID(op.TaskID) || op.SubmittedAt == nil || op.SubmittedAt.IsZero() || op.SubmittedAt.After(now) {
		return zero, errConfigurationTransition
	}
	switch op.Phase {
	case "Accepted", "Verifying", "Unknown":
	default:
		return zero, errConfigurationTransition
	}
	if err := contract.ValidateGroupJournalIdentity(op); err != nil {
		return zero, errConfigurationStale
	}
	canonical, err := contract.CanonicalGroupTargets(targets)
	if err != nil || !reflect.DeepEqual(canonical, op.PhysicalGroup.Targets) {
		return zero, errConfigurationStale
	}
	if snapshot.site.want != want || !snapshot.site.exists || snapshot.site.actual.ID != op.ParentID || snapshot.site.actual.SiteID != snapshot.site.siteID || !fabricSiteMatches(want, snapshot.site.actual) || !validAPIID(snapshot.site.siteID) {
		return zero, errConfigurationStale
	}
	if len(snapshot.devices) != len(canonical) {
		return zero, errConfigurationStale
	}
	for _, target := range canonical {
		found := false
		for _, device := range snapshot.devices {
			if device.ID == target.InventoryID && device.Serial == target.PhysicalIdentity {
				found = true
				break
			}
		}
		if !found {
			return zero, errConfigurationStale
		}
	}
	event := fabricPendingEvent{ID: op.TargetID, FabricID: op.ParentID}
	resource := pendingFabricEventResource(want.path, snapshot.site.siteID, event)
	if resource != op.Resource {
		return zero, errConfigurationStale
	}
	return fabricPendingEventReceipt{resource: resource, siteID: snapshot.site.siteID, event: event, taskID: op.TaskID, submitted: op.SubmittedAt.Time}, nil
}

// The resource prefix also fences a partially pruned group journal.
func isPendingFabricEventOperation(section, resource string) bool {
	return section == "fabric" && strings.HasPrefix(resource, "fabric-pending-event:")
}
