// Copyright © 2026 Cisco Systems Inc.
// Licensed under the Apache License, Version 2.0 (the "License");
package catalystcenter

import (
	"context"
	"errors"
	configapi "github.com/cisco/virtual-kubelet-cisco/api/config/v1alpha1"
	ops "github.com/cisco/virtual-kubelet-cisco/api/ops/v1alpha1"
	"reflect"
	"testing"
	"time"
)

func TestConfigurationWriteRequiresDeviceAdmission(t *testing.T) {
	for _, synchronous := range []bool{false, true} {
		a, config := journalFixture(t)
		calls := 0
		write := &configurationWrite{resource: "device", action: "Update", identity: configurationResourceIdentity{target: "device-id", serial: "serial"}}
		if synchronous {
			write.submitCompleted = func(context.Context) error { calls++; return nil }
		} else {
			write.submit = func(context.Context) (Task, error) { calls++; return Task{ID: "task"}, nil }
		}
		err := a.submitConfigurationWrite(context.Background(), config, nil, "sites", write)
		if !errors.Is(err, errConfigurationDeviceAdmission) || calls != 0 {
			t.Fatal("device write bypassed admission", err, calls)
		}
		if state := journalState(t, a); state != nil && state.Operation != nil {
			t.Fatal("unadmitted operation persisted")
		}
	}
}
func TestConfigurationDeviceRecoveryRetainsJournal(t *testing.T) {
	for _, phase := range []string{"Prepared", "Submitting", "Accepted", "Verifying", "Verified", "Unknown"} {
		t.Run(phase, func(t *testing.T) {
			ctx := context.Background()
			a, config := journalFixture(t)
			op, err := a.prepareConfiguration(ctx, config, nil, "sites", "device", "Update", configurationResourceIdentity{target: "device", serial: "serial"})
			if err != nil {
				t.Fatal(err)
			}
			nc, err := a.currentController(ctx)
			if err != nil {
				t.Fatal(err)
			}
			nc.Status.Configuration.Operation.Phase = phase
			if err := a.statusWriter.Status().Update(ctx, nc); err != nil {
				t.Fatal(err)
			}
			status := &configapi.NetworkControllerConfigStatus{}
			_, _, reason, _ := (&configReconciler{a: a}).evaluateConfigurationApply(ctx, nc, config, status)
			if reason != "DeviceAdmissionRecoveryRequired" {
				t.Fatal("wrong recovery decision", reason)
			}
			stored := journalState(t, a).Operation
			if stored == nil || stored.ID != op.ID || stored.Phase != phase {
				t.Fatal("device evidence changed")
			}
		})
	}
}

func TestConfigurationReservationClaimRequiresRuntimeIdentity(t *testing.T) {
	a, _ := journalFixture(t)
	for _, identity := range [][2]string{{"", ""}, {"connector", ""}, {"", "pod-uid"}} {
		a.workerPodName, a.workerPodUID = identity[0], identity[1]
		if _, err := a.claimConfigurationReservation(context.Background(), "leases", nil); !errors.Is(err, errConfigurationDeviceAdmission) {
			t.Fatal("incomplete runtime identity reached claim", err)
		}
	}
}

func TestPreparedPhysicalReservationReplansOnlyAfterSettlement(t *testing.T) {
	for _, scenario := range []string{"settled", "missing", "held", "revoked", "released without manager", "claimed"} {
		t.Run(scenario, func(t *testing.T) {
			ctx := context.Background()
			a, config := journalFixture(t)
			op, err := a.prepareConfiguration(ctx, config, nil, "sites", "device", "Update", configurationResourceIdentity{target: "device", serial: "serial"})
			if err != nil {
				t.Fatal(err)
			}
			a.configurationAdmission = &configurationAdmission{}
			if scenario != "missing" {
				r, _ := settledConfigurationFixture(t, a)
				switch scenario {
				case "held":
					r.Status.Grant.Phase = "Held"
					r.Status.ManagerSettledAt = nil
				case "revoked":
					r.Status.Grant.Phase = "Revoked"
					r.Status.ManagerSettledAt = nil
				case "released without manager":
					r.Status.ManagerSettledAt = nil
				case "claimed":
					r.Status.Claim = &ops.ConfigurationReservationClaim{OperationID: op.ID}
				}
				if err := a.statusWriter.Status().Update(ctx, r); err != nil {
					t.Fatal(err)
				}
			}
			nc, err := a.currentController(ctx)
			if err != nil {
				t.Fatal(err)
			}
			_, _, reason, _ := (&configReconciler{a: a}).evaluateConfigurationApply(ctx, nc, config, &configapi.NetworkControllerConfigStatus{})
			if scenario == "settled" {
				if reason != "ApplyProgressing" || journalState(t, a).Operation != nil {
					t.Fatal("settled preparation not cleared", reason)
				}
				next, err := a.prepareConfiguration(ctx, config, nil, "sites", "device", "Update", configurationResourceIdentity{target: "device", serial: "serial"})
				if err != nil || next.ID == op.ID {
					t.Fatal("replan reused stale grant identity", err)
				}
			} else {
				stored := journalState(t, a).Operation
				if reason != "DevicePreparationPending" || stored == nil || stored.ID != op.ID || stored.Phase != "Prepared" {
					t.Fatal("unsafe preparation discarded", reason)
				}
			}
		})
	}
}

