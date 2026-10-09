// Copyright © 2026 Cisco Systems Inc.
// Licensed under the Apache License, Version 2.0 (the "License");
package catalystcenter

import (
	"context"
	"errors"
	"testing"
	"time"

	ciskov1 "github.com/cisco/virtual-kubelet-cisco/api/v1alpha1"
	"github.com/cisco/virtual-kubelet-cisco/internal/controlleradapter"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/types"
	ctrlclient "sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
)

func statusFixture(t *testing.T) (*adapter, *ciskov1.NetworkController) {
	t.Helper()
	nc := &ciskov1.NetworkController{ObjectMeta: metav1.ObjectMeta{Namespace: "lab", Name: "catc", UID: types.UID("controller-uid"), Generation: 1}, Spec: ciskov1.NetworkControllerSpec{Type: TypeName}}
	scheme := runtime.NewScheme()
	if err := ciskov1.AddToScheme(scheme); err != nil {
		t.Fatal(err)
	}
	k8s := fake.NewClientBuilder().WithScheme(scheme).WithStatusSubresource(nc).WithObjects(nc).Build()
	a := &adapter{key: ctrlclient.ObjectKeyFromObject(nc), uid: nc.UID, generation: nc.Generation, statusReader: k8s, statusWriter: k8s}
	return a, nc
}

func TestStatusRejectsStaleWorker(t *testing.T) {
	changes := map[string]func(*ciskov1.NetworkController){
		"replacement":   func(nc *ciskov1.NetworkController) { nc.UID = "replacement-uid" },
		"spec change":   func(nc *ciskov1.NetworkController) { nc.Generation++ },
		"pause":         func(nc *ciskov1.NetworkController) { nc.Spec.Paused = true },
		"other adapter": func(nc *ciskov1.NetworkController) { nc.Spec.Type = "nexus-dashboard" },
	}
	for name, change := range changes {
		t.Run(name, func(t *testing.T) {
			a, nc := statusFixture(t)
			if err := a.statusWriter.Get(context.Background(), a.key, nc); err != nil {
				t.Fatal(err)
			}
			change(nc)
			if err := a.statusWriter.Update(context.Background(), nc); err != nil {
				t.Fatal(err)
			}
			if err := a.publishHealth(context.Background(), classify(nil)); !errors.Is(err, errStaleController) {
				t.Fatalf("health accepted: %v", err)
			}
			if err := a.publishInventory(context.Background(), nil, nil, nil, nil); !errors.Is(err, errStaleController) {
				t.Fatalf("inventory accepted: %v", err)
			}
			if err := a.statusWriter.Get(context.Background(), a.key, nc); err != nil {
				t.Fatal(err)
			}
			if len(nc.Status.Conditions) != 0 || len(nc.Status.Capabilities) != 0 {
				t.Fatal("stale worker changed status")
			}
		})
	}
}

type conflictOnceClient struct {
	ctrlclient.Client
	conflicted bool
}

func (c *conflictOnceClient) Status() ctrlclient.SubResourceWriter {
	return &conflictOnceStatus{SubResourceWriter: c.Client.Status(), owner: c}
}

type conflictOnceStatus struct {
	ctrlclient.SubResourceWriter
	owner *conflictOnceClient
}

func (s *conflictOnceStatus) Update(ctx context.Context, obj ctrlclient.Object, opts ...ctrlclient.SubResourceUpdateOption) error {
	if !s.owner.conflicted {
		s.owner.conflicted = true
		var latest ciskov1.NetworkController
		if err := s.owner.Client.Get(ctx, ctrlclient.ObjectKeyFromObject(obj), &latest); err != nil {
			return err
		}
		setCapability(&latest.Status, "concurrent-capability", true, "written by another reconciliation")
		if err := s.owner.Client.Status().Update(ctx, &latest); err != nil {
			return err
		}
		return apierrors.NewConflict(schema.GroupResource{Group: "cisco.vk", Resource: "networkcontrollers"}, obj.GetName(), errors.New("concurrent update"))
	}
	return s.SubResourceWriter.Update(ctx, obj, opts...)
}

func TestStatusConflictRetryPreservesOtherWriter(t *testing.T) {
	a, nc := statusFixture(t)
	conflicting := &conflictOnceClient{Client: a.statusWriter}
	a.statusWriter = conflicting
	if err := a.publishHealth(context.Background(), classify(nil)); err != nil {
		t.Fatal(err)
	}
	if !conflicting.conflicted {
		t.Fatal("test did not exercise conflict")
	}
	if err := a.statusReader.Get(context.Background(), a.key, nc); err != nil {
		t.Fatal(err)
	}
	if nc.Status.Phase != ciskov1.NetworkControllerPhaseReady {
		t.Fatalf("status=%+v", nc.Status)
	}
	found := false
	for _, capability := range nc.Status.Capabilities {
		if capability.Name == "concurrent-capability" {
			found = true
		}
	}
	if !found {
		t.Fatal("concurrent status update was lost")
	}
}

func TestFailedProbePersistsAttemptTimeAndReportsManagedSWIMCapability(t *testing.T) {
	a, nc := statusFixture(t)
	old := metav1.NewTime(time.Now().Add(-time.Hour))
	if err := a.statusWriter.Get(context.Background(), a.key, nc); err != nil {
		t.Fatal(err)
	}
	nc.Status.LastAttemptTime = &old
	if err := a.statusWriter.Status().Update(context.Background(), nc); err != nil {
		t.Fatal(err)
	}
	failure := classify(errors.New("arbitrary remote secret"))
	for i := 0; i < 2; i++ {
		if err := a.publishHealth(context.Background(), failure); err != nil {
			t.Fatal(err)
		}
	}
	if err := a.publishInventory(context.Background(), []Device{{ID: "device-1"}}, nil, []Image{{ID: "image-1"}}, nil); err != nil {
		t.Fatal(err)
	}
	if err := a.statusReader.Get(context.Background(), a.key, nc); err != nil {
		t.Fatal(err)
	}
	if nc.Status.LastAttemptTime == nil || !nc.Status.LastAttemptTime.After(old.Time) {
		t.Fatal("attempt time not updated")
	}
	for _, condition := range nc.Status.Conditions {
		if condition.Message == "arbitrary remote secret" {
			t.Fatal("raw error was published")
		}
	}
	found := false
	for _, capability := range nc.Status.Capabilities {
		if capability.Name == CapabilitySWIM {
			found = true
			if !capability.Supported {
				t.Fatal("managed SWIM capability was not reported")
			}
		}
	}
	if !found {
		t.Fatal("SWIM availability not reported")
	}
}

func TestFactoryRejectsUnsupportedFeatures(t *testing.T) {
	if _, err := newAdapter(controlleradapter.Options{}); err == nil {
		t.Fatal("nil controller accepted")
	}
	nc := &ciskov1.NetworkController{}
	nc.Spec.PreferredAPIVersion = "v2"
	if _, err := newAdapter(controlleradapter.Options{Controller: nc}); err == nil {
		t.Fatal("unsupported API pin accepted")
	}
	nc.Spec.PreferredAPIVersion = ""
	nc.Spec.DeviceAdoption = &ciskov1.NetworkControllerDeviceAdoption{Enabled: true}
	if _, err := newAdapter(controlleradapter.Options{Controller: nc}); err == nil {
		t.Fatal("unsupported adoption accepted")
	}
}
