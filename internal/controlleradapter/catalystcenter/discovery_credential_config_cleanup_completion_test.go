// Copyright 2026 Cisco Systems Inc.
// SPDX-License-Identifier: Apache-2.0
package catalystcenter

import (
	"context"
	"encoding/json"
	"errors"
	"reflect"
	"slices"
	"strings"
	"testing"

	configapi "github.com/cisco/virtual-kubelet-cisco/api/config/v1alpha1"
	cvk "github.com/cisco/virtual-kubelet-cisco/api/v1alpha1"
	"github.com/cisco/virtual-kubelet-cisco/internal/controller"
	core "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	ctrl "sigs.k8s.io/controller-runtime"
	ctrlclient "sigs.k8s.io/controller-runtime/pkg/client"
)

var credentialCleanupCompletionModes = []string{"inline", "configmap", "active", "after-source-loss", "after-source-change", "completion-source-gone", "completion-native-return", "completion-pruned-pair", "completion-pending", "completion-lost-ack", "completion-conflict"}

func TestDiscoveryCredentialConfigCleanupCompletion(t *testing.T) {
	for _, mode := range credentialCleanupCompletionModes {
		t.Run(mode, func(t *testing.T) { discoveryCredentialCleanupObservationScenario(t, "complete/"+mode, nil) })
	}
}

type credentialCompletionFaultClient struct {
	ctrlclient.Client
	mode string
	hits *int
}
type credentialCompletionFaultWriter struct {
	ctrlclient.SubResourceWriter
	owner credentialCompletionFaultClient
}

func (c credentialCompletionFaultClient) Status() ctrlclient.SubResourceWriter {
	return credentialCompletionFaultWriter{c.Client.Status(), c}
}
func (w credentialCompletionFaultWriter) Update(ctx context.Context, obj ctrlclient.Object, opts ...ctrlclient.SubResourceUpdateOption) error {
	c, ok := obj.(*configapi.NetworkControllerConfig)
	if !ok || *w.owner.hits > 0 {
		return w.SubResourceWriter.Update(ctx, obj, opts...)
	}
	cond := meta.FindStatusCondition(c.Status.Conditions, discoveryConfigCleanupCondition)
	if cond == nil || cond.Status != metav1.ConditionTrue {
		return w.SubResourceWriter.Update(ctx, obj, opts...)
	}
	*w.owner.hits++
	if w.owner.mode == "completion-conflict" {
		return errors.New("rejected completion proof write")
	}
	if err := w.SubResourceWriter.Update(ctx, obj, opts...); err != nil {
		return err
	}
	return errors.New("lost completion proof acknowledgement")
}

