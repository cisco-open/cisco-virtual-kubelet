// Copyright © 2026 Cisco Systems Inc.
// Licensed under the Apache License, Version 2.0 (the "License");
package catalystcenter

import (
	"context"
	"errors"
	"sync"
	"sync/atomic"
	"testing"

	configapi "github.com/cisco/virtual-kubelet-cisco/api/config/v1alpha1"
	ciskov1 "github.com/cisco/virtual-kubelet-cisco/api/v1alpha1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	ctrlclient "sigs.k8s.io/controller-runtime/pkg/client"
)

func journalFixture(t *testing.T) (*adapter, *configapi.NetworkControllerConfig) {
	t.Helper()
	r, config, _ := reportFixture(t)
	config.Spec.Mode = configapi.NetworkControllerApplyModeApply
	if err := r.a.statusWriter.Update(context.Background(), config); err != nil {
		t.Fatal(err)
	}
	return r.a, config
}

func journalState(t *testing.T, a *adapter) *ciskov1.NetworkControllerConfigurationStatus {
	t.Helper()
	nc, err := a.configurationRecoveryController(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	return nc.Status.Configuration
}

func TestConfigurationJournalRecoveryAndOwnership(t *testing.T) {
	ctx := context.Background()
	a, config := journalFixture(t)
	op, err := a.prepareConfiguration(ctx, config, nil, "sites", "logical-resource", "Create")
	if err != nil {
		t.Fatal(err)
	}
	if err = a.startConfiguration(ctx, config, nil, op.ID); err != nil {
		t.Fatal(err)
	}
	// A new worker has no local memory of whether the request reached the API.
	restarted := *a
	if err = restarted.startConfiguration(ctx, config, nil, op.ID); !errors.Is(err, errConfigurationTransition) {
		t.Fatalf("repeated submission allowed: %v", err)
	}
	if err = restarted.finishConfiguration(ctx, op.ID); !errors.Is(err, errConfigurationBusy) {
		t.Fatalf("uncertain write released: %v", err)
	}
	if _, err = restarted.prepareConfiguration(ctx, config, nil, "sites", "Global/Other", "Create"); !errors.Is(err, errConfigurationBusy) {
		t.Fatalf("parallel operation allowed: %v", err)
	}
	for _, phase := range []string{"Unknown", "Verifying", "Verified"} {
		if err = restarted.advanceConfiguration(ctx, op.ID, phase, ""); err != nil {
			t.Fatalf("%s: %v", phase, err)
		}
	}
	if err = restarted.finishConfiguration(ctx, op.ID); err != nil {
		t.Fatal(err)
	}
	state := journalState(t, a)
	if state.Operation != nil || len(state.Owners) != 1 || state.Owners[0].ConfigUID != string(config.UID) {
		t.Fatalf("lost ownership: %+v", state)
	}
	// Recreating the manifest under the same name cannot inherit its old claim.
	if err = a.statusWriter.Delete(ctx, config); err != nil {
		t.Fatal(err)
	}
	config.ResourceVersion = ""
	config.UID = "replacement-config"
	if err = a.statusWriter.Create(ctx, config); err != nil {
		t.Fatal(err)
	}
	if _, err = a.prepareConfiguration(ctx, config, nil, "sites", "logical-resource", "Update"); !errors.Is(err, errConfigurationOwned) {
		t.Fatalf("replacement inherited claim: %v", err)
	}
}

func TestConfigurationJournalConcurrentSubmissionPermit(t *testing.T) {
	ctx := context.Background()
	a, config := journalFixture(t)
	op, err := a.prepareConfiguration(ctx, config, nil, "sites", "logical-resource", "Create")
	if err != nil {
		t.Fatal(err)
	}
	var permits atomic.Int32
	var wg sync.WaitGroup
	for range 20 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if a.startConfiguration(ctx, config.DeepCopy(), nil, op.ID) == nil {
				permits.Add(1)
			}
		}()
	}
	wg.Wait()
	if permits.Load() != 1 {
		t.Fatalf("submission permits=%d, want exactly one", permits.Load())
	}
	if journalState(t, a).Operation.Phase != "Submitting" {
		t.Fatal("permit was not durable")
	}
}

