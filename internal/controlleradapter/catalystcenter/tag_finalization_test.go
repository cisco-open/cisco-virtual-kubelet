// Copyright 2026 Cisco Systems Inc.
// SPDX-License-Identifier: Apache-2.0
package catalystcenter

import (
	"context"
	configapi "github.com/cisco/virtual-kubelet-cisco/api/config/v1alpha1"
	cvk "github.com/cisco/virtual-kubelet-cisco/api/v1alpha1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"strings"
	"testing"
	"time"
)

func TestTagFinalizationBoundInputs(t *testing.T) {
	for _, section := range []string{"inventory", "templates", "combined"} {
		for _, mode := range []string{"valid", "alive", "future-deletion", "no-finalizer", "family-conflict", "changed-uid", "changed-spec", "changed-deletion", "changed-raw", "projected-key", "projected-namespace", "projected-uid", "projected-version", "assets", "secret-snapshot", "other-context", "wrong-deletion-token", "wrong-spec-digest", "ordinary-prune", "wrong-action", "bad-source-mode", "trailing-part"} {
			t.Run(section+"/"+mode, func(t *testing.T) {
				_, c := journalFixture(t)
				c.Spec.ManagedSections = []string{section}
				if section == "combined" {
					c.Spec.ManagedSections = []string{"inventory", "templates"}
				}
				c.Spec.DeletionPolicy = configapi.NetworkControllerRetentionPolicyDelete
				c.Finalizers = []string{tagCleanupFinalizer(c)}
				at := metav1.NewTime(time.Now().Add(-time.Second))
				c.DeletionTimestamp = &at
				source := &configSourceSnapshot{raw: tagDeletionEmptySource(c), tagDeletion: tagDeletionContext(c, true)}
				if source.tagDeletion == nil {
					t.Fatal("context fixture rejected")
				}
				resource := tagFinalizePrefix + strings.Repeat("a", 64) + ":" + strings.Repeat("b", 64) + ":i:" + source.tagDeletion.DeletionToken + ":" + source.tagDeletion.SpecDigest
				action := "Delete"
				switch mode {
				case "alive":
					c.DeletionTimestamp = nil
				case "future-deletion":
					v := metav1.NewTime(time.Now().Add(time.Hour))
					c.DeletionTimestamp = &v
				case "no-finalizer":
					c.Finalizers = nil
				case "family-conflict":
					c.Finalizers = append(c.Finalizers, featureConfigFinalizer)
				case "changed-uid":
					c.UID = "another"
				case "changed-spec":
					c.Spec.TaskTimeout = &metav1.Duration{Duration: time.Minute}
				case "changed-deletion":
					v := metav1.NewTime(at.Add(-time.Second))
					c.DeletionTimestamp = &v
				case "changed-raw":
					source.raw = []byte(`{"catalyst_center":{"inventory":{"tags":[{"name":"unexpected"}]}}}`)
				case "projected-key":
					source.key.Name = "source"
				case "projected-namespace":
					source.key.Namespace = "source"
				case "projected-uid":
					source.uid = "uid"
				case "projected-version":
					source.version = "revision"
				case "assets":
					source.templateFiles = map[string]string{"file": "contents"}
				case "secret-snapshot":
					source.secrets = &configurationSecretSnapshot{}
				case "other-context":
					source.deletion = &featureDeletionSource{}
				case "wrong-deletion-token":
					resource = strings.Replace(resource, source.tagDeletion.DeletionToken, strings.Repeat("c", 64), 1)
				case "wrong-spec-digest":
					resource = strings.Replace(resource, source.tagDeletion.SpecDigest, strings.Repeat("d", 64), 1)
				case "ordinary-prune":
					resource = tagDeleteCanonical(resource)
				case "wrong-action":
					action = "Create"
				case "bad-source-mode":
					resource = strings.Replace(resource, ":i:", ":c:", 1)
				case "trailing-part":
					resource += ":extra"
				}
				if tagFinalizationMutationAllowed(c, source, resource, action) != (mode == "valid") {
					t.Fatal("wrong finalization authority", mode)
				}
			})
		}
	}
}

func TestTagFinalizationCannotBypassOrdinaryJournal(t *testing.T) {
	ctx := context.Background()
	a, c := journalFixture(t)
	c.Spec.ManagedSections = []string{"inventory"}
	if err := a.statusWriter.Update(ctx, c); err != nil {
		t.Fatal(err)
	}
	source, err := loadControllerSource(ctx, a.statusReader, c)
	if err != nil {
		t.Fatal(err)
	}
	resource := tagFinalizePrefix + strings.Repeat("a", 64) + ":" + strings.Repeat("b", 64) + ":i:" + strings.Repeat("c", 64) + ":" + strings.Repeat("d", 64)
	if _, err := a.prepareConfiguration(ctx, c, source, "inventory", resource, "Delete", configurationResourceIdentity{target: "22f33fa0-e275-41c9-a50d-d7e5135adb8c"}); err == nil {
		t.Fatal("live CR prepared finalization")
	}
	nc, err := a.currentController(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if nc.Status.Configuration != nil && nc.Status.Configuration.Operation != nil {
		t.Fatal("refused finalization consumed journal")
	}
}

func TestTagFinalizationReusesReceiptVerification(t *testing.T) {
	creator := tagTestReceipt(true)
	full := strings.Repeat("e", 64)
	creator.Resource += ":s:" + full
	key, _, _ := tagOwnedParts(&creator.Operation)
	op := *creator.Operation.DeepCopy()
	op.ID = strings.Repeat("f", 32)
	op.Action = "Delete"
	op.TargetID = "22f33fa0-e275-41c9-a50d-d7e5135adb8c"
	op.TaskID = "delete-task"
	at := metav1.NewTime(time.Now().Add(-time.Second))
	done := metav1.Now()
	op.SubmittedAt = &at
	op.Resource = tagFinalizePrefix + key + ":" + full + ":i:" + strings.Repeat("a", 64) + ":" + strings.Repeat("b", 64)
	row := cvk.NetworkControllerConfigurationActionApplication{Section: op.Section, Resource: tagDeletedPrefix + key + ":" + op.TargetID, Operation: op, VerifiedAt: &done}
	state := &cvk.NetworkControllerConfigurationStatus{ActionApplications: []cvk.NetworkControllerConfigurationActionApplication{creator, row}}
	if !validTagDeleteOperation(&op) || !tagDeletedReceiptPresent(state, &op) {
		t.Fatal("bound finalization did not reuse exact receipt verifier")
	}
	state.ActionApplications[1].Operation.TaskID = creator.Operation.TaskID
	if tagDeletedReceiptPresent(state, &op) {
		t.Fatal("finalization accepted original creator task as delete evidence")
	}
}
