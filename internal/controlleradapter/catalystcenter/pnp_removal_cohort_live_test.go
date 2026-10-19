//go:build live

// Copyright 2026 Cisco Systems Inc.
// SPDX-License-Identifier: Apache-2.0
package catalystcenter

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"reflect"
	"testing"
	"time"

	configapi "github.com/cisco/virtual-kubelet-cisco/api/config/v1alpha1"
	cvk "github.com/cisco/virtual-kubelet-cisco/api/v1alpha1"
	contract "github.com/cisco/virtual-kubelet-cisco/internal/controllerconfiguration"
	"k8s.io/apimachinery/pkg/api/meta"
	"k8s.io/apimachinery/pkg/runtime"
	ctrl "sigs.k8s.io/controller-runtime"
	ctrlclient "sigs.k8s.io/controller-runtime/pkg/client"
)

// Qualification-only transport: exactly one POST per generated payload and one
// DELETE per durably verified UUID. No claim/reset/device mutations are allowed.
type livePnPCohortTrace struct {
	base       http.RoundTripper
	records    []*livePnPRegistrationTrace
	beforeRead func() error
	requests   int
}

func (t *livePnPCohortTrace) RoundTrip(req *http.Request) (*http.Response, error) {
	t.requests++
	if req.Method == http.MethodGet || req.URL.Path == authPath {
		if req.Method == http.MethodGet && req.URL.Path == pnpDevicesPath && t.beforeRead != nil {
			if err := t.beforeRead(); err != nil {
				return nil, err
			}
		}
		return t.base.RoundTrip(req)
	}
	if req.Method == http.MethodPost && req.URL.Path == pnpDevicesPath {
		raw, err := io.ReadAll(req.Body)
		if err != nil {
			return nil, err
		}
		req.Body = io.NopCloser(bytes.NewReader(raw))
		var actual any
		if json.Unmarshal(raw, &actual) != nil {
			return nil, errInvalidResponse
		}
		for _, record := range t.records {
			var expected any
			if json.Unmarshal(record.payload, &expected) == nil && reflect.DeepEqual(expected, actual) {
				return record.RoundTrip(req)
			}
		}
	}
	if req.Method == http.MethodDelete {
		for _, record := range t.records {
			if record.deleteID != "" && req.URL.Path == pnpDevicesPath+"/"+record.deleteID {
				return record.RoundTrip(req)
			}
		}
	}
	return nil, errors.New("PnP cohort qualification refused out-of-scope mutation")
}

func TestLivePnPRemovalCohort(t *testing.T) {
	if os.Getenv("CATC_PNP_REMOVAL_COHORT") != "two-owned-uncontacted-records" {
		t.Skip("two-record PnP cohort qualification disabled")
	}
	exerciseLivePnPRemovalCohort(t, livePnPCohortOptions{})
}

func TestLivePnPMetadataRemovalCycle(t *testing.T) {
	if os.Getenv("CATC_PNP_METADATA_CYCLE") != "two-owned-uncontacted-records" {
		t.Skip("metadata cohort qualification disabled")
	}
	exerciseLivePnPRemovalCohort(t, livePnPCohortOptions{metadata: true})
}

func TestLivePnPRemovalPublicMetadataCycle(t *testing.T) {
	if os.Getenv("CATC_PNP_PUBLIC_METADATA_CYCLE") != "two-owned-uncontacted-records" {
		t.Skip("public metadata cohort qualification disabled")
	}
	if os.Getenv("CATC_PNP_METADATA_RECOVERY_DIR") != "" {
		t.Fatal("public metadata qualification requires a fresh generated cohort")
	}
	exerciseLivePnPRemovalCohort(t, livePnPCohortOptions{metadata: true, public: true})
}

func TestLivePnPMetadataCancellationCycle(t *testing.T) {
	if os.Getenv("CATC_PNP_METADATA_CANCELLATION_CYCLE") != "two-owned-uncontacted-records" {
		t.Skip("metadata cancellation qualification disabled")
	}
	if os.Getenv("CATC_PNP_METADATA_RECOVERY_DIR") != "" {
		t.Fatal("cancellation qualification requires fresh generated records")
	}
	exerciseLivePnPRemovalCohort(t, livePnPCohortOptions{metadata: true, public: true, cancellation: true})
}