func TestConfigurationJournalConcurrentReservations(t *testing.T) {
	ctx := context.Background()
	a, config := journalFixture(t)
	var reservations atomic.Int32
	var wg sync.WaitGroup
	for range 20 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if _, err := a.prepareConfiguration(ctx, config.DeepCopy(), nil, "sites", "logical-resource", "Create"); err == nil {
				reservations.Add(1)
			}
		}()
	}
	wg.Wait()
	if reservations.Load() != 1 {
		t.Fatalf("reservations=%d, want one", reservations.Load())
	}
}

func TestConfigurationJournalRejectsChangedSource(t *testing.T) {
	ctx := context.Background()
	a, config := journalFixture(t)
	cm := &corev1.ConfigMap{ObjectMeta: metav1.ObjectMeta{Namespace: config.Namespace, Name: "model", UID: "model-uid"}, Data: map[string]string{"model.yaml": "first"}}
	if err := a.statusWriter.Create(ctx, cm); err != nil {
		t.Fatal(err)
	}
	config.Spec.Source.Inline = nil
	config.Spec.Source.ConfigMapRef = &configapi.ConfigMapKeyRef{Name: cm.Name, Key: "model.yaml"}
	if err := a.statusWriter.Update(ctx, config); err != nil {
		t.Fatal(err)
	}
	source := &configSourceSnapshot{key: ctrlclient.ObjectKeyFromObject(cm), uid: cm.UID, version: cm.ResourceVersion}
	op, err := a.prepareConfiguration(ctx, config, source, "sites", "logical-resource", "Create")
	if err != nil {
		t.Fatal(err)
	}
	cm.Data["model.yaml"] = "second"
	if err = a.statusWriter.Update(ctx, cm); err != nil {
		t.Fatal(err)
	}
	if err = a.startConfiguration(ctx, config, source, op.ID); !errors.Is(err, errConfigurationStale) {
		t.Fatalf("stale source submitted: %v", err)
	}
	if journalState(t, a).Operation.Phase != "Prepared" {
		t.Fatal("stale source changed operation")
	}
	if err = a.finishConfiguration(ctx, op.ID); err != nil {
		t.Fatal(err)
	}
}

func TestConfigurationJournalTaskReceiptDoesNotReleaseFence(t *testing.T) {
	ctx := context.Background()
	a, config := journalFixture(t)
	op, err := a.prepareConfiguration(ctx, config, nil, "sites", "logical-resource", "Create")
	if err != nil {
		t.Fatal(err)
	}
	if err = a.advanceConfiguration(ctx, op.ID, "Accepted", "task-1"); err == nil {
		t.Fatal("receipt accepted before submission")
	}
	if err = a.startConfiguration(ctx, config, nil, op.ID); err != nil {
		t.Fatal(err)
	}
	if err = a.advanceConfiguration(ctx, op.ID, "Accepted", "../secret"); err == nil {
		t.Fatal("invalid receipt accepted")
	}
	if err = a.advanceConfiguration(ctx, op.ID, "Accepted", "task-1"); err != nil {
		t.Fatal(err)
	}
	if err = a.advanceConfiguration(ctx, op.ID, "Verified", ""); err == nil {
		t.Fatal("receipt treated as verified state")
	}
	if err = a.finishConfiguration(ctx, op.ID); !errors.Is(err, errConfigurationBusy) {
		t.Fatal("accepted task released fence")
	}
	if err = a.advanceConfiguration(ctx, op.ID, "Verifying", ""); err != nil {
		t.Fatal(err)
	}
	// Policy changes stop new submissions but must not prevent durable recovery.
	nc, err := a.currentController(ctx)
	if err != nil {
		t.Fatal(err)
	}
	nc.Spec.Paused = true
	nc.Generation++
	if err = a.statusWriter.Update(ctx, nc); err != nil {
		t.Fatal(err)
	}
	if err = a.advanceConfiguration(ctx, op.ID, "Verified", ""); err != nil {
		t.Fatal(err)
	}
	if err = a.finishConfiguration(ctx, op.ID); err != nil {
		t.Fatal(err)
	}
}

