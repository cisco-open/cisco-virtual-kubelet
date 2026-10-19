// Copyright 2026 Cisco Systems Inc.
// SPDX-License-Identifier: Apache-2.0
package catalystcenter

import (
	"context"
	"encoding/json"
	"errors"
	configapi "github.com/cisco/virtual-kubelet-cisco/api/config/v1alpha1"
	cvk "github.com/cisco/virtual-kubelet-cisco/api/v1alpha1"
	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	ctrlclient "sigs.k8s.io/controller-runtime/pkg/client"
	"strings"
	"testing"
	"time"
)

// Reject or lose acknowledgement of the retirement status update only. Native
// requests and journal writes stay on the shared public-flow fixture.
type tagRetirementStatusClient struct {
	ctrlclient.Client
	reject    bool
	attempted int
	lost      bool
}

func (c *tagRetirementStatusClient) Status() ctrlclient.SubResourceWriter {
	return tagRetirementStatusWriter{SubResourceWriter: c.Client.Status(), owner: c}
}

type tagRetirementStatusWriter struct {
	ctrlclient.SubResourceWriter
	owner *tagRetirementStatusClient
}

func (w tagRetirementStatusWriter) Update(ctx context.Context, obj ctrlclient.Object, opts ...ctrlclient.SubResourceUpdateOption) error {
	if nc, ok := obj.(*cvk.NetworkController); ok {
		cond := meta.FindStatusCondition(nc.Status.Conditions, tagRetirementCondition)
		if cond != nil && cond.Status == metav1.ConditionTrue && cond.Reason == "TagConfigClosed" {
			w.owner.attempted++
			if w.owner.reject {
				return errors.New("retirement status update refused")
			}
			if !w.owner.lost {
				if err := w.SubResourceWriter.Update(ctx, obj, opts...); err != nil {
					return err
				}
				w.owner.lost = true
				return errors.New("retirement status acknowledgement lost")
			}
		}
	}
	return w.SubResourceWriter.Update(ctx, obj, opts...)
}

