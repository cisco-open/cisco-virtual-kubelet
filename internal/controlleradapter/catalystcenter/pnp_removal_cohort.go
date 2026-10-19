// Copyright 2026 Cisco Systems Inc.
// SPDX-License-Identifier: Apache-2.0
package catalystcenter

import (
	"context"
	"crypto/sha256"
	"fmt"
	"sort"
	"strings"
	"time"

	configapi "github.com/cisco/virtual-kubelet-cisco/api/config/v1alpha1"
	cvk "github.com/cisco/virtual-kubelet-cisco/api/v1alpha1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// A read-only projection of existing creator evidence, never a dispatch permit.
// Recompute after each completed removal; do not retain a batch mutation token.
type pnpRemovalCohortEntry struct {
	Creator  cvk.NetworkControllerConfigurationActionApplication
	Baseline pnpRemovalBaseline
}

func ownedPnPRemovalCohort(nc *cvk.NetworkController, c *configapi.NetworkControllerConfig, s *configSourceSnapshot) ([]pnpRemovalCohortEntry, error) {
	if nc == nil || nc.Status.Configuration == nil || c == nil {
		return nil, errConfigurationOwned
	}
	declared, err := pnpRemovalDeclaredSerials(c, s)
	if err != nil {
		return nil, err
	}
	state := nc.Status.Configuration
	if err := configurationOwnersMatch(state, c); err != nil {
		return nil, err
	}
	roots := map[string]pnpRemovalCohortEntry{}
	for _, r := range state.ActionApplications {
		if !isPnPRegistrationOperation(r.Operation.Section, r.Operation.Resource) && !strings.HasPrefix(r.Resource, pnpRecordPrefix) {
			continue
		}
		creator, baseline, err := pnpRemovalCreator(state, string(c.UID), c.Name, r.Operation.CreatedResourceID, nc.Generation)
		if err != nil || creator.Operation.ConfigGeneration > c.Generation {
			return nil, errConfigurationOwned
		}
		if _, exists := roots[baseline.ID]; exists {
			return nil, errConfigurationOwned
		}
		roots[baseline.ID] = pnpRemovalCohortEntry{Creator: *creator, Baseline: baseline}
	}
	removed := map[string]bool{}
	for _, r := range state.ActionApplications {
		if !pnpRemovalSelected(r.Operation.Resource) && !strings.HasPrefix(r.Resource, pnpRemovedPrefix) {
			continue
		}
		if !pnpRemovedReceiptPresent(state, &r.Operation) {
			return nil, errConfigurationOwned
		}
		if _, exists := roots[r.Operation.TargetID]; !exists || removed[r.Operation.TargetID] {
			return nil, errConfigurationOwned
		}
		removed[r.Operation.TargetID] = true
	}
	cohort := []pnpRemovalCohortEntry{}
	liveSerials := map[string]bool{}
	for id, entry := range roots {
		if removed[id] {
			continue
		}
		if liveSerials[entry.Baseline.Serial] {
			return nil, errConfigurationOwned
		}
		liveSerials[entry.Baseline.Serial] = true
		if !declared[entry.Baseline.Serial] {
			cohort = append(cohort, entry)
		}
	}
	sort.Slice(cohort, func(i, j int) bool { return cohort[i].Baseline.ID < cohort[j].Baseline.ID })
	// Reserve all pending absence receipts in the same bounded ledger. Existing
	// roots/tombstones and unrelated rows remain intact; no optimistic eviction.
	rows := append([]cvk.NetworkControllerConfigurationActionApplication(nil), state.ActionApplications...)
	if len(cohort) > 256-len(rows) {
		return nil, errPnPRegistrationCapacity
	}
	at := metav1.NewTime(time.Now().UTC().Truncate(time.Second))
	for _, entry := range cohort {
		resource := pnpRemovalJournalPrefix + entry.Baseline.ID + ":" + featureIdentityDigest(entry.Creator)
		digest := sha256.Sum256([]byte(resource))
		op := cvk.NetworkControllerConfigurationOperation{ID: fmt.Sprintf("%x", digest[:16]), Section: "inventory", Resource: resource, Action: "Delete", Phase: "Verified", TargetID: entry.Baseline.ID, ConfigName: c.Name, ConfigUID: string(c.UID), ConfigGeneration: c.Generation, ControllerGeneration: nc.Generation, SourceUID: string(s.uid), SourceVersion: s.version, SubmittedAt: &at}
		rows = append(rows, cvk.NetworkControllerConfigurationActionApplication{Section: "inventory", Resource: pnpRemovedKey(resource), Operation: op, VerifiedAt: &at})
	}
	if err := pnpRegistrationReceiptBytesFit(rows); err != nil {
		return nil, err
	}
	return cohort, nil
}

// Use the existing native eligibility/managed-serial checks for every pending
// record before the selected Delete. This cannot reserve external appliance
// activity or replace manager-owned physical identity admission.
func (c *client) observePnPRemovalCohort(ctx context.Context, cohort []pnpRemovalCohortEntry) error {
	rows, err := c.pnpRemovalDevices(ctx)
	if err != nil {
		return err
	}
	inventory, err := c.configurationDevices(ctx)
	if err != nil {
		return err
	}
	for _, entry := range cohort {
		b := entry.Baseline
		var selected *pnpRemovalDevice
		for _, row := range rows {
			if row.Device.ID == b.ID {
				copy := row
				selected = &copy
			}
		}
		if selected == nil || featureIdentityDigest(*selected) != b.NativeDigest {
			return errConfigurationOwned
		}
		payload, err := pnpRegistrationIdentityPayload(b.Serial, selected.Device.Info.Hostname, selected.Device.Info.PID, false)
		if err != nil || fmt.Sprintf("%x", sha256.Sum256(payload)) != b.CanonicalDigest {
			return errConfigurationOwned
		}
		expected := pnpRegistrationReceipt{deviceID: b.ID, serial: b.Serial, pid: selected.Device.Info.PID, hostname: selected.Device.Info.Hostname}
		fresh, err := validatePnPRemovalState(rows, inventory, expected, false)
		if err != nil {
			return err
		}
		if featureIdentityDigest(*fresh) != b.NativeDigest {
			return errConfigurationOwned
		}
	}
	return nil
}
