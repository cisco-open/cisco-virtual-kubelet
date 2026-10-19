// Copyright 2026 Cisco Systems Inc.
// SPDX-License-Identifier: Apache-2.0
package controllerconfiguration

import (
	"context"
	"reflect"
	"strings"
	"testing"
	"time"

	config "github.com/cisco/virtual-kubelet-cisco/api/config/v1alpha1"
	ops "github.com/cisco/virtual-kubelet-cisco/api/ops/v1alpha1"
	cvk "github.com/cisco/virtual-kubelet-cisco/api/v1alpha1"
	core "k8s.io/api/core/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

type metadataWorkerSourceRaceReader struct {
	client.Reader
	pods int
}

func (r *metadataWorkerSourceRaceReader) Get(ctx context.Context, key client.ObjectKey, obj client.Object, opts ...client.GetOption) error {
	if err := r.Reader.Get(ctx, key, obj, opts...); err != nil {
		return err
	}
	if _, ok := obj.(*core.Pod); ok {
		r.pods++
	}
	if intent, ok := obj.(*config.NetworkControllerConfig); ok && r.pods > 0 {
		intent.Generation++
	}
	return nil
}
func TestMetadataScopeWorker(t *testing.T) {
	for _, mode := range []string{"valid", "missing-worker", "overlap", "unready", "wrong-args", "late-source", "late-deployment", "late-replicaSet", "late-pod", "late-controller", "zero-time"} {
		t.Run(mode, func(t *testing.T) {
			ctx := context.Background()
			c, nc, _ := bootstrapFenceFixture(t)
			_ = ops.AddToScheme(c.Scheme())
			nc.Status.Configuration.Operation.Action = "Delete"
			nc.Status.Configuration.Operation.TargetSerial = ""
			nc.Status.Configuration.Operation.Resource = "pnp-remove:v2:native-pnp:" + strings.Repeat("b", 64)
			if err := c.Status().Update(ctx, nc); err != nil {
				t.Fatal(err)
			}
			if err := c.Get(ctx, client.ObjectKeyFromObject(nc), nc); err != nil {
				t.Fatal(err)
			}
			var pod core.Pod
			if err := c.Get(ctx, client.ObjectKey{Namespace: nc.Namespace, Name: "worker"}, &pod); err != nil {
				t.Fatal(err)
			}
			switch mode {
			case "missing-worker":
				if err := c.Delete(ctx, &pod); err != nil {
					t.Fatal(err)
				}
			case "overlap":
				other := pod.DeepCopy()
				other.Name = "overlap"
				other.UID = "overlap"
				other.ResourceVersion = ""
				if err := c.Create(ctx, other); err != nil {
					t.Fatal(err)
				}
			case "unready":
				pod.Status.Conditions = nil
				if err := c.Status().Update(ctx, &pod); err != nil {
					t.Fatal(err)
				}
			case "wrong-args":
				pod.Spec.Containers[0].Args = append(pod.Spec.Containers[0].Args, "--controller-uid=foreign")
				if err := c.Update(ctx, &pod); err != nil {
					t.Fatal(err)
				}
			}
			var reader client.Reader = &bootstrapWorkerRaceReader{Reader: c, mode: mode, reads: map[string]int{}}
			if mode == "late-source" {
				reader = &metadataWorkerSourceRaceReader{Reader: c}
			}
			now := time.Now()
			if mode == "zero-time" {
				now = time.Time{}
			}
			before := nc.DeepCopy()
			input := []string{"second123", "bootstrap123"}
			observation, err := ReadMetadataScopeWorker(ctx, reader, nc, "leases", input, now)
			if (err == nil) != (mode == "valid") {
				t.Fatal("wrong actor observation", err)
			}
			if mode != "valid" {
				if observation != nil {
					t.Fatal("unproven actor returned")
				}
				return
			}
			if observation.Pod.UID != "pod-uid" || observation.Deployment.UID != "deployment-uid" || observation.ReplicaSet.UID != "replica-uid" || observation.OperationID != nc.Status.Configuration.Operation.ID || len(observation.BindingDigest) != 64 || !reflect.DeepEqual(observation.PhysicalIdentities, []string{"bootstrap123", "second123"}) {
				t.Fatal("incomplete actor/cohort binding")
			}
			repeat, err := ReadMetadataScopeWorker(ctx, c, nc, "leases", []string{"bootstrap123", "second123"}, now)
			if err != nil || !reflect.DeepEqual(observation, repeat) {
				t.Fatal("canonical repeated observation changed", err)
			}
			if !reflect.DeepEqual(before, nc) || input[0] != "second123" {
				t.Fatal("input snapshot changed")
			}

			// The handoff seals this witness; hashing that field itself would
			// make every retained seal change its own binding.
			sealed := nc.DeepCopy()
			sealed.Status.Configuration.MetadataHandoff = &cvk.NetworkControllerConfigurationMetadataHandoff{Protocol: cvk.NetworkControllerMetadataHandoffProtocol, BindingDigest: repeat.BindingDigest, LeaseNamespace: "leases", WorkerUsername: repeat.Username, WorkerPod: repeat.Pod, PhysicalIdentities: append([]string(nil), repeat.PhysicalIdentities...), Operation: *sealed.Status.Configuration.Operation.DeepCopy()}
			if err := c.Status().Update(ctx, sealed); err != nil {
				t.Fatal(err)
			}
			witness, err := ReadMetadataScopeWorker(ctx, c, sealed, "leases", input, now)
			if err != nil || witness.BindingDigest != repeat.BindingDigest {
				t.Fatal("handoff seal changed its own scope digest", err)
			}
			observation.PhysicalIdentities[0] = "foreign"
			if repeat.PhysicalIdentities[0] != "bootstrap123" {
				t.Fatal("returned mutable alias")
			}
			var current cvk.NetworkController
			if err := c.Get(ctx, client.ObjectKeyFromObject(nc), &current); err != nil {
				t.Fatal(err)
			}
			current.Status.Configuration.ActionApplications = append(current.Status.Configuration.ActionApplications, cvk.NetworkControllerConfigurationActionApplication{Section: "inventory", Resource: "changed-creator-history"})
			if err := c.Status().Update(ctx, &current); err != nil {
				t.Fatal(err)
			}
			changed, err := ReadMetadataScopeWorker(ctx, c, &current, "leases", input, now)
			if err != nil || changed.BindingDigest == repeat.BindingDigest {
				t.Fatal("binding did not include complete existing creator/owner/journal state", err)
			}
		})
	}
}