// Journal sequencing tests inject only the permit boundary. Live claim and
// single-use permit enforcement are exercised in controllerconfiguration.
type journalTestPermit func(context.Context, time.Time) error

func (p journalTestPermit) Check(ctx context.Context, now time.Time) error { return p(ctx, now) }
func journalTestAdmission(context.Context) (configurationDispatchCheck, error) {
	return journalTestPermit(func(context.Context, time.Time) error { return nil }), nil
}

func TestConfigurationJournalOrdersPhysicalAdmission(t *testing.T) {
	for _, scenario := range []string{"accepted", "claim rejected", "missing permit", "permit rejected", "remote uncertain"} {
		t.Run(scenario, func(t *testing.T) {
			ctx := context.Background()
			a, config := journalFixture(t)
			var events []string
			admit := func(context.Context) (configurationDispatchCheck, error) {
				events = append(events, "claim")
				if journalState(t, a).Operation.Phase != "Prepared" {
					t.Fatal("claim did not precede submission journal")
				}
				if scenario == "claim rejected" {
					return nil, errors.New("claim rejected")
				}
				if scenario == "missing permit" {
					return nil, nil
				}
				return journalTestPermit(func(context.Context, time.Time) error {
					events = append(events, "check")
					if journalState(t, a).Operation.Phase != "Submitting" {
						t.Fatal("permit checked without durable submission journal")
					}
					if scenario == "permit rejected" {
						return errors.New("permit rejected")
					}
					return nil
				}), nil
			}
			submit := func(context.Context) (configurationReceipt, error) {
				events = append(events, "submit")
				if scenario == "remote uncertain" {
					return configurationReceipt{}, errors.New("connection lost")
				}
				return configurationReceipt{task: Task{ID: "task"}}, nil
			}
			identity := configurationResourceIdentity{target: "device", serial: "SERIAL"}
			err := a.submitConfigurationReceiptWithAdmission(ctx, config, nil, "sites", "resource", "Update", submit, admit, identity)
			phase := journalState(t, a).Operation.Phase
			switch scenario {
			case "accepted":
				if err != nil || phase != "Accepted" || !reflect.DeepEqual(events, []string{"claim", "check", "submit"}) {
					t.Fatal("submission sequence", err, phase, events)
				}
			case "claim rejected", "missing permit":
				if err == nil || phase != "Prepared" || !reflect.DeepEqual(events, []string{"claim"}) {
					t.Fatal("unclaimed operation advanced", err, phase, events)
				}
			case "permit rejected":
				if err == nil || phase != "Submitting" || !reflect.DeepEqual(events, []string{"claim", "check"}) {
					t.Fatal("rejected permit submitted", err, phase, events)
				}
			case "remote uncertain":
				if err == nil || phase != "Unknown" || !reflect.DeepEqual(events, []string{"claim", "check", "submit"}) {
					t.Fatal("uncertainty lost", err, phase, events)
				}
			}
			if scenario == "accepted" || scenario == "permit rejected" || scenario == "remote uncertain" {
				count := len(events)
				if err := a.submitConfigurationReceiptWithAdmission(ctx, config, nil, "sites", "resource", "Update", submit, admit, identity); err == nil {
					t.Fatal("replayed uncertain/accepted operation")
				}
				if len(events) != count {
					t.Fatal("retry reached admission or API")
				}
			}
		})
	}
}

func TestLowLevelPhysicalSubmissionCannotBypassAdmission(t *testing.T) {
	a, config := journalFixture(t)
	calls := 0
	err := a.submitConfigurationOperation(context.Background(), config, nil, "sites", "device", "Update", func(context.Context) (Task, error) { calls++; return Task{ID: "task"}, nil }, configurationResourceIdentity{target: "device", serial: "SERIAL"})
	if !errors.Is(err, errConfigurationDeviceAdmission) || calls != 0 {
		t.Fatal("lower-level physical bypass", err, calls)
	}
}