func exerciseDiscoveryCredentialCompletion(t *testing.T, a *adapter, c *configapi.NetworkControllerConfig, mode string, present *bool, cm *core.ConfigMap, gets *int) {
	t.Helper()
	ctx := context.Background()
	key := ctrlclient.ObjectKeyFromObject(c)
	admin := a.statusWriter
	before := journalState(t, a).DeepCopy()
	nc, err := a.currentController(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if discoveryDeletionCompleted(nc, c) {
		t.Fatal("manager trusted task completion without worker attestation")
	}
	if mode == "completion-source-gone" {
		if err := admin.Delete(ctx, cm); err != nil {
			t.Fatal(err)
		}
	}
	blocked := mode == "completion-native-return" || mode == "completion-pruned-pair" || mode == "completion-pending"
	if mode == "completion-native-return" {
		*present = true
	}
	if mode == "completion-pruned-pair" || mode == "completion-pending" {
		nc, err = a.currentController(ctx)
		if err != nil {
			t.Fatal(err)
		}
		if mode == "completion-pruned-pair" {
			nc.Status.Configuration.CredentialApplications = nil
		} else {
			op := nc.Status.Configuration.ActionApplications[1].Operation.DeepCopy()
			op.Phase = "Prepared"
			op.TaskID = ""
			op.SubmittedAt = nil
			nc.Status.Configuration.Operation = op
		}
		if err := admin.Status().Update(ctx, nc); err != nil {
			t.Fatal(err)
		}
		before = journalState(t, a).DeepCopy()
	}
	hits := 0
	worker := statusOnlyFeatureClient{Client: admin}
	a.statusWriter = worker
	if mode == "completion-lost-ack" || mode == "completion-conflict" {
		a.statusWriter = credentialCompletionFaultClient{worker, mode, &hits}
	}
	defer func() { a.statusWriter = admin }()
	err = a.publishDiscoveryCredentialConfigCompletion(ctx, c)
	if blocked {
		if err == nil {
			t.Fatal("unsafe native/evidence closure was attested")
		}
		nc, readErr := a.currentController(ctx)
		if readErr != nil {
			t.Fatal(readErr)
		}
		if discoveryDeletionCompleted(nc, c) || !hasDiscoveryConfigFinalizer(c) || !reflect.DeepEqual(before, journalState(t, a)) {
			t.Fatal("unsafe completion released or changed audit")
		}
		return
	}
	if mode == "completion-lost-ack" || mode == "completion-conflict" {
		if err == nil || hits != 1 {
			t.Fatal("completion acknowledgement fault not exercised", err, hits)
		}
		if err := a.statusReader.Get(ctx, key, c); err != nil {
			t.Fatal(err)
		}
		if mode == "completion-conflict" {
			nc, readErr := a.currentController(ctx)
			if readErr != nil {
				t.Fatal(readErr)
			}
			if discoveryDeletionCompleted(nc, c) {
				t.Fatal("manager trusted rejected completion write")
			}
			if err := a.publishDiscoveryCredentialConfigCompletion(ctx, c); err != nil {
				t.Fatal("completion publication retry", err)
			}
		}
	} else if err != nil {
		t.Fatal("completion publication", err)
	}
	if err := a.statusReader.Get(ctx, key, c); err != nil {
		t.Fatal(err)
	}
	nc, err = a.currentController(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if !discoveryDeletionCompleted(nc, c) || !reflect.DeepEqual(before, journalState(t, a)) || len(c.Finalizers) != 2 || c.Status.Phase != configapi.NetworkControllerConfigPhaseWaiting {
		t.Fatal("completed typed audit was not independently recognized", c.Status)
	}
	if _, err := decodeDiscoveryRetirement(nc); err == nil {
		t.Fatal("ordinary handoff accepted typed credential history")
	}
	record, _, err := discoveryCredentialRetiredHandoffRecord(nc)
	if err != nil || record.CredentialHistoryDigest != discoveryCredentialHistorySealPrefix+discoveryCredentialAuditDigest(nc.Status.Configuration, "") {
		t.Fatal("new credential closure did not seal its complete typed audit", err)
	}
	if mode == "inline" {
		legacy := nc.DeepCopy()
		copy := *record
		copy.CredentialHistoryDigest = ""
		raw, _ := json.Marshal(copy)
		meta.FindStatusCondition(legacy.Status.Conditions, discoveryRetirementCondition).Message = string(raw)
		if !discoveryDeletionCompleted(legacy, c) {
			t.Fatal("legacy single-owner closure lost read-only compatibility")
		}
		cases := []struct {
			name   string
			change func(*cvk.NetworkController, *configapi.NetworkControllerConfig)
		}{
			{"history-seal", func(n *cvk.NetworkController, _ *configapi.NetworkControllerConfig) {
				condition := meta.FindStatusCondition(n.Status.Conditions, discoveryRetirementCondition)
				var r discoveryOwnershipRetirement
				json.Unmarshal([]byte(condition.Message), &r)
				r.CredentialHistoryDigest = discoveryCredentialHistorySealPrefix + strings.Repeat("a", 64)
				raw, _ := json.Marshal(r)
				condition.Message = string(raw)
			}},
			{"history-seal-version", func(n *cvk.NetworkController, _ *configapi.NetworkControllerConfig) {
				condition := meta.FindStatusCondition(n.Status.Conditions, discoveryRetirementCondition)
				var r discoveryOwnershipRetirement
				json.Unmarshal([]byte(condition.Message), &r)
				r.CredentialHistoryDigest = "credential-discovery-history:v99:" + strings.Repeat("a", 64)
				raw, _ := json.Marshal(r)
				condition.Message = string(raw)
			}},
			{"owner", func(n *cvk.NetworkController, _ *configapi.NetworkControllerConfig) {
				n.Status.Configuration.Owners[0].ConfigUID = "foreign"
			}},
			{"credential-pair", func(n *cvk.NetworkController, _ *configapi.NetworkControllerConfig) {
				n.Status.Configuration.CredentialApplications = nil
			}},
			{"retirement-receipt", func(n *cvk.NetworkController, _ *configapi.NetworkControllerConfig) {
				n.Status.Configuration.ActionApplications = n.Status.Configuration.ActionApplications[:1]
			}},
			{"completion-condition", func(_ *cvk.NetworkController, c *configapi.NetworkControllerConfig) {
				meta.RemoveStatusCondition(&c.Status.Conditions, discoveryConfigCleanupCondition)
			}},
			{"retirement-condition", func(n *cvk.NetworkController, _ *configapi.NetworkControllerConfig) {
				meta.RemoveStatusCondition(&n.Status.Conditions, discoveryRetirementCondition)
			}},
			{"controller-generation", func(n *cvk.NetworkController, _ *configapi.NetworkControllerConfig) { n.Generation++ }},
			{"config-uid", func(_ *cvk.NetworkController, c *configapi.NetworkControllerConfig) { c.UID = "foreign" }},
			{"family-conflict", func(_ *cvk.NetworkController, c *configapi.NetworkControllerConfig) {
				c.Finalizers = append(c.Finalizers, featureConfigFinalizer)
			}},
			{"paused", func(n *cvk.NetworkController, _ *configapi.NetworkControllerConfig) { n.Spec.Paused = true }},
			{"undeleting", func(_ *cvk.NetworkController, c *configapi.NetworkControllerConfig) { c.DeletionTimestamp = nil }},
			{"source-fields", func(_ *cvk.NetworkController, c *configapi.NetworkControllerConfig) {
				cond := meta.FindStatusCondition(c.Status.Conditions, discoveryConfigCleanupCondition)
				var proof discoveryConfigCompletion
				json.Unmarshal([]byte(cond.Message), &proof)
				proof.SourceUID = "foreign"
				raw, _ := json.Marshal(proof)
				cond.Message = string(raw)
			}},
			{"digest", func(_ *cvk.NetworkController, c *configapi.NetworkControllerConfig) {
				cond := meta.FindStatusCondition(c.Status.Conditions, discoveryConfigCleanupCondition)
				var proof discoveryConfigCompletion
				json.Unmarshal([]byte(cond.Message), &proof)
				proof.CredentialDigest = "unknown:v1:"
				raw, _ := json.Marshal(proof)
				cond.Message = string(raw)
			}},
		}
		reads := *gets
		for _, test := range cases {
			t.Run(test.name, func(t *testing.T) {
				n, copy := nc.DeepCopy(), c.DeepCopy()
				test.change(n, copy)
				if discoveryDeletionCompleted(n, copy) {
					t.Fatal("manager trusted altered completion evidence")
				}
			})
		}
		if *gets != reads {
			t.Fatal("manager validation called controller API")
		}
	}
	reads := *gets
	manager := &controller.NetworkControllerReconciler{Client: discoveryLifecycleIndexedClient{admin}, APIReader: a.statusReader}
	for i := 0; i < 4 && hasDiscoveryConfigFinalizer(c); i++ {
		if _, err := manager.Reconcile(ctx, ctrl.Request{NamespacedName: a.key}); err != nil {
			t.Fatal("manager release", err)
		}
		if err := a.statusReader.Get(ctx, key, c); err != nil {
			t.Fatal(err)
		}
	}
	if hasDiscoveryConfigFinalizer(c) || len(c.Finalizers) != 1 || !slices.Contains(c.Finalizers, "example.com/keeper") || *gets != reads || !reflect.DeepEqual(before, journalState(t, a)) {
		var current cvk.NetworkController
		_ = a.statusReader.Get(ctx, a.key, &current)
		t.Fatal("manager failed to release only its finalizer without I/O or audit loss", c.Finalizers, "worker generation", a.generation, "current generation", current.Generation, "proof valid", discoveryDeletionCompleted(&current, c))
	}
}