func tagRetirementFixture(t *testing.T) (*cvk.NetworkController, *configapi.NetworkControllerConfig) {
	t.Helper()
	a, c := journalFixture(t)
	c.Spec.ManagedSections = []string{"inventory"}
	c.Spec.DeletionPolicy = configapi.NetworkControllerRetentionPolicyDelete
	c.Finalizers = []string{discoveryConfigFinalizer}
	at := metav1.NewTime(time.Now().Add(-2 * time.Second))
	c.DeletionTimestamp = &at
	nc, err := a.currentController(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	creator := tagTestReceipt(true)
	creator.Operation.ConfigUID = string(c.UID)
	creator.Operation.ConfigName = c.Name
	full := strings.Repeat("e", 64)
	creator.Resource += ":s:" + full
	key, _, _ := tagOwnedParts(&creator.Operation)
	op := *creator.Operation.DeepCopy()
	op.ID = strings.Repeat("f", 32)
	op.Action = "Delete"
	op.TargetID = "22f33fa0-e275-41c9-a50d-d7e5135adb8c"
	op.TaskID = "original-delete-task"
	submit := metav1.NewTime(time.Now().Add(-time.Second))
	done := metav1.Now()
	op.SubmittedAt = &submit
	d := tagDeletionContext(c, true)
	op.Resource = tagFinalizePrefix + key + ":" + full + ":i:" + d.DeletionToken + ":" + d.SpecDigest
	deleted := cvk.NetworkControllerConfigurationActionApplication{Section: "inventory", Resource: tagDeletedPrefix + key + ":" + op.TargetID, Operation: op, VerifiedAt: &done}
	nc.Status.Configuration = &cvk.NetworkControllerConfigurationStatus{Owners: []cvk.NetworkControllerSectionOwner{{Section: "inventory", ConfigName: c.Name, ConfigUID: string(c.UID)}}, ActionApplications: []cvk.NetworkControllerConfigurationActionApplication{creator, deleted}}
	own, err := tagRetirementEvidence(nc, c)
	if err != nil {
		t.Fatal(err)
	}
	r := tagOwnershipRetirement{Protocol: tagRetirementProtocol, ModelVersion: "0.5.0", ConfigName: c.Name, Sections: []string{"inventory"}, Completion: featureConfigCompletionProof(nc, c), RetainedDigest: featureIdentityDigest(own)}
	raw, _ := json.Marshal(r)
	meta.SetStatusCondition(&nc.Status.Conditions, condition(tagRetirementCondition, true, "TagConfigClosed", string(raw), nc.Generation))
	proof, _ := json.Marshal(featureConfigCompletionProof(nc, c))
	meta.SetStatusCondition(&c.Status.Conditions, condition(featureConfigCleanupCondition, true, "TagConfigFinalized", string(proof), c.Generation))
	return nc, c
}

func TestTagOwnershipRetirementIntegrity(t *testing.T) {
	for _, mode := range []string{"valid", "missing", "false", "wrong-reason", "wrong-observed-generation", "noncanonical", "unknown-field", "wrong-protocol", "wrong-model", "wrong-controller", "wrong-controller-generation", "wrong-config", "wrong-config-generation", "wrong-name", "wrong-section", "duplicate-section", "invalid-digest", "wrong-retained-digest", "wrong-evidence", "retain-policy", "missing-owner", "duplicate-owner", "foreign-overlap", "missing-creator", "missing-delete", "changed-receipt", "duplicate-receipt", "credential-history", "active-operation", "paused", "deleting-controller"} {
		t.Run(mode, func(t *testing.T) {
			nc, c := tagRetirementFixture(t)
			cond := meta.FindStatusCondition(nc.Status.Conditions, tagRetirementCondition)
			var r tagOwnershipRetirement
			if err := json.Unmarshal([]byte(cond.Message), &r); err != nil {
				t.Fatal(err)
			}
			remarshal := true
			switch mode {
			case "missing":
				nc.Status.Conditions = nil
				remarshal = false
			case "false":
				cond.Status = metav1.ConditionFalse
			case "wrong-reason":
				cond.Reason = "Unqualified"
			case "wrong-observed-generation":
				cond.ObservedGeneration++
			case "noncanonical":
				cond.Message = " " + cond.Message
				remarshal = false
			case "unknown-field":
				cond.Message = strings.TrimSuffix(cond.Message, "}") + `,"unknown":true}`
				remarshal = false
			case "wrong-protocol":
				r.Protocol = "future"
			case "wrong-model":
				r.ModelVersion = "0.6.0"
			case "wrong-controller":
				r.Completion.ControllerUID = "other"
			case "wrong-controller-generation":
				r.Completion.ControllerGeneration++
			case "wrong-config":
				r.Completion.ConfigUID = "other"
			case "wrong-config-generation":
				r.Completion.ConfigGeneration = 0
			case "wrong-name":
				r.ConfigName = "other"
			case "wrong-section":
				r.Sections = []string{"templates"}
			case "duplicate-section":
				r.Sections = []string{"inventory", "inventory"}
			case "invalid-digest":
				r.Completion.SpecDigest = "not-a-digest"
			case "wrong-retained-digest":
				r.RetainedDigest = strings.Repeat("a", 64)
			case "wrong-evidence":
				r.Completion.EvidenceDigest = strings.Repeat("b", 64)
			case "retain-policy":
				r.Completion.Policy = "Retain"
			case "missing-owner":
				nc.Status.Configuration.Owners = nil
			case "duplicate-owner":
				nc.Status.Configuration.Owners = append(nc.Status.Configuration.Owners, nc.Status.Configuration.Owners[0])
			case "foreign-overlap":
				nc.Status.Configuration.Owners = append(nc.Status.Configuration.Owners, cvk.NetworkControllerSectionOwner{Section: "templates", ConfigName: "foreign", ConfigUID: "foreign"})
			case "missing-creator":
				nc.Status.Configuration.ActionApplications = nc.Status.Configuration.ActionApplications[1:]
			case "missing-delete":
				nc.Status.Configuration.ActionApplications = nc.Status.Configuration.ActionApplications[:1]
			case "changed-receipt":
				nc.Status.Configuration.ActionApplications[1].Operation.TaskID = "other-task"
			case "duplicate-receipt":
				nc.Status.Configuration.ActionApplications = append(nc.Status.Configuration.ActionApplications, nc.Status.Configuration.ActionApplications[1])
			case "credential-history":
				nc.Status.Configuration.CredentialApplications = []cvk.NetworkControllerConfigurationCredentialApplication{{ConfigUID: string(c.UID)}}
			case "active-operation":
				nc.Status.Configuration.Operation = &cvk.NetworkControllerConfigurationOperation{ConfigUID: "other"}
			case "paused":
				nc.Spec.Paused = true
			case "deleting-controller":
				v := metav1.Now()
				nc.DeletionTimestamp = &v
			}
			if remarshal {
				raw, _ := json.Marshal(r)
				cond.Message = string(raw)
			}
			_, _, err := tagRetirementRecord(nc)
			if (err == nil) != (mode == "valid") {
				t.Fatal("retirement witness validation mismatch", mode, err)
			}
			if mode == "valid" && !tagDeletionCompleted(nc, c) {
				t.Fatal("valid retirement did not permit completed cleanup")
			}
			if mode != "valid" && tagDeletionCompleted(nc, c) {
				t.Fatal("changed retirement authorized metadata release", mode)
			}
		})
	}
}

func TestTagOwnershipRetirementRetainCannotAuthorizeTransfer(t *testing.T) {
	nc, c := tagRetirementFixture(t)
	c.Spec.DeletionPolicy = configapi.NetworkControllerRetentionPolicyRetain
	proof, _ := json.Marshal(featureConfigCompletionProof(nc, c))
	meta.SetStatusCondition(&c.Status.Conditions, condition(featureConfigCleanupCondition, true, "TagConfigFinalized", string(proof), c.Generation))
	if tagDeletionCompleted(nc, c) {
		t.Fatal("Retain completion left active Delete retirement authority")
	}
	meta.SetStatusCondition(&nc.Status.Conditions, condition(tagRetirementCondition, false, "TagConfigRetained", "Retain policy does not retire ownership", nc.Generation))
	if !tagDeletionCompleted(nc, c) {
		t.Fatal("Retain completion required Delete authority")
	}
}