func TestLivePnPMetadataEpochSettlement(t *testing.T) {
	if os.Getenv("CATC_PNP_METADATA_EPOCH_CYCLE") != "two-owned-uncontacted-records" {
		t.Skip("metadata epoch qualification disabled")
	}
	if os.Getenv("CATC_PNP_METADATA_RECOVERY_DIR") != "" {
		t.Fatal("epoch qualification requires a fresh generated cohort")
	}
	exerciseLivePnPRemovalCohort(t, livePnPCohortOptions{metadata: true, public: true, epoch: true})
}

type livePnPCohortOptions struct{ metadata, public, epoch, cancellation bool }

func exerciseLivePnPRemovalCohort(t *testing.T, options livePnPCohortOptions) {
	metadata, public := options.metadata, options.public
	ctx, cancel := context.WithTimeout(context.Background(), 4*time.Minute)
	defer cancel()
	c := liveClient(t)
	recovery := ""
	var recovered *cvk.NetworkControllerConfigurationStatus
	var recoveredIntent []byte
	if metadata {
		recovery = os.Getenv("CATC_PNP_METADATA_RECOVERY_DIR")
	}
	if recovery != "" {
		data, err := os.ReadFile(filepath.Join(recovery, "journal.json"))
		if err != nil {
			t.Fatal(err)
		}
		if err := json.Unmarshal(data, &recovered); err != nil {
			t.Fatal(err)
		}
		if recovered == nil || recovered.MetadataHandoff == nil || recovered.MetadataHandoff.ReleasedAt == nil || recovered.MetadataHandoff.SettlementDigest == "" || recovered.MetadataHandoff.RefusedAt == nil || recovered.Operation != nil {
			t.Fatal("recovery requires the exact completed original no-dispatch settlement")
		}
		recoveredIntent, err = os.ReadFile(filepath.Join(recovery, "intent.json"))
		if err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(recovery, "journal-before-recovery.json"), data, 0600); err != nil {
			t.Fatal(err)
		}
	}
	baseline, err := c.pnpDevices(ctx)
	if err != nil || recovery == "" && len(baseline) != 0 {
		t.Fatal("requires empty PnP catalog", err)
	}
	network, err := c.configurationDevices(ctx)
	if err != nil {
		t.Fatal(err)
	}
	sites, err := c.ListSites(ctx)
	if err != nil {
		t.Fatal(err)
	}
	site := ""
	for _, s := range sites {
		if s.Type != "global" && validSitePath(s.NameHierarchy) {
			site = s.NameHierarchy
			break
		}
	}
	if site == "" {
		t.Fatal("existing non-global site required")
	}
	suffix := uint32(time.Now().UnixNano())
	wants := []pnpDeviceIntent{}
	rest, _, err := c.session(ctx)
	if err != nil {
		t.Fatal(err)
	}
	base := rest.HTTPClient.Transport
	if base == nil {
		base = http.DefaultTransport
	}
	trace := &livePnPCohortTrace{base: base}
	for i := range 2 {
		want := pnpDeviceIntent{Name: fmt.Sprintf("cvk-q-cohort-%08x-%d", suffix, i), State: "PNP", Serial: fmt.Sprintf("CVK%08X", suffix+uint32(i)), Site: site, PID: "C9300-48P"}
		payload, err := pnpRegistrationPayload(want)
		if err != nil {
			t.Fatal(err)
		}
		wants = append(wants, want)
		trace.records = append(trace.records, &livePnPRegistrationTrace{base: base, payload: payload})
	}
	if recovery != "" {
		parsed, err := parsePnPInventory(recoveredIntent)
		if err != nil || len(parsed) != 2 {
			t.Fatal("recovery intent invalid", err)
		}
		wants = parsed
		trace.records = nil
		ids := map[string]bool{}
		for _, r := range recovered.ActionApplications {
			if isPnPRegistrationOperation(r.Operation.Section, r.Operation.Resource) {
				ids[r.Operation.CreatedResourceID] = true
			}
		}
		if len(baseline) != 2 || len(ids) != 2 {
			t.Fatal("recovery catalog does not match two original creators")
		}
		for _, row := range baseline {
			if !ids[row.ID] {
				t.Fatal("foreign PnP record present; recovery stopped")
			}
		}
		for _, want := range wants {
			payload, err := pnpRegistrationPayload(want)
			if err != nil {
				t.Fatal(err)
			}
			trace.records = append(trace.records, &livePnPRegistrationTrace{base: base, payload: payload, posts: 1})
		}
	}
	rest.HTTPClient.Transport = trace
	a, config := journalFixture(t)
	a.client = c
	nc, err := a.currentController(ctx)
	if err != nil {
		t.Fatal(err)
	}
	nc.Spec.Endpoint = c.cfg.Endpoint
	if err := a.statusWriter.Update(ctx, nc); err != nil {
		t.Fatal(err)
	}
	if recovery != "" {
		nc.Status.Configuration = recovered.DeepCopy()
		if err := a.statusWriter.Status().Update(ctx, nc); err != nil {
			t.Fatal(err)
		}
	}
	raw, err := json.Marshal(map[string]any{"catalyst_center": map[string]any{"inventory": map[string]any{"devices": wants}}})
	if err != nil {
		t.Fatal(err)
	}
	config.Spec.ManagedSections = []string{"inventory"}
	if recovery != "" {
		config.Generation = recovered.MetadataHandoff.Operation.ConfigGeneration
		config.Spec.PrunePolicy = configapi.NetworkControllerRetentionPolicyDelete
		raw = []byte(`{"catalyst_center":{"inventory":{"devices":[]}}}`)
	}
	config.Spec.Source.Inline = &runtime.RawExtension{Raw: raw}
	if err := a.statusWriter.Update(ctx, config); err != nil {
		t.Fatal(err)
	}
	dir := recovery
	if dir == "" {
		dir, err = os.MkdirTemp("/private/tmp", "cvk-pnp-cohort-")
		if err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(dir, "intent.json"), raw, 0600); err != nil {
			t.Fatal(err)
		}
	}
	t.Log("private cohort recovery directory", dir)
	persist := func() {
		t.Helper()
		nc, err := a.configurationRecoveryController(ctx)
		if err != nil {
			t.Fatal(err)
		}
		raw, err := json.Marshal(nc.Status.Configuration)
		if err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(dir, "journal.json"), raw, 0600); err != nil {
			t.Fatal(err)
		}
		copy := nc.DeepCopy()
		if err := json.Unmarshal(raw, &copy.Status.Configuration); err != nil {
			t.Fatal(err)
		}
		if err := a.statusWriter.Status().Update(ctx, copy); err != nil {
			t.Fatal(err)
		}
		rebuilt := *a
		a = &rebuilt
	}
	done := recovery != ""
	for range 16 {
		if done {
			break
		}
		done, err = a.reconcilePnPRegistrationStage(ctx, config, nil)
		persist()
		if err != nil {
			t.Fatal("registration uncertain; retained evidence, no replay", err)
		}
		if done {
			break
		}
	}
	if !done {
		t.Fatal("registration cohort incomplete")
	}
	nc, err = a.currentController(ctx)
	if err != nil {
		t.Fatal(err)
	}
	for i, want := range wants {
		receipt, err := ownedPnPRegistrationReceipt(nc, config, want)
		if err != nil || receipt == nil {
			t.Fatal("missing creator", err)
		}
		trace.records[i].deleteID = receipt.CreatedResourceID
		t.Log("owned generated record", receipt.CreatedResourceID, want.Serial)
		if recovery == "" {
			if changed, err := a.retainOwnedPnPRemovalBaseline(ctx, config, nil, want); err != nil || !changed {
				t.Fatal("baseline", err)
			}
		}
		persist()
	}
	creators := journalState(t, a).DeepCopy().ActionApplications
	config.Spec.PrunePolicy = configapi.NetworkControllerRetentionPolicyDelete
	config.Spec.Source.Inline = &runtime.RawExtension{Raw: []byte(`{"catalyst_center":{"inventory":{"devices":[]}}}`)}
	if recovery == "" {
		config.Generation++
	}
	if err := a.statusWriter.Update(ctx, config); err != nil {
		t.Fatal(err)
	}
	source, err := loadControllerSource(ctx, a.statusReader, config)
	if err != nil {
		t.Fatal(err)
	}
	nc, err = a.currentController(ctx)
	if err != nil {
		t.Fatal(err)
	}
	var metadataInstallation contract.BootstrapFenceInstallation
	var metadataPolicy string
	metadataManager := &metadataSubmissionManager{Client: a.statusWriter}
	if recovery != "" {
		metadataInstallation, metadataPolicy = installPnPMetadataSubmissionFixture(t, a, config, source, &metadataSubmissionHarness{fake: true, skipSeal: true})
		if err := a.ConfigureMetadataHandoff(metadataInstallation, metadataPolicy, func(context.Context, *cvk.NetworkController) error { return nil }); err != nil {
			t.Fatal(err)
		}
		current, err := a.currentController(ctx)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := contract.RetireMetadataHandoff(ctx, a.statusReader, metadataManager, current, metadataInstallation, metadataPolicy, pnpMetadataSettlementState, func(context.Context, *cvk.NetworkController) error { return nil }, time.Now()); err != nil {
			t.Fatal("retained completed settlement recovery", err)
		}
		persist()
		nc, err = a.currentController(ctx)
		if err != nil {
			t.Fatal(err)
		}
		t.Log("resumed exact released no-dispatch journal; original native records retained, no registrations replayed")
	}
	cohort, err := ownedPnPRemovalCohort(nc, config, source)
	if err != nil || len(cohort) != 2 {
		t.Fatal("whole cohort", err)
	}
	if _, err := a.planOwnedPnPRemoval(ctx, config, source, cohort[1].Baseline.ID); err == nil {
		t.Fatal("later Delete bypassed order")
	}
	settleMetadata := func() {
		current, err := a.configurationRecoveryController(ctx)
		if err != nil {
			t.Fatal(err)
		}
		authorize := func(context.Context, *cvk.NetworkController) error { return nil }
		released, err := contract.SettleMetadataHandoff(ctx, a.statusReader, metadataManager, current, metadataInstallation, metadataPolicy, pnpMetadataSettlementState, authorize, time.Now())
		persist()
		if err != nil {
			t.Fatal("metadata settlement retained; no replay", err)
		}
		released, err = a.configurationRecoveryController(ctx)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := contract.RetireMetadataHandoff(ctx, a.statusReader, metadataManager, released, metadataInstallation, metadataPolicy, pnpMetadataSettlementState, authorize, time.Now()); err != nil {
			t.Fatal("metadata retirement", err)
		}
		persist()
	}
	publicStep := func(expected string) {
		t.Helper()
		if _, err := (&configReconciler{a: a}).Reconcile(ctx, ctrl.Request{NamespacedName: ctrlclient.ObjectKeyFromObject(config)}); err != nil {
			t.Fatal("public reconciliation", err)
		}
		if err := a.statusReader.Get(ctx, ctrlclient.ObjectKeyFromObject(config), config); err != nil {
			t.Fatal(err)
		}
		condition := meta.FindStatusCondition(config.Status.Conditions, configapi.NetworkControllerConfigConditionReady)
		if condition == nil || condition.Reason != expected || config.Status.Phase == configapi.NetworkControllerConfigPhaseInSync {
			t.Fatal("unexpected public metadata outcome", expected, config.Status.Conditions)
		}
		persist()
	}
	if public {
		metadataInstallation, metadataPolicy = installPnPMetadataSubmissionFixture(t, a, config, source, &metadataSubmissionHarness{fake: true, skipSeal: true})
		if err := a.ConfigureMetadataHandoff(metadataInstallation, metadataPolicy, func(context.Context, *cvk.NetworkController) error { return nil }); err != nil {
			t.Fatal(err)
		}
	}
	for index, entry := range cohort {
		write, err := a.planOwnedPnPRemoval(ctx, config, source, entry.Baseline.ID)
		if err != nil {
			t.Fatal(err)
		}
		if public {
			publicStep("PnPRemovalHandoffPending")
			prepared := journalState(t, a).Operation
			if prepared == nil || prepared.TargetID != entry.Baseline.ID || prepared.Phase != "Prepared" {
				t.Fatal("public preparation changed deterministic target")
			}
			publicStep("PnPRemovalHandoffPending")
			sealPnPMetadataSubmissionFixture(t, a, config, source, metadataManager, metadataInstallation, metadataPolicy)
			persist()
			if options.cancellation && index == 0 {
				current, err := a.configurationRecoveryController(ctx)
				if err != nil {
					t.Fatal(err)
				}
				originalOperation := current.Status.Configuration.Operation.DeepCopy()
				retainedCreators := current.Status.Configuration.DeepCopy().ActionApplications
				config.Generation++
				config.Spec.ModelSource.Exporter = "cvk-cancellation-qualification"
				if err := a.statusWriter.Update(ctx, config); err != nil {
					t.Fatal(err)
				}
				requestsBefore := trace.requests
				if _, err := contract.CancelMetadataHandoff(ctx, a.statusReader, metadataManager, current, metadataInstallation, metadataPolicy, pnpMetadataCancellationState, func(context.Context, *cvk.NetworkController) error { return nil }, time.Now()); err != nil {
					persist()
					t.Fatal("prepared cancellation retained; no native replay", err)
				}
				persist()
				settleMetadata()
				state := journalState(t, a)
				if state.Operation != nil || state.MetadataHandoff != nil || !reflect.DeepEqual(retainedCreators, state.ActionApplications) || trace.requests != requestsBefore || trace.records[0].deletes+trace.records[1].deletes != 0 {
					t.Fatal("cancellation released ownership or contacted native API")
				}
				source, err = loadControllerSource(ctx, a.statusReader, config)
				if err != nil {
					t.Fatal(err)
				}
				publicStep("PnPRemovalHandoffPending")
				if journalState(t, a).Operation.ID == originalOperation.ID {
					t.Fatal("cancelled journal reused for new intent")
				}
				sealPnPMetadataSubmissionFixture(t, a, config, source, metadataManager, metadataInstallation, metadataPolicy)
				persist()
				t.Log("original Prepared cleanup cancelled and settled with zero native requests; fresh edited-intent journal admitted")
			}
			publicStep("PnPRemovalSubmitted")
			publicStep("PnPRemovalVerified")
			if _, err := (&configReconciler{a: a}).Reconcile(ctx, ctrl.Request{NamespacedName: ctrlclient.ObjectKeyFromObject(config)}); err == nil {
				t.Fatal("terminal recovery ignored retained metadata custody")
			}
			if options.epoch && index == len(cohort)-1 {
				n, err := a.configurationRecoveryController(ctx)
				if err != nil {
					t.Fatal(err)
				}
				n.Spec.Paused = true
				n.Generation++
				if err := a.statusWriter.Update(ctx, n); err != nil {
					t.Fatal(err)
				}
				t.Log("paused controller after Verified; original generation retained for settlement", n.Generation, n.Status.Configuration.MetadataHandoff.Operation.ControllerGeneration)
			}
			settleMetadata()
			originalSource := config.Spec.Source.DeepCopy()
			if index == 0 {
				config.Spec.Source.Inline = nil
				config.Spec.Source.ConfigMapRef = &configapi.ConfigMapKeyRef{Name: "qualification-missing-source", Key: "intent.yaml"}
			} else {
				config.Spec.Source.Inline = &runtime.RawExtension{Raw: []byte(`{"unrelated":true}`)}
			}
			if err := a.statusWriter.Update(ctx, config); err != nil {
				t.Fatal(err)
			}
			requestsBefore := trace.requests
			if _, err := (&configReconciler{a: a}).Reconcile(ctx, ctrl.Request{NamespacedName: ctrlclient.ObjectKeyFromObject(config)}); err != nil {
				t.Fatal("source-independent terminal recovery", err)
			}
			if journalState(t, a).Operation != nil || trace.requests != requestsBefore {
				t.Fatal("terminal recovery retained journal or contacted appliance")
			}
			persist()
			config.Spec.Source = *originalSource
			if err := a.statusWriter.Update(ctx, config); err != nil {
				t.Fatal(err)
			}
			t.Log("public metadata preparation, dispatch, verification and settlement completed", entry.Baseline.ID)
			continue
		}
		if metadata {
			op, err := a.prepareConfiguration(ctx, config, source, "inventory", write.resource, "Delete", write.identity)
			if err != nil {
				t.Fatal(err)
			}
			if index == 0 && recovery == "" {
				metadataInstallation, metadataPolicy = installPnPMetadataSubmissionFixture(t, a, config, source)
				if err := a.ConfigureMetadataHandoff(metadataInstallation, metadataPolicy, func(context.Context, *cvk.NetworkController) error { return nil }); err != nil {
					t.Fatal(err)
				}
			} else {
				sealPnPMetadataSubmissionFixture(t, a, config, source, metadataManager, metadataInstallation, metadataPolicy)
			}
			persist()
			if index == 0 && recovery == "" {
				refused := false
				trace.beforeRead = func() error {
					current, err := a.currentController(ctx)
					if err != nil {
						return err
					}
					if current.Status.Configuration.Operation != nil && current.Status.Configuration.Operation.Phase == "Submitting" && !refused {
						refused = true
						return errors.New("qualification original metadata pre-HTTP refusal")
					}
					return nil
				}
				err = a.resumeOwnedPnPMetadataRemoval(ctx, config, source, op)
				persist()
				if !refused || !errors.Is(err, errConfigurationNotDispatched) || journalState(t, a).Operation != nil || journalState(t, a).MetadataHandoff.RefusedAt == nil || trace.records[0].deletes+trace.records[1].deletes != 0 {
					t.Fatal("metadata refusal not certified before any Delete", err)
				}
				trace.beforeRead = nil
				settleMetadata()
				write, err = a.planOwnedPnPRemoval(ctx, config, source, entry.Baseline.ID)
				if err != nil {
					t.Fatal(err)
				}
				op, err = a.prepareConfiguration(ctx, config, source, "inventory", write.resource, "Delete", write.identity)
				if err != nil {
					t.Fatal(err)
				}
				sealPnPMetadataSubmissionFixture(t, a, config, source, metadataManager, metadataInstallation, metadataPolicy)
				t.Log("original metadata refusal settled with zero Deletes; fresh operation and cohort admitted")
			}
			if err := a.resumeOwnedPnPMetadataRemoval(ctx, config, source, op); err != nil {
				persist()
				t.Fatal("metadata Delete uncertain; no replay", err)
			}
			persist()
			current := journalState(t, a).Operation
			if current == nil || current.Phase != "Verifying" || current.ID != op.ID {
				t.Fatal("original metadata acknowledgement missing")
			}
			if err := a.recordOwnedPnPRemoval(ctx, config, source, op.ID); err != nil {
				t.Fatal(err)
			}
			persist()
			settleMetadata()
			if err := a.finishConfiguration(ctx, op.ID); err != nil {
				t.Fatal(err)
			}
			persist()
			t.Log("original metadata removal and settlement completed", entry.Baseline.ID)
			continue
		}
		if index == 0 {
			refused := false
			trace.beforeRead = func() error {
				current, err := a.currentController(ctx)
				if err != nil {
					return err
				}
				if op := current.Status.Configuration.Operation; op != nil && op.Phase == "Submitting" && !refused {
					refused = true
					return errors.New("qualification pre-HTTP cohort read refusal")
				}
				return nil
			}
			err = a.submitConfigurationWrite(ctx, config, source, "inventory", write)
			persist()
			if !refused || !errors.Is(err, errConfigurationNotDispatched) || journalState(t, a).Operation != nil || trace.records[0].deletes+trace.records[1].deletes != 0 {
				t.Fatal("cohort refusal did not retire before any Delete", err)
			}
			trace.beforeRead = nil
			t.Log("whole cohort pre-HTTP refusal retired with zero Deletes; fresh plan required")
			write, err = a.planOwnedPnPRemoval(ctx, config, source, entry.Baseline.ID)
			if err != nil {
				t.Fatal(err)
			}
		}
		if err := a.submitConfigurationWrite(ctx, config, source, "inventory", write); err != nil {
			persist()
			t.Fatal("Delete uncertain; do not replay", err)
		}
		persist()
		op := journalState(t, a).Operation
		if op == nil || op.Phase != "Verifying" || op.TargetID != entry.Baseline.ID {
			t.Fatal("missing acknowledged UUID")
		}
		if err := a.recordOwnedPnPRemoval(ctx, config, source, op.ID); err != nil {
			t.Fatal(err)
		}
		persist()
		if err := a.finishConfiguration(ctx, op.ID); err != nil {
			t.Fatal(err)
		}
		persist()
		t.Log("ordered receipt-gated removal completed", entry.Baseline.ID)
	}
	if public && !options.epoch {
		publicStep("PnPRemovalComplete")
		publicStep("PnPRemovalComplete")
	}
	state := journalState(t, a)
	if state.Operation != nil || len(state.ActionApplications) != 4 || !reflect.DeepEqual(creators, state.ActionApplications[:2]) {
		t.Fatal("creator/tombstone evidence changed")
	}
	for _, record := range trace.records {
		if record.posts != 1 || record.deletes != 1 {
			t.Fatal("mutation replay")
		}
	}
	for range 2 {
		current, err := c.pnpDevices(ctx)
		if err != nil || recovery == "" && !reflect.DeepEqual(baseline, current) || recovery != "" && len(current) != 0 {
			t.Fatal("catalog baseline not restored", err)
		}
		currentNetwork, err := c.configurationDevices(ctx)
		if err != nil || !reflect.DeepEqual(network, currentNetwork) {
			t.Fatal("network baseline changed", err)
		}
	}
	if metadata && !public {
		t.Log("metadata claim/refusal/verified-absence/UID settlement/retirement cycle completed; native baselines restored; Kubernetes/runtime authority and persistence simulated")
	}
	t.Log("two generated registrations and two exact Deletes; original creators plus tombstones retained; native baselines restored; Kubernetes persistence simulated JSON")
}
