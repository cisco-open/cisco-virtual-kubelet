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
	"k8s.io/apimachinery/pkg/runtime"
	"net/http"
	ctrlclient "sigs.k8s.io/controller-runtime/pkg/client"
	"sync/atomic"
	"testing"
)

type tagClaimFaultClient struct {
	ctrlclient.Client
	mode      string
	injected  bool
	transfers int
}

func (c *tagClaimFaultClient) Status() ctrlclient.SubResourceWriter {
	return tagClaimFaultWriter{SubResourceWriter: c.Client.Status(), client: c}
}

type tagClaimFaultWriter struct {
	ctrlclient.SubResourceWriter
	client *tagClaimFaultClient
}

func (w tagClaimFaultWriter) Update(ctx context.Context, obj ctrlclient.Object, opts ...ctrlclient.SubResourceUpdateOption) error {
	if n, ok := obj.(*cvk.NetworkController); ok {
		cond := meta.FindStatusCondition(n.Status.Conditions, tagRetirementCondition)
		if cond != nil && cond.Reason == "TagOwnershipHandedOff" {
			w.client.transfers++
			if !w.client.injected {
				w.client.injected = true
				if w.client.mode == "conflict" {
					var fresh cvk.NetworkController
					if err := w.client.Client.Get(ctx, ctrlclient.ObjectKeyFromObject(n), &fresh); err != nil {
						return err
					}
					meta.SetStatusCondition(&fresh.Status.Conditions, condition("FixtureConcurrentStatus", true, "Changed", "Concurrent controller status update", fresh.Generation))
					if err := w.SubResourceWriter.Update(ctx, &fresh, opts...); err != nil {
						return err
					}
					return w.SubResourceWriter.Update(ctx, obj, opts...)
				}
				if err := w.SubResourceWriter.Update(ctx, obj, opts...); err != nil {
					return err
				}
				return errors.New("claim acknowledgement lost")
			}
		}
	}
	return w.SubResourceWriter.Update(ctx, obj, opts...)
}

// Native reads here are a stateless empty catalog, not a second lifecycle
// simulator. The public multi-owner execution fixture covers native effects.
func TestTagHandoffClaimCASRecovery(t *testing.T) {
	for _, mode := range []string{"lost-ack", "conflict", "source-changed"} {
		t.Run(mode, func(t *testing.T) {
			ctx := context.Background()
			a, old := journalFixture(t)
			admin := a.statusWriter
			closed, prior := tagRetirementFixture(t)
			stored, err := a.currentController(ctx)
			if err != nil {
				t.Fatal(err)
			}
			stored.Status = *closed.Status.DeepCopy()
			if err := admin.Status().Update(ctx, stored); err != nil {
				t.Fatal(err)
			}
			if err := admin.Delete(ctx, old); err != nil {
				t.Fatal(err)
			}
			next := prior.DeepCopy()
			next.ObjectMeta = metav1.ObjectMeta{Name: "new-owner", Namespace: old.Namespace, UID: "new-owner", Generation: 1, Finalizers: []string{discoveryConfigFinalizer}}
			next.Status = configapi.NetworkControllerConfigStatus{}
			next.Spec.Source = configapi.NetworkControllerConfigurationSource{Inline: &runtime.RawExtension{Raw: []byte(`{"catalyst_center":{"inventory":{"tags":[]}}}`)}}
			if err := admin.Create(ctx, next); err != nil {
				t.Fatal(err)
			}
			var changed atomic.Bool
			a.client = testClient(t, func(w http.ResponseWriter, r *http.Request) {
				if r.URL.Path == authPath {
					json.NewEncoder(w).Encode(map[string]string{"Token": "test"})
					return
				}
				if r.Method != "GET" || r.URL.Path != tagsPath {
					t.Error("handoff attempted native mutation", r.Method, r.URL.Path)
					w.WriteHeader(403)
					return
				}
				if mode == "source-changed" && changed.CompareAndSwap(false, true) {
					var fresh configapi.NetworkControllerConfig
					if err := admin.Get(ctx, ctrlclient.ObjectKeyFromObject(next), &fresh); err != nil {
						t.Error(err)
					}
					fresh.Generation++
					fresh.Spec.DeletionPolicy = configapi.NetworkControllerRetentionPolicyRetain
					if err := admin.Update(ctx, &fresh); err != nil {
						t.Error(err)
					}
				}
				json.NewEncoder(w).Encode(map[string]any{"response": []any{}})
			})
			faults := &tagClaimFaultClient{Client: statusOnlyFeatureClient{Client: admin}, mode: mode}
			a.statusWriter = faults
			source, err := loadControllerSource(ctx, a.statusReader, next)
			if err != nil {
				t.Fatal(err)
			}
			before, err := a.currentController(ctx)
			if err != nil {
				t.Fatal(err)
			}
			handled, firstErr := a.handoffTagOwnership(ctx, before, next, source)
			if !handled || firstErr == nil {
				t.Fatal("fault did not fence first transfer", mode, handled, firstErr)
			}
			current, err := a.currentController(ctx)
			if err != nil {
				t.Fatal(err)
			}
			mine := configurationOwnersMatch(current.Status.Configuration, next) == nil
			if mode == "source-changed" {
				if mine || !changed.Load() || faults.transfers != 0 {
					t.Fatal("stale source transferred ownership")
				}
				return
			}
			if (mode == "lost-ack") != mine {
				t.Fatal("CAS outcome was misclassified", mode, mine)
			}
			handled, err = a.handoffTagOwnership(ctx, current, next, source)
			if err != nil {
				t.Fatal("durable outcome did not recover", mode, err)
			}
			if mode == "lost-ack" && (handled || faults.transfers != 1) {
				t.Fatal("lost claim acknowledgement repeated transfer", handled, faults.transfers)
			}
			if mode == "conflict" && (!handled || faults.transfers != 2) {
				t.Fatal("status conflict did not retry fresh guarded transfer")
			}
			current, err = a.currentController(ctx)
			if err != nil {
				t.Fatal(err)
			}
			if configurationOwnersMatch(current.Status.Configuration, next) != nil || tagOwnerCustodyValid(current, next) != nil {
				t.Fatal("recovered claim lacks sealed custody")
			}
			if current.Status.Configuration.Operation != nil {
				t.Fatal("claim-only transfer created native dispatch authority")
			}
		})
	}
}
