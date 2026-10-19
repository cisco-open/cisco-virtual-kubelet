// Copyright 2026 Cisco Systems Inc.
// SPDX-License-Identifier: Apache-2.0
package catalystcenter

import (
	"strings"
	"time"

	configapi "github.com/cisco/virtual-kubelet-cisco/api/config/v1alpha1"
	cvk "github.com/cisco/virtual-kubelet-cisco/api/v1alpha1"
)

type lanLinkEffect struct {
	deleted                      bool
	logical, declaration, result string
	receipt                      *cvk.NetworkControllerConfigurationActionApplication
}

func parseLANLinkEffect(receipt *cvk.NetworkControllerConfigurationActionApplication, now time.Time) (*lanLinkEffect, error) {
	if receipt == nil || receipt.Section != "lan_automation" || receipt.Operation.Phase != "Verified" || receipt.VerifiedAt == nil || receipt.VerifiedAt.IsZero() || receipt.Operation.SubmittedAt == nil || receipt.VerifiedAt.Before(receipt.Operation.SubmittedAt) || receipt.VerifiedAt.After(now) {
		return nil, errConfigurationStale
	}
	if strings.HasPrefix(receipt.Resource, "lan-link-delete:") {
		p, err := lanLinkHexParts(receipt.Resource, "lan-link-delete:", 3)
		if err != nil {
			return nil, err
		}
		op := receipt.Operation.DeepCopy()
		op.Phase = "Verifying"
		b, err := validateLANLinkDeleteCompletionOperation(op, now)
		if err != nil || b.logical != p[0] || b.declaration != p[1] {
			return nil, errConfigurationStale
		}
		return &lanLinkEffect{true, p[0], p[1], p[2], receipt.DeepCopy()}, nil
	}
	p, err := lanLinkHexParts(receipt.Resource, "lan-link:", 2)
	if err != nil {
		return nil, err
	}
	b, err := validateLANLinkAddVerifiedReceipt(receipt, now)
	if err != nil || b.logical != p[0] {
		return nil, errConfigurationStale
	}
	return &lanLinkEffect{false, p[0], "", p[1], receipt.DeepCopy()}, nil
}

// Existing opaque action receipts form an ordered effect history. Native
// presence alone is never ownership. Ties, duplicates and malformed history
// cannot choose a predecessor for another mutation.
func latestLANLinkEffect(nc *cvk.NetworkController, config *configapi.NetworkControllerConfig, logical string, now time.Time) (*lanLinkEffect, error) {
	if nc == nil || nc.Status.Configuration == nil {
		return nil, nil
	}
	var latest *lanLinkEffect
	keys, times, operations := map[string]bool{}, map[int64]bool{}, map[string]bool{}
	for i := range nc.Status.Configuration.ActionApplications {
		r := &nc.Status.Configuration.ActionApplications[i]
		if r.Section != "lan_automation" || !(r.Resource == "lan-link:"+logical || strings.HasPrefix(r.Resource, "lan-link:"+logical+":") || strings.HasPrefix(r.Resource, "lan-link-delete:"+logical+":")) {
			continue
		}
		effect, err := parseLANLinkEffect(r, now)
		if err != nil {
			return nil, err
		}
		if r.Operation.ConfigUID != string(config.UID) || r.Operation.ConfigName != config.Name || r.Operation.ConfigGeneration > config.Generation || r.Operation.ControllerGeneration > nc.Generation {
			return nil, errConfigurationOwned
		}
		at := r.VerifiedAt.Unix()
		if keys[r.Resource] || times[at] || operations[r.Operation.ID] {
			return nil, errConfigurationOwned
		}
		keys[r.Resource], times[at], operations[r.Operation.ID] = true, true, true
		if latest == nil || latest.receipt.VerifiedAt.Before(r.VerifiedAt) {
			latest = effect
		}
	}
	return latest, nil
}

func validateLANLinkEffectPublication(nc *cvk.NetworkController, config *configapi.NetworkControllerConfig, receipt *cvk.NetworkControllerConfigurationActionApplication, now time.Time) error {
	effect, err := parseLANLinkEffect(receipt, now)
	if err != nil {
		return err
	}
	latest, err := latestLANLinkEffect(nc, config, effect.logical, now)
	if err != nil {
		return err
	}
	if latest != nil && (!receipt.VerifiedAt.After(latest.receipt.VerifiedAt.Time) || latest.receipt.VerifiedAt.After(receipt.Operation.SubmittedAt.Time)) {
		return errConfigurationBusy
	}
	return nil
}
