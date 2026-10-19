// Copyright 2026 Cisco Systems Inc.
// SPDX-License-Identifier: Apache-2.0
package controllerconfiguration

import (
	"context"
	"reflect"
	"strings"
	"testing"
	"time"

	cvk "github.com/cisco/virtual-kubelet-cisco/api/v1alpha1"
	apps "k8s.io/api/apps/v1"
	core "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

type bootstrapWorkerRaceReader struct {
	client.Reader
	mode  string
	reads map[string]int
}

func (r *bootstrapWorkerRaceReader) Get(ctx context.Context, key client.ObjectKey, obj client.Object, opts ...client.GetOption) error {
	if err := r.Reader.Get(ctx, key, obj, opts...); err != nil {
		return err
	}
	switch o := obj.(type) {
	case *apps.Deployment:
		r.reads["deployment"]++
		if r.mode == "late-deployment" && r.reads["deployment"] >= 4 {
			o.ResourceVersion = "changed"
		}
	case *apps.ReplicaSet:
		r.reads["replicaSet"]++
		if r.mode == "late-replicaSet" && r.reads["replicaSet"] >= 3 {
			o.ResourceVersion = "changed"
		}
	case *core.Pod:
		if r.mode == "late-pod" {
			o.Spec.Containers[0].Args = append(o.Spec.Containers[0].Args, "--controller-uid=foreign")
		}
	case *cvk.NetworkController:
		r.reads["controller"]++
		if r.mode == "late-controller" && r.reads["controller"] >= 4 {
			o.ResourceVersion = "changed"
		}
	}
	return nil
}

func TestBootstrapControllerWorker(t *testing.T) {
	for _, mode := range []string{"valid", "missing-worker", "wrong-parent", "overlap", "unready", "wrong-args", "old-template", "paused", "late-deployment", "late-replicaSet", "late-pod", "late-controller"} {
		t.Run(mode, func(t *testing.T) {
			ctx := context.Background()
			c, nc, _ := bootstrapFenceFixture(t)
			var pod core.Pod
			if err := c.Get(ctx, client.ObjectKey{Namespace: nc.Namespace, Name: "worker"}, &pod); err != nil {
				t.Fatal(err)
			}
			switch mode {
			case "missing-worker":
				if err := c.Delete(ctx, &pod); err != nil {
					t.Fatal(err)
				}
			case "wrong-parent":
				pod.OwnerReferences[0].UID = "foreign"
				if err := c.Update(ctx, &pod); err != nil {
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
				pod.Spec.Containers[0].Args = append(pod.Spec.Containers[0].Args, "--controller-generation=2")
				if err := c.Update(ctx, &pod); err != nil {
					t.Fatal(err)
				}
			case "old-template":
				pod.Annotations["cisco.vk/controller-contract-hash"] = strings.Repeat("b", 64)
				if err := c.Update(ctx, &pod); err != nil {
					t.Fatal(err)
				}
			case "paused":
				nc.Spec.Paused = true
				if err := c.Update(ctx, nc); err != nil {
					t.Fatal(err)
				}
			}
			reader := &bootstrapWorkerRaceReader{Reader: c, mode: mode, reads: map[string]int{}}
			worker, err := readBootstrapControllerWorker(ctx, reader, nc, time.Now())
			if (err == nil) != (mode == "valid") {
				t.Fatal("worker observation outcome", err)
			}
			if mode != "valid" {
				if worker != nil {
					t.Fatal("unbound worker returned")
				}
				return
			}
			if worker.Pod.UID != string(pod.UID) || worker.Deployment.UID != "deployment-uid" || worker.ReplicaSet.UID != "replica-uid" || worker.Username != "system:serviceaccount:lab:connector" {
				t.Fatal("incomplete actor binding")
			}
		})
	}
}

func TestBootstrapFenceCannotTransferWorker(t *testing.T) {
	ctx := context.Background()
	c, nc, _ := bootstrapFenceFixture(t)
	store := &bootstrapFenceStore{Client: c}
	authorize := BootstrapFenceRuntimeCheck(func(context.Context, string, *cvk.NetworkController, BootstrapIdentityObservation) error { return nil })
	first, err := ReserveBootstrapFence(ctx, store, store, nc, bootstrapFenceTestInstallation(), authorize)
	if err != nil {
		t.Fatal(err)
	}
	var pod core.Pod
	if err := c.Get(ctx, client.ObjectKey{Namespace: nc.Namespace, Name: "worker"}, &pod); err != nil {
		t.Fatal(err)
	}
	if err := c.Delete(ctx, &pod); err != nil {
		t.Fatal(err)
	}
	pod.UID = "replacement-worker"
	pod.ResourceVersion = ""
	pod.CreationTimestamp = metav1.Time{}
	if err := c.Create(ctx, &pod); err != nil {
		t.Fatal(err)
	}
	if _, err := readBootstrapControllerWorker(ctx, c, nc, time.Now()); err != nil {
		t.Fatal("replacement itself is not a valid current worker", err)
	}
	second, err := ReserveBootstrapFence(ctx, store, store, nc, bootstrapFenceTestInstallation(), authorize)
	if err == nil || second != nil || store.creates.Load() != 1 {
		t.Fatal("retained fence transferred to replacement worker", err)
	}
	retained, err := ReadBootstrapFence(ctx, c, "leases", "bootstrap123")
	if err != nil || !reflect.DeepEqual(first, retained) {
		t.Fatal("original actor evidence changed", err)
	}
}