func TestConfigurationJournalDeletionRetainsUncertainty(t *testing.T) {
	ctx := context.Background()
	a, config := journalFixture(t)
	op, err := a.prepareConfiguration(ctx, config, nil, "sites", "logical-resource", "Create")
	if err != nil {
		t.Fatal(err)
	}
	if err = a.startConfiguration(ctx, config, nil, op.ID); err != nil {
		t.Fatal(err)
	}
	if err = a.statusWriter.Delete(ctx, config); err != nil {
		t.Fatal(err)
	}
	if journalState(t, a).Operation.ID != op.ID {
		t.Fatal("config deletion erased journal")
	}
	if err = a.finishConfiguration(ctx, op.ID); !errors.Is(err, errConfigurationBusy) {
		t.Fatal("deleted config released uncertainty")
	}
}

type lostJournalAckClient struct{ ctrlclient.Client }
type lostJournalAckWriter struct{ ctrlclient.SubResourceWriter }

func (c lostJournalAckClient) Status() ctrlclient.SubResourceWriter {
	return lostJournalAckWriter{c.Client.Status()}
}
func (w lostJournalAckWriter) Update(ctx context.Context, obj ctrlclient.Object, opts ...ctrlclient.SubResourceUpdateOption) error {
	if err := w.SubResourceWriter.Update(ctx, obj, opts...); err != nil {
		return err
	}
	return errors.New("simulated lost Kubernetes acknowledgement")
}

func TestConfigurationJournalLostPermitAcknowledgementNeverRetries(t *testing.T) {
	ctx := context.Background()
	a, config := journalFixture(t)
	op, err := a.prepareConfiguration(ctx, config, nil, "sites", "logical-resource", "Create")
	if err != nil {
		t.Fatal(err)
	}
	original := a.statusWriter
	a.statusWriter = lostJournalAckClient{original}
	if err := a.startConfiguration(ctx, config, nil, op.ID); err == nil {
		t.Fatal("lost acknowledgement issued a permit")
	}
	a.statusWriter = original
	if journalState(t, a).Operation.Phase != "Submitting" {
		t.Fatal("test did not persist ambiguous permit")
	}
	if err := a.startConfiguration(ctx, config, nil, op.ID); err == nil {
		t.Fatal("ambiguous permit retried")
	}
}

func TestConfigurationSubmissionPersistsReceiptOrUncertainty(t *testing.T) {
	for _, outcome := range []string{"accepted", "request-timeout", "empty-receipt", "receipt-write-timeout"} {
		t.Run(outcome, func(t *testing.T) {
			a, config := journalFixture(t)
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			calls := 0
			err := a.submitConfigurationOperation(ctx, config, nil, "sites", "logical-resource", "Create", func(context.Context) (Task, error) {
				calls++
				state := journalState(t, a)
				if state.Operation.Phase != "Submitting" || state.Operation.SubmittedAt == nil {
					t.Fatal("API invoked without durable submission permit")
				}
				switch outcome {
				case "request-timeout":
					cancel()
					return Task{}, context.DeadlineExceeded
				case "empty-receipt":
					return Task{}, nil
				case "receipt-write-timeout":
					a.statusWriter = lostJournalAckClient{a.statusWriter}
				}
				return Task{ID: "task-1"}, nil
			})
			if (err == nil) != (outcome == "accepted") {
				t.Fatalf("outcome=%s err=%v", outcome, err)
			}
			want := "Accepted"
			if outcome == "request-timeout" || outcome == "empty-receipt" {
				want = "Unknown"
			}
			if got := journalState(t, a).Operation.Phase; got != want {
				t.Fatalf("phase=%s want=%s", got, want)
			}
			if err = a.submitConfigurationOperation(context.Background(), config, nil, "sites", "logical-resource", "Create", func(context.Context) (Task, error) { calls++; return Task{}, nil }); !errors.Is(err, errConfigurationBusy) {
				t.Fatalf("pending operation not fenced: %v", err)
			}
			if calls != 1 {
				t.Fatalf("API called %d times", calls)
			}
		})
	}
}

