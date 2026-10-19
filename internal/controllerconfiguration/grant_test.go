// Copyright © 2026 Cisco Systems Inc.
// Licensed under the Apache License, Version 2.0 (the "License");
package controllerconfiguration

import (
	"encoding/json"
	ops "github.com/cisco/virtual-kubelet-cisco/api/ops/v1alpha1"
	"github.com/cisco/virtual-kubelet-cisco/internal/configengine/engine"
	"github.com/cisco/virtual-kubelet-cisco/internal/devicecoordination"
	coordination "k8s.io/api/coordination/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"testing"
	"time"
)

func TestValidateLiveGrant(t *testing.T) {
	now := time.Date(2026, 10, 13, 12, 0, 0, 0, time.UTC)
	r := &ops.ControllerConfigurationReservation{ObjectMeta: metav1.ObjectMeta{Namespace: "lab", UID: "reservation-uid"}, Spec: ops.ControllerConfigurationReservationSpec{Protocol: Protocol, Controller: ops.ConfigurationReservationReference{UID: "controller-uid"}, Device: ops.ConfigurationReservationReference{Name: "switch", UID: "device-uid"}, OperationID: "0123456789abcdef0123456789abcdef"}}
	r.Name, _ = ReservationName(r.Spec.Controller.UID, r.Spec.OperationID)
	holder, _ := Holder(r.Namespace, r)
	name := engine.LeaseName(devicecoordination.DeviceKey(r.Namespace, r.Spec.Device.Name), devicecoordination.MutationLeaseFamily)
	duration, transitions := int32(60), int32(2)
	acquired, renewed := metav1.NewMicroTime(now.Add(-30*time.Second)), metav1.NewMicroTime(now.Add(-10*time.Second))
	lease := &coordination.Lease{ObjectMeta: metav1.ObjectMeta{Namespace: "leases", Name: name, UID: "lease-uid"}, Spec: coordination.LeaseSpec{HolderIdentity: &holder, LeaseDurationSeconds: &duration, LeaseTransitions: &transitions, AcquireTime: &acquired, RenewTime: &renewed}}
	r.Status.Grant = &ops.ConfigurationReservationGrant{Phase: "Held", LeaseNamespace: "leases", Lease: ops.ConfigurationReservationReference{Name: name, UID: "lease-uid"}, Holder: holder, LeaseTransitions: transitions, GrantedAt: metav1.NewTime(now.Add(-20 * time.Second))}
	if err := ValidateLiveGrant(r, lease, "leases", now); err != nil {
		t.Fatal(err)
	}
	t.Run("persisted same-second acquisition", func(t *testing.T) {
		rr, ll := r.DeepCopy(), lease.DeepCopy()
		observed := now.Add(500 * time.Millisecond)
		acquired := metav1.NewMicroTime(now.Add(100 * time.Millisecond))
		ll.Spec.AcquireTime = &acquired
		ll.Spec.RenewTime = &acquired
		rr.Status.Grant.GrantedAt = metav1.NewTime(observed)
		raw, err := json.Marshal(rr)
		if err != nil {
			t.Fatal(err)
		}
		var stored ops.ControllerConfigurationReservation
		if err = json.Unmarshal(raw, &stored); err != nil {
			t.Fatal(err)
		}
		if err = ValidateLiveGrant(&stored, ll, "leases", observed); err != nil {
			t.Fatal(err)
		}
	})
	cases := map[string]func(*ops.ControllerConfigurationReservation, *coordination.Lease){
		"revoked": func(r *ops.ControllerConfigurationReservation, l *coordination.Lease) {
			r.Status.Grant.Phase = "Revoked"
		},
		"no grant":             func(r *ops.ControllerConfigurationReservation, l *coordination.Lease) { r.Status.Grant = nil },
		"reservation replaced": func(r *ops.ControllerConfigurationReservation, l *coordination.Lease) { r.UID = "replacement" },
		"lease replaced":       func(r *ops.ControllerConfigurationReservation, l *coordination.Lease) { l.UID = "replacement" },
		"foreign holder": func(r *ops.ControllerConfigurationReservation, l *coordination.Lease) {
			*l.Spec.HolderIdentity = "foreign"
		},
		"transition changed": func(r *ops.ControllerConfigurationReservation, l *coordination.Lease) { *l.Spec.LeaseTransitions++ },
		"expired boundary": func(r *ops.ControllerConfigurationReservation, l *coordination.Lease) {
			l.Spec.RenewTime = &metav1.MicroTime{Time: now.Add(-60 * time.Second)}
		},
		"future renewal": func(r *ops.ControllerConfigurationReservation, l *coordination.Lease) {
			l.Spec.RenewTime = &metav1.MicroTime{Time: now.Add(time.Second)}
		},
		"new acquisition": func(r *ops.ControllerConfigurationReservation, l *coordination.Lease) {
			l.Spec.AcquireTime = &metav1.MicroTime{Time: now.Add(-15 * time.Second)}
		},
		"no renewal": func(r *ops.ControllerConfigurationReservation, l *coordination.Lease) { l.Spec.RenewTime = nil },
		"zero duration": func(r *ops.ControllerConfigurationReservation, l *coordination.Lease) {
			*l.Spec.LeaseDurationSeconds = 0
		},
		"future grant": func(r *ops.ControllerConfigurationReservation, l *coordination.Lease) {
			r.Status.Grant.GrantedAt = metav1.NewTime(now.Add(time.Second))
		},
		"deleting lease": func(r *ops.ControllerConfigurationReservation, l *coordination.Lease) {
			v := metav1.NewTime(now)
			l.DeletionTimestamp = &v
		},
		"deleting reservation": func(r *ops.ControllerConfigurationReservation, l *coordination.Lease) {
			v := metav1.NewTime(now)
			r.DeletionTimestamp = &v
		},
		"foreign namespace": func(r *ops.ControllerConfigurationReservation, l *coordination.Lease) {
			r.Status.Grant.LeaseNamespace = "other"
			l.Namespace = "other"
		},
		"foreign device":   func(r *ops.ControllerConfigurationReservation, l *coordination.Lease) { r.Spec.Device.Name = "other" },
		"unknown protocol": func(r *ops.ControllerConfigurationReservation, l *coordination.Lease) { r.Spec.Protocol = "other" },
	}
	for name, mutate := range cases {
		t.Run(name, func(t *testing.T) {
			rr, ll := r.DeepCopy(), lease.DeepCopy()
			mutate(rr, ll)
			if ValidateLiveGrant(rr, ll, "leases", now) == nil {
				t.Fatal("accepted invalid grant")
			}
		})
	}
	if ValidateLiveGrant(nil, lease, "leases", now) == nil || ValidateLiveGrant(r, nil, "leases", now) == nil || ValidateLiveGrant(r, lease, "", now) == nil {
		t.Fatal("accepted missing evidence")
	}
}
