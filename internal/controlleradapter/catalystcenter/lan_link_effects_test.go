// Copyright 2026 Cisco Systems Inc.
// SPDX-License-Identifier: Apache-2.0
package catalystcenter

import (
	"context"
	"strings"
	"testing"
	"time"

	cvk "github.com/cisco/virtual-kubelet-cisco/api/v1alpha1"
	contract "github.com/cisco/virtual-kubelet-cisco/internal/controllerconfiguration"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// Synthetic later effects exercise format/history ordering only. No synthetic
// DELETE task/result is passed off as native or independent device evidence.
func TestLANLinkEffectHistoryOrdering(t *testing.T) {
	for _, mode := range []string{"add-delete-add", "latest-delete", "reordered", "tie", "duplicate-key", "duplicate-operation", "foreign-owner", "future-generation", "bad-delete-binding", "legacy", "publication-tie", "publication-after"} {
		t.Run(mode, func(t *testing.T) {
			a, config, _, _, _ := ownedLANDeleteFixture(t, true)
			nc, err := a.currentController(context.Background())
			if err != nil {
				t.Fatal(err)
			}
			first := nc.Status.Configuration.ActionApplications[0].DeepCopy()
			binding, err := validateLANLinkAddVerifiedReceipt(first, time.Now())
			if err != nil {
				t.Fatal(err)
			}
			base := first.VerifiedAt.Time
			config.Generation = 3
			deleted := first.DeepCopy()
			deleted.Operation.ID = strings.Repeat("b", 32)
			decl, result := strings.Repeat("c", 64), strings.Repeat("d", 64)
			deleted.Operation.Resource = lanLinkDeletePrefix + binding.logical + ":" + strings.Repeat("1", 64) + ":" + strings.Repeat("2", 64) + ":" + decl + ":" + strings.Repeat("3", 64)
			deleted.Operation.ConfigGeneration = 2
			deleted.Operation.TaskID = "delete-task"
			deleted.Operation.SubmittedAt = &metav1.Time{Time: base.Add(time.Second)}
			deleted.VerifiedAt = &metav1.Time{Time: base.Add(2 * time.Second)}
			deleted.Resource = "lan-link-delete:" + binding.logical + ":" + decl + ":" + result
			withGroup, err := lanHistoryShape(&deleted.Operation, first.Operation.PhysicalGroup.Targets)
			if err != nil {
				t.Fatal(err)
			}
			deleted.Operation = *withGroup
			readded := first.DeepCopy()
			readded.Operation.ID = strings.Repeat("e", 32)
			readded.Operation.ConfigGeneration = 3
			readded.Operation.TaskID = "readd-task"
			readded.Operation.SubmittedAt = &metav1.Time{Time: base.Add(3 * time.Second)}
			readded.VerifiedAt = &metav1.Time{Time: base.Add(4 * time.Second)}
			readded.Resource = "lan-link:" + binding.logical + ":" + strings.Repeat("f", 64)
			withGroup, err = lanHistoryShape(&readded.Operation, first.Operation.PhysicalGroup.Targets)
			if err != nil {
				t.Fatal(err)
			}
			readded.Operation = *withGroup
			nc.Status.Configuration.ActionApplications = []cvk.NetworkControllerConfigurationActionApplication{*first, *deleted, *readded}
			switch mode {
			case "latest-delete", "publication-tie", "publication-after":
				nc.Status.Configuration.ActionApplications = nc.Status.Configuration.ActionApplications[:2]
			case "reordered":
				nc.Status.Configuration.ActionApplications = []cvk.NetworkControllerConfigurationActionApplication{*readded, *first, *deleted}
			case "tie":
				readded.VerifiedAt = deleted.VerifiedAt.DeepCopy()
				nc.Status.Configuration.ActionApplications[2] = *readded
			case "duplicate-key":
				nc.Status.Configuration.ActionApplications[2].Resource = first.Resource
			case "duplicate-operation":
				nc.Status.Configuration.ActionApplications[2].Operation = *first.Operation.DeepCopy()
			case "foreign-owner":
				nc.Status.Configuration.ActionApplications[0].Operation.ConfigUID = "foreign"
			case "future-generation":
				config.Generation = 2
			case "bad-delete-binding":
				nc.Status.Configuration.ActionApplications[1].Resource = "lan-link-delete:" + binding.logical + ":" + strings.Repeat("0", 64) + ":" + result
			case "legacy":
				nc.Status.Configuration.ActionApplications[0].Resource = "lan-link:" + binding.logical
			}
			now := base.Add(10 * time.Second)
			latest, err := latestLANLinkEffect(nc, config, binding.logical, now)
			success := mode == "add-delete-add" || mode == "latest-delete" || mode == "reordered" || strings.HasPrefix(mode, "publication-")
			if (err == nil) != success {
				t.Fatal("unexpected ordered history", err)
			}
			if !success {
				return
			}
			if mode == "latest-delete" || strings.HasPrefix(mode, "publication-") {
				if !latest.deleted {
					t.Fatal("older ADD suppressed DELETE")
				}
			} else if latest.deleted || latest.receipt.Operation.ID != readded.Operation.ID {
				t.Fatal("obsolete effect selected")
			}
			if strings.HasPrefix(mode, "publication-") {
				if mode == "publication-tie" {
					readded.VerifiedAt = deleted.VerifiedAt.DeepCopy()
				}
				err = validateLANLinkEffectPublication(nc, config, readded, now)
				if (err == nil) != (mode == "publication-after") {
					t.Fatal("non-monotonic effect publication", err)
				}
			}
		})
	}
}

// Construct only a synthetic format fixture through the same Prepared digest
// constructor; no persisted accepted operation is rewritten.
func lanHistoryShape(op *cvk.NetworkControllerConfigurationOperation, targets []contract.GroupTargetIdentity) (*cvk.NetworkControllerConfigurationOperation, error) {
	prepared := op.DeepCopy()
	prepared.Phase = "Prepared"
	prepared.TaskID = ""
	prepared.SubmittedAt = nil
	prepared.PhysicalGroup = nil
	result, err := contract.WithGroupIdentity(prepared, targets)
	if err != nil {
		return nil, err
	}
	result.Phase = op.Phase
	result.TaskID = op.TaskID
	result.SubmittedAt = op.SubmittedAt.DeepCopy()
	return result, nil
}