func TestConfigurationRevisionChangeReleasesOnlySafeBoundaries(t *testing.T) {
	for _, phase := range []string{"Prepared", "Submitting", "Accepted", "Verified"} {
		t.Run(phase, func(t *testing.T) {
			ctx := context.Background()
			a, config := journalFixture(t)
			op, err := a.prepareConfiguration(ctx, config, nil, "sites", "logical-resource", "Create")
			if err != nil {
				t.Fatal(err)
			}
			if phase != "Prepared" {
				if err = a.startConfiguration(ctx, config, nil, op.ID); err != nil {
					t.Fatal(err)
				}
			}
			if phase == "Accepted" || phase == "Verified" {
				if err = a.advanceConfiguration(ctx, op.ID, "Accepted", "task-1"); err != nil {
					t.Fatal(err)
				}
			}
			if phase == "Verified" {
				for _, next := range []string{"Verifying", "Verified"} {
					if err = a.advanceConfiguration(ctx, op.ID, next, ""); err != nil {
						t.Fatal(err)
					}
				}
			}
			config.Generation++
			if err = a.statusWriter.Update(ctx, config); err != nil {
				t.Fatal(err)
			}
			status := config.Status.DeepCopy()
			nc, err := a.currentController(ctx)
			if err != nil {
				t.Fatal(err)
			}
			// Safe cancellation/release and old-revision fencing need no API calls.
			(&configReconciler{a: a}).evaluateConfigurationApply(ctx, nc, config, status)
			cleared := journalState(t, a).Operation == nil
			if cleared != (phase == "Prepared" || phase == "Verified") {
				t.Fatalf("phase=%s cleared=%v", phase, cleared)
			}
		})
	}
}

func TestConfigurationJournalRejectsChangedTemplateAsset(t *testing.T) {
	ctx := context.Background()
	a, config := journalFixture(t)
	config.Spec.ManagedSections = []string{"templates"}
	cm := &corev1.ConfigMap{ObjectMeta: metav1.ObjectMeta{Namespace: config.Namespace, Name: "template-model", UID: "template-model-uid"}, Data: map[string]string{"model.yaml": projectModel, "cvk-template-assets.json": `{"body.j2":"first"}`}}
	if err := a.statusWriter.Create(ctx, cm); err != nil {
		t.Fatal(err)
	}
	config.Spec.Source.Inline = nil
	config.Spec.Source.ConfigMapRef = &configapi.ConfigMapKeyRef{Name: cm.Name, Key: "model.yaml"}
	if err := a.statusWriter.Update(ctx, config); err != nil {
		t.Fatal(err)
	}
	source, err := loadControllerSource(ctx, a.statusReader, config)
	if err != nil {
		t.Fatal(err)
	}
	if source.templateFiles["body.j2"] != "first" {
		t.Fatal("template files were not loaded")
	}
	op, err := a.prepareConfiguration(ctx, config, source, "templates", "CVK-Test", "Create")
	if err != nil {
		t.Fatal(err)
	}
	cm.Data["cvk-template-assets.json"] = `{"body.j2":"second"}`
	if err = a.statusWriter.Update(ctx, cm); err != nil {
		t.Fatal(err)
	}
	if source.templateFiles["body.j2"] != "first" {
		t.Fatal("snapshot changed in place")
	}
	if err = a.startConfiguration(ctx, config, source, op.ID); !errors.Is(err, errConfigurationStale) {
		t.Fatalf("asset-only change did not fence submission: %v", err)
	}
	if journalState(t, a).Operation.Phase != "Prepared" {
		t.Fatal("stale asset revision advanced journal")
	}
	fresh, err := loadControllerSource(ctx, a.statusReader, config)
	if err != nil {
		t.Fatal(err)
	}
	if fresh.templateFiles["body.j2"] != "second" || fresh.version == source.version {
		t.Fatal("new asset revision not observed")
	}
}

func TestTagNamespaceOwnershipAcrossSections(t *testing.T) {
	for _, pair := range [][2]string{{"templates", "inventory"}, {"inventory", "templates"}} {
		for _, noop := range []bool{true, false} {
			a, config := journalFixture(t)
			config.Spec.ManagedSections = []string{pair[1]}
			if err := a.statusWriter.Update(context.Background(), config); err != nil {
				t.Fatal(err)
			}
			nc, err := a.currentController(context.Background())
			if err != nil {
				t.Fatal(err)
			}
			nc.Status.Configuration = &ciskov1.NetworkControllerConfigurationStatus{Owners: []ciskov1.NetworkControllerSectionOwner{{Section: pair[0], ConfigName: "other", ConfigUID: "other-uid"}}}
			if err := a.statusWriter.Status().Update(context.Background(), nc); err != nil {
				t.Fatal(err)
			}
			if noop {
				err = a.claimConfigurationSection(context.Background(), config, nil)
			} else {
				_, err = a.prepareConfiguration(context.Background(), config, nil, pair[1], "tag", "Create")
			}
			if !errors.Is(err, errConfigurationOwned) {
				t.Fatalf("cross-section ownership not enforced: %v", err)
			}
		}
	}
}

func TestConfigurationJournalPreservesPhysicalIdentity(t *testing.T) {
	a, config := journalFixture(t)
	config.Spec.ManagedSections = []string{"inventory"}
	if err := a.statusWriter.Update(context.Background(), config); err != nil {
		t.Fatal(err)
	}
	op, err := a.prepareConfiguration(context.Background(), config, nil, "inventory", "switch", "Update", configurationResourceIdentity{target: "device", parent: "site", serial: "SERIAL"})
	if err != nil {
		t.Fatal(err)
	}
	restarted := *a
	persisted := journalState(t, &restarted).Operation
	if persisted == nil || persisted.ID != op.ID || persisted.TargetID != "device" || persisted.ParentID != "site" || persisted.TargetSerial != "SERIAL" {
		t.Fatal("physical binding lost across journal read")
	}
	if err := a.startConfiguration(context.Background(), config, nil, op.ID); err != nil {
		t.Fatal(err)
	}
	if err := a.advanceConfiguration(context.Background(), op.ID, "Unknown", ""); err != nil {
		t.Fatal(err)
	}
	if journalState(t, &restarted).Operation.TargetSerial != "SERIAL" {
		t.Fatal("uncertain outcome lost physical binding")
	}
	if err := a.finishConfiguration(context.Background(), op.ID); !errors.Is(err, errConfigurationBusy) {
		t.Fatal("uncertain physical mutation released", err)
	}
}

func TestConfigurationJournalRejectsInvalidPhysicalBinding(t *testing.T) {
	for _, identity := range []configurationResourceIdentity{{serial: "SERIAL"}, {target: "device", serial: " SERIAL"}, {target: "device", serial: "serial\n"}} {
		a, config := journalFixture(t)
		if _, err := a.prepareConfiguration(context.Background(), config, nil, "sites", "resource", "Update", identity); !errors.Is(err, errConfigurationTransition) {
			t.Fatal("invalid physical binding accepted", err)
		}
		state := journalState(t, a)
		if state != nil && state.Operation != nil {
			t.Fatal("invalid binding persisted")
		}
	}
}

func TestConfigurationPermitRequiresPersistedResourceIdentity(t *testing.T) {
	for _, field := range []string{"device", "site", "serial"} {
		a, config := journalFixture(t)
		identity := configurationResourceIdentity{target: "device", parent: "site", serial: "SERIAL"}
		op, err := a.prepareConfiguration(context.Background(), config, nil, "sites", "resource", "Update", identity)
		if err != nil {
			t.Fatal(err)
		}
		// Model an older CRD pruning a newly added field, or a changed stored binding.
		nc, err := a.currentController(context.Background())
		if err != nil {
			t.Fatal(err)
		}
		switch field {
		case "device":
			nc.Status.Configuration.Operation.TargetID = ""
		case "site":
			nc.Status.Configuration.Operation.ParentID = ""
		case "serial":
			nc.Status.Configuration.Operation.TargetSerial = ""
		}
		if err := a.statusWriter.Status().Update(context.Background(), nc); err != nil {
			t.Fatal(err)
		}
		if err := a.startConfiguration(context.Background(), config, nil, op.ID, identity); !errors.Is(err, errConfigurationStale) {
			t.Fatal("submission permitted without stored binding", field, err)
		}
		if journalState(t, a).Operation.Phase != "Prepared" {
			t.Fatal("invalid binding consumed permit")
		}
	}
}

type pruningJournalClient struct {
	ctrlclient.Client
	phase string
}
type pruningJournalWriter struct {
	ctrlclient.SubResourceWriter
	phase string
}

func (c pruningJournalClient) Status() ctrlclient.SubResourceWriter {
	return pruningJournalWriter{c.Client.Status(), c.phase}
}
func (w pruningJournalWriter) Update(ctx context.Context, obj ctrlclient.Object, opts ...ctrlclient.SubResourceUpdateOption) error {
	if nc, ok := obj.(*ciskov1.NetworkController); ok && nc.Status.Configuration != nil && nc.Status.Configuration.Operation != nil && (w.phase == "" || nc.Status.Configuration.Operation.Phase == w.phase) {
		nc.Status.Configuration.Operation.TargetSerial = ""
	}
	return w.SubResourceWriter.Update(ctx, obj, opts...)
}
func TestConfigurationSubmissionRefusesPrunedBinding(t *testing.T) {
	a, config := journalFixture(t)
	a.statusWriter = pruningJournalClient{Client: a.statusWriter}
	calls := 0
	err := a.submitConfigurationReceiptWithAdmission(context.Background(), config, nil, "sites", "resource", "Update", func(context.Context) (configurationReceipt, error) {
		calls++
		return configurationReceipt{task: Task{ID: "task"}}, nil
	}, journalTestAdmission, configurationResourceIdentity{target: "device", parent: "site", serial: "SERIAL"})
	if !errors.Is(err, errConfigurationStale) || calls != 0 {
		t.Fatal("API called after schema pruned physical binding", err, calls)
	}
}

func TestConfigurationSubmissionRefusesBindingPrunedWithPermit(t *testing.T) {
	a, config := journalFixture(t)
	a.statusWriter = pruningJournalClient{Client: a.statusWriter, phase: "Submitting"}
	calls := 0
	err := a.submitConfigurationReceiptWithAdmission(context.Background(), config, nil, "sites", "resource", "Update", func(context.Context) (configurationReceipt, error) {
		calls++
		return configurationReceipt{task: Task{ID: "task"}}, nil
	}, journalTestAdmission, configurationResourceIdentity{target: "device", parent: "site", serial: "SERIAL"})
	if !errors.Is(err, errConfigurationStale) || calls != 0 {
		t.Fatal("API called after submission permit lost binding", err, calls)
	}
	op := journalState(t, a).Operation
	if op == nil || op.Phase != "Submitting" {
		t.Fatal("lost write-ahead fence")
	}
	if err := a.finishConfiguration(context.Background(), op.ID); !errors.Is(err, errConfigurationBusy) {
		t.Fatal("uncertain permit released", err)
	}
}
