// Copyright 2026 Cisco Systems Inc.
// SPDX-License-Identifier: Apache-2.0
//go:build live

package catalystcenter

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	configapi "github.com/cisco/virtual-kubelet-cisco/api/config/v1alpha1"
	cvk "github.com/cisco/virtual-kubelet-cisco/api/v1alpha1"
	configurationcontract "github.com/cisco/virtual-kubelet-cisco/internal/controllerconfiguration"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	ctrl "sigs.k8s.io/controller-runtime"
	ctrlclient "sigs.k8s.io/controller-runtime/pkg/client"
)

// The native-assisted bootstrap test must refuse runtime admission before any
// Kubernetes write. This guard makes an accidental early write observable.
type liveBootstrapWriteGuard struct {
	ctrlclient.Writer
	creates int
}

func (g *liveBootstrapWriteGuard) Create(context.Context, ctrlclient.Object, ...ctrlclient.CreateOption) error {
	g.creates++
	return errors.New("unexpected bootstrap lease creation")
}

type livePnPRegistrationTrace struct {
	base           http.RoundTripper
	payload        []byte
	deleteID       string
	posts, deletes int
	deleteAckShape string
	beforeRead     func() error
}

func (trace *livePnPRegistrationTrace) RoundTrip(req *http.Request) (*http.Response, error) {
	if req.Method == http.MethodGet && req.URL.Path == pnpDevicesPath && trace.beforeRead != nil {
		if err := trace.beforeRead(); err != nil {
			return nil, err
		}
	}
	if req.Method != http.MethodGet && req.URL.Path != authPath {
		switch {
		case req.Method == http.MethodPost && req.URL.Path == pnpDevicesPath && trace.posts == 0:
			raw, err := io.ReadAll(req.Body)
			if err != nil {
				return nil, err
			}
			req.Body = io.NopCloser(bytes.NewReader(raw))
			var actual, expected any
			if json.Unmarshal(raw, &actual) != nil || json.Unmarshal(trace.payload, &expected) != nil || !reflect.DeepEqual(actual, expected) {
				return nil, errors.New("PnP qualification refused unexpected registration")
			}
			trace.posts++
		case req.Method == http.MethodDelete && trace.deleteID != "" && req.URL.Path == pnpDevicesPath+"/"+trace.deleteID && trace.deletes == 0:
			trace.deletes++
		default:
			return nil, errors.New("PnP qualification refused an out-of-scope or repeated write")
		}
	}
	response, err := trace.base.RoundTrip(req)
	if err == nil && response != nil && req.Method == http.MethodDelete && response.Body != nil {
		raw, readErr := io.ReadAll(response.Body)
		response.Body.Close()
		response.Body = io.NopCloser(bytes.NewReader(raw))
		if readErr != nil {
			return nil, readErr
		}
		var fields map[string]json.RawMessage
		shape := map[string]any{"status": response.StatusCode, "validObject": json.Unmarshal(raw, &fields) == nil}
		var id string
		_ = json.Unmarshal(fields["id"], &id)
		shape["rootIDMatches"] = id == trace.deleteID
		shape["hasDeviceInfo"] = len(fields["deviceInfo"]) > 0
		shape["hasResponse"] = len(fields["response"]) > 0
		var audit []json.RawMessage
		_ = json.Unmarshal(fields["runSummaryList"], &audit)
		shape["auditCount"] = len(audit)
		safe, _ := json.Marshal(shape)
		trace.deleteAckShape = string(safe)
	}
	return response, err
}

// The fixture has a generated, nonexistent serial. Only database registration
// and exact-UUID removal are permitted; there is no claim/reset/device endpoint.
func TestLivePnPRegistrationStageExecution(t *testing.T) {
	if os.Getenv("CATC_PNP_REGISTRATION_EXECUTION") != "isolated-unclaimed-record" {
		t.Skip("isolated PnP registration qualification not enabled")
	}
	qualifyLivePnPRegistrationStage(t, false, false, "")
}

func TestLivePnPRegistrationPublicExecution(t *testing.T) {
	if os.Getenv("CATC_PNP_REGISTRATION_PUBLIC_EXECUTION") != "isolated-unclaimed-record" {
		t.Skip("public PnP registration qualification not enabled")
	}
	qualifyLivePnPRegistrationStage(t, true, false, "")
}

func TestLivePnPRegistrationGuardedRemoval(t *testing.T) {
	if os.Getenv("CATC_PNP_REMOVAL_PRIMITIVE") != "verified-uncontacted-record" {
		t.Skip("guarded PnP removal qualification disabled")
	}
	qualifyLivePnPRegistrationStage(t, true, false, "guarded-removal")
}

func TestLivePnPRemovalBaseline(t *testing.T) {
	if os.Getenv("CATC_PNP_REMOVAL_BASELINE") != "owned-canonical-baseline" {
		t.Skip("owned PnP baseline qualification disabled")
	}
	qualifyLivePnPRegistrationStage(t, true, false, "removal-baseline")
}

func TestLivePnPJournalRemoval(t *testing.T) {
	if os.Getenv("CATC_PNP_DELETE_JOURNAL") != "canonical-ack-recovery" {
		t.Skip("PnP journal removal qualification disabled")
	}
	qualifyLivePnPRegistrationStage(t, true, false, "journal-removal")
}

func TestLivePnPJournalPublicRecovery(t *testing.T) {
	if os.Getenv("CATC_PNP_PUBLIC_REMOVAL_RECOVERY") != "acknowledged-only" {
		t.Skip("public PnP removal recovery disabled")
	}
	qualifyLivePnPRegistrationStage(t, true, false, "journal-public")
}

func TestLivePnPRemovalNoDispatch(t *testing.T) {
	if os.Getenv("CATC_PNP_REMOVAL_NO_DISPATCH") != "pre-http-refusal" {
		t.Skip("PnP removal no-dispatch qualification disabled")
	}
	qualifyLivePnPRegistrationStage(t, true, false, "journal-no-dispatch")
}

func TestLivePnPPreparedClaimHandoff(t *testing.T) {
	if os.Getenv("CATC_PNP_CLAIM_PREPARATION") != "isolated-unclaimed-record" {
		t.Skip("isolated native claim preparation qualification not enabled")
	}
	qualifyLivePnPRegistrationStage(t, false, true, "")
}

func TestLivePnPRegistrationNoDispatchReplan(t *testing.T) {
	if os.Getenv("CATC_PNP_REGISTRATION_NO_DISPATCH") != "isolated-unclaimed-record" {
		t.Skip("isolated PnP pre-dispatch qualification not enabled")
	}
	qualifyLivePnPRegistrationStage(t, true, false, "cohort")
}

func TestLivePnPRegistrationPrimitiveNoDispatchReplan(t *testing.T) {
	if os.Getenv("CATC_PNP_PRIMITIVE_NO_DISPATCH") != "isolated-unclaimed-record" {
		t.Skip("isolated primitive pre-dispatch qualification disabled")
	}
	qualifyLivePnPRegistrationStage(t, true, false, "primitive")
}

func TestLivePnPRegistrationCapacityAdmission(t *testing.T) {
	if os.Getenv("CATC_PNP_CAPACITY_EXECUTION") != "isolated-unclaimed-record" {
		t.Skip("native PnP capacity qualification disabled")
	}
	for _, scenario := range []string{"capacity-count", "capacity-bytes", "capacity-cohort"} {
		t.Run(scenario, func(t *testing.T) { qualifyLivePnPRegistrationStage(t, true, false, scenario) })
	}
}

func TestLivePnPRegistrationPrivatePlannerExecution(t *testing.T) {
	if os.Getenv("CATC_PNP_PRIVATE_PLANNER_EXECUTION") != "isolated-unclaimed-record" {
		t.Skip("private planner qualification disabled")
	}
	qualifyLivePnPRegistrationStage(t, true, false, "private-planner")
}

func qualifyLivePnPRegistrationStage(t *testing.T, public, prepareClaim bool, scenario string) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	c := liveClient(t)
	before, err := c.pnpDevices(ctx)
	if err != nil || len(before) != 0 {
		t.Fatal("qualification requires an empty PnP database", err)
	}
	network, err := c.configurationDevices(ctx)
	if err != nil {
		t.Fatal("network baseline unavailable", err)
	}
	sites, err := c.ListSites(ctx)
	if err != nil {
		t.Fatal("site baseline unavailable", err)
	}
	site := ""
	for _, s := range sites {
		if s.Type != "global" && validSitePath(s.NameHierarchy) {
			site = s.NameHierarchy
			break
		}
	}
	if site == "" {
		t.Fatal("no existing non-global site for the declared model")
	}
	suffix := uint32(time.Now().UnixNano())
	want := pnpDeviceIntent{Name: fmt.Sprintf("cvk-q-registration-%08x", suffix), State: "PNP", Serial: fmt.Sprintf("CVK%08X", suffix), Site: site, PID: "C9300-48P"}
	payload, err := pnpRegistrationPayload(want)
	if err != nil {
		t.Fatal(err)
	}
	rest, _, err := c.session(ctx)
	if err != nil {
		t.Fatal(err)
	}
	base := rest.HTTPClient.Transport
	if base == nil {
		base = http.DefaultTransport
	}
	trace := &livePnPRegistrationTrace{base: base, payload: payload}
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
	declared := []pnpDeviceIntent{want}
	if scenario == "capacity-cohort" {
		second := want
		second.Name += "-second"
		second.Serial = fmt.Sprintf("CVK%08X", suffix+1)
		declared = append(declared, second)
	}
	raw, _ := json.Marshal(map[string]any{"catalyst_center": map[string]any{"inventory": map[string]any{"devices": declared}}})
	config.Spec.Source.Inline = &runtime.RawExtension{Raw: raw}
	config.Spec.ManagedSections = []string{"inventory"}
	config.Spec.Mode = configapi.NetworkControllerApplyModeApply
	if err := a.statusWriter.Update(ctx, config); err != nil {
		t.Fatal(err)
	}
	recoveryDir, err := os.MkdirTemp("/private/tmp", "cvk-pnp-registration-")
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(recoveryDir, "intent.json"), raw, 0600); err != nil {
		t.Fatal(err)
	}
	baselineRaw, err := json.Marshal(network)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(recoveryDir, "network-baseline.json"), baselineRaw, 0600); err != nil {
		t.Fatal(err)
	}
	t.Log("private registration recovery directory", recoveryDir)
	persist := func() {
		t.Helper()
		nc, err := a.currentController(ctx)
		if err != nil {
			t.Fatal(err)
		}
		raw, err := json.Marshal(nc.Status.Configuration)
		if err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(recoveryDir, "journal.json"), raw, 0600); err != nil {
			t.Fatal(err)
		}
		copy := nc.DeepCopy()
		if err := json.Unmarshal(raw, &copy.Status.Configuration); err != nil {
			t.Fatal(err)
		}
		if err := a.statusWriter.Status().Update(ctx, copy); err != nil {
			t.Fatal(err)
		}
	}
	privateFirst := scenario == "private-planner"
	runStage := func() (bool, error) {
		if privateFirst {
			privateFirst = false
			write, id, err := a.planPnPRegistrationStage(ctx, config, nil, want)
			if err != nil || write == nil || id != "" {
				return false, errors.New("private planner did not produce a new guarded registration")
			}
			return false, a.submitConfigurationWrite(ctx, config, nil, "inventory", write)
		}
		if !public {
			return a.reconcilePnPRegistrationStage(ctx, config, nil)
		}
		if _, err := (&configReconciler{a: a}).Reconcile(ctx, ctrl.Request{NamespacedName: ctrlclient.ObjectKeyFromObject(config)}); err != nil {
			return false, err
		}
		if err := a.statusReader.Get(ctx, ctrlclient.ObjectKeyFromObject(config), config); err != nil {
			return false, err
		}
		if config.Status.Phase == configapi.NetworkControllerConfigPhaseInSync {
			return false, errors.New("public registration incorrectly published onboarding InSync")
		}
		for _, condition := range config.Status.Conditions {
			if condition.Type == configapi.NetworkControllerConfigConditionReady {
				if condition.Status == metav1.ConditionTrue {
					return false, errors.New("public registration incorrectly published Ready")
				}
				return condition.Reason == "PnPClaimAdmissionUnavailable", nil
			}
		}
		return false, errors.New("public registration did not publish readiness condition")
	}
	if strings.HasPrefix(scenario, "capacity-") {
		current, err := a.currentController(ctx)
		if err != nil {
			t.Fatal(err)
		}
		count := 256
		if scenario == "capacity-cohort" {
			count = 255
		}
		if scenario == "capacity-bytes" {
			count = 2
		}
		// This is synthetic occupancy in the test's simulated API only. No
		// installed ledger is edited or receipt evicted to make space.
		current.Status.Configuration = &cvk.NetworkControllerConfigurationStatus{Owners: []cvk.NetworkControllerSectionOwner{{Section: "inventory", ConfigName: config.Name, ConfigUID: string(config.UID)}}, ActionApplications: pnpCapacityHistory(count, scenario == "capacity-bytes")}
		if err := a.statusWriter.Status().Update(ctx, current); err != nil {
			t.Fatal(err)
		}
		persist()
		baseline := journalState(t, a).DeepCopy()
		for range 2 {
			done, err := runStage()
			persist()
			if err != nil || done || trace.posts != 0 || trace.deletes != 0 || !reflect.DeepEqual(baseline, journalState(t, a)) {
				t.Fatal("native capacity gate changed ownership or dispatched", err)
			}
		}
		capacity := false
		for _, condition := range config.Status.Conditions {
			if condition.Type == configapi.NetworkControllerConfigConditionReady && condition.Reason == "PnPRegistrationCapacityUnavailable" {
				capacity = true
			}
		}
		if !capacity {
			t.Fatal("missing native capacity status")
		}
		for range 2 {
			rows, err := c.pnpDevices(ctx)
			if err != nil || !reflect.DeepEqual(before, rows) {
				t.Fatal("capacity gate changed PnP baseline", err)
			}
			current, err := c.configurationDevices(ctx)
			if err != nil || !reflect.DeepEqual(network, current) {
				t.Fatal("capacity gate changed network baseline", err)
			}
		}
		t.Log("native capacity gate preserved all synthetic receipts, original native baselines and zero POST/DELETE counts", scenario)
		return
	}
	if scenario == "cohort" || scenario == "primitive" {
		refused := 0
		activeReads := 0
		trace.beforeRead = func() error {
			current, err := a.currentController(ctx)
			if err != nil {
				return err
			}
			if current.Status.Configuration != nil && current.Status.Configuration.Operation != nil && current.Status.Configuration.Operation.Phase == "Submitting" {
				activeReads++
				if scenario == "primitive" && activeReads < 3 {
					return nil
				}
				refused++
				raw, _ := json.Marshal(current.Status.Configuration.Operation)
				if err := os.WriteFile(filepath.Join(recoveryDir, "no-dispatch-original.json"), raw, 0600); err != nil {
					return err
				}
				return errors.New("qualification-injected final preflight read failure")
			}
			return nil
		}
		done, err := runStage()
		persist()
		state := journalState(t, a)
		if err != nil || done || refused == 0 || trace.posts != 0 || state == nil || state.Operation != nil || len(state.Owners) != 1 || len(state.ActionApplications) != 0 {
			t.Fatal("pre-dispatch proof failed to retire without native mutation", err)
		}
		if scenario == "primitive" && activeReads != 3 {
			t.Fatal("primitive refusal did not follow both cohort observations", activeReads)
		}
		trace.beforeRead = nil
		t.Log(scenario, "original final-preflight refusal retired with zero native POSTs; inventory owner retained")
	}
	done := false
	for step := 0; step < 8; step++ {
		done, err = runStage()
		persist()
		if err != nil {
			t.Fatal("registration stage stopped; retained evidence must be inspected without replay", err)
		}
		// Rebuild the worker between every persisted journal transition.
		rebuilt := *a
		a = &rebuilt
		if done {
			break
		}
	}
	if !done || trace.posts != 1 || journalState(t, a).Operation != nil || len(journalState(t, a).ActionApplications) != 1 {
		t.Fatal("registration lifecycle incomplete")
	}
	nc, err = a.currentController(ctx)
	if err != nil {
		t.Fatal(err)
	}
	receipt, err := ownedPnPRegistrationReceipt(nc, config, want)
	if err != nil || receipt == nil {
		t.Fatal("registration ownership unavailable", err)
	}
	id := receipt.CreatedResourceID
	if scenario == "private-planner" {
		write, reusedID, err := a.planPnPRegistrationStage(ctx, config, nil, want)
		if err != nil || write != nil || reusedID != id || trace.posts != 1 {
			t.Fatal("private planner did not reuse public-finalized ownership", err)
		}
		t.Log("private planner submission resumed through public reconciliation; exact owned UUID reused without POST", id)
	}
	t.Log("owned native registration", id, want.Serial)
	for range 2 {
		done, err = runStage()
		if err != nil || !done || trace.posts != 1 {
			t.Fatal("owned registration no-op failed", err)
		}
	}

	if prepareClaim {
		op, err := a.prepareOwnedPnPSiteClaim(ctx, config, nil, want, nil)
		if err != nil {
			t.Fatal("native claim preparation", err)
		}
		persist()
		rebuilt := *a
		a = &rebuilt
		retained := journalState(t, a).DeepCopy()
		current, err := a.currentController(ctx)
		if err != nil {
			t.Fatal(err)
		}
		observation, err := configurationcontract.ReadBootstrapIdentity(ctx, a.statusReader, current)
		if err != nil || observation.NativeTargetID != id || observation.OperationID != op.ID || observation.PhysicalIdentity != op.TargetSerial {
			t.Fatal("source-bound bootstrap identity observation", err)
		}
		t.Log("shared bootstrap identity observation verified against simulated Kubernetes state; no grant produced")
		guard := &liveBootstrapWriteGuard{Writer: a.statusWriter}
		runtimeDenied := errors.New("bootstrap runtime is not installed")
		fence, fenceErr := configurationcontract.ReserveBootstrapFence(ctx, a.statusReader, guard, current, configurationcontract.BootstrapFenceInstallation{LeaseNamespace: "leases"},
			func(context.Context, string, *cvk.NetworkController, configurationcontract.BootstrapIdentityObservation) error {
				return runtimeDenied
			})
		if !errors.Is(fenceErr, runtimeDenied) || fence != nil || guard.creates != 0 || !reflect.DeepEqual(retained, journalState(t, a)) {
			t.Fatal("uninstalled bootstrap runtime created a fence or changed its journal", fenceErr)
		}
		t.Log("bootstrap serial fence refused uninstalled runtime before any Kubernetes write; no native grant produced")
		denied := configurationAdmissionStep(func(context.Context) (configurationDispatchCheck, error) { return nil, errConfigurationDeviceAdmission })
		err = a.resumeOwnedPnPSiteClaim(ctx, config, nil, want, nil, retained.Operation, denied)
		if err == nil || trace.posts != 1 || trace.deletes != 0 || !reflect.DeepEqual(retained, journalState(t, a)) || op.Phase != "Prepared" {
			t.Fatal("unadmitted native claim preparation dispatched or changed its journal", err)
		}
		t.Log("native Prepared v2 claim rebuilt and revalidated; denied admission retained the exact journal without site-claim POST", op.ID)
	}
	if scenario == "removal-baseline" || scenario == "journal-removal" || scenario == "journal-public" || scenario == "journal-no-dispatch" {
		initial := journalState(t, a).DeepCopy()
		if changed, err := a.retainOwnedPnPRemovalBaseline(ctx, config, nil, want); err != nil || !changed {
			t.Fatal("baseline capture", changed, err)
		}
		persist()
		// A new adapter instance observes the serialized enriched receipt. Public
		// registration remains an owned no-op; no extra registration is permitted.
		rebuilt := *a
		a = &rebuilt
		if done, err := runStage(); err != nil || !done {
			t.Fatal("public registration with enriched receipt", done, err)
		}
		if changed, err := a.retainOwnedPnPRemovalBaseline(ctx, config, nil, want); err != nil || changed {
			t.Fatal("baseline restart no-op", changed, err)
		}
		retained := journalState(t, a)
		if len(retained.ActionApplications) != len(initial.ActionApplications) || !reflect.DeepEqual(retained.ActionApplications[0].Operation, initial.ActionApplications[0].Operation) || !reflect.DeepEqual(retained.ActionApplications[0].VerifiedAt, initial.ActionApplications[0].VerifiedAt) {
			t.Fatal("baseline changed original creator")
		}
		baseline, err := decodePnPRecord(retained.ActionApplications[0].Resource)
		if err != nil || baseline.Serial == "" || baseline.ID != id {
			t.Fatal("canonical baseline missing", err)
		}
		persist()
		t.Log("canonical removal baseline retained with unchanged original operation/time; serialized restart no extra POST", id, baseline.Serial)
	}
	if scenario == "journal-removal" || scenario == "journal-public" || scenario == "journal-no-dispatch" {
		config.Spec.PrunePolicy = configapi.NetworkControllerRetentionPolicyDelete
		config.Spec.Source = configapi.NetworkControllerConfigurationSource{Inline: &runtime.RawExtension{Raw: []byte(`{"catalyst_center":{"inventory":{"devices":[]}}}`)}}
		config.Generation++
		if err := a.statusWriter.Update(ctx, config); err != nil {
			t.Fatal(err)
		}
		source, err := loadControllerSource(ctx, a.statusReader, config)
		if err != nil {
			t.Fatal(err)
		}
		write, err := a.planOwnedPnPRemoval(ctx, config, source, id)
		if err != nil {
			t.Fatal("source-independent journal plan", err)
		}
		if scenario == "journal-no-dispatch" {
			refused := false
			trace.beforeRead = func() error {
				current, err := a.currentController(ctx)
				if err != nil {
					return err
				}
				if op := current.Status.Configuration.Operation; op != nil && op.Phase == "Submitting" && !refused {
					refused = true
					return errors.New("qualification pre-HTTP read refusal")
				}
				return nil
			}
			err := a.submitConfigurationWrite(ctx, config, source, "inventory", write)
			current, readErr := a.currentController(ctx)
			if readErr != nil {
				t.Fatal(readErr)
			}
			if !refused || !errors.Is(err, errConfigurationNotDispatched) || current.Status.Configuration.Operation != nil || trace.deletes != 0 {
				t.Fatal("native no-dispatch proof failed", err)
			}
			trace.beforeRead = nil
			write, err = a.planOwnedPnPRemoval(ctx, config, source, id)
			if err != nil {
				t.Fatal("fresh plan after refusal", err)
			}
			t.Log("pre-HTTP refusal retired through shared proof; fresh plan required, no Delete yet", id)
		}
		trace.deleteID = id
		if err := a.submitConfigurationWrite(ctx, config, source, "inventory", write); err != nil {
			t.Fatal("journal Delete outcome uncertain; do not replay", err, trace.deleteAckShape)
		}
		persist()
		current, err := a.currentController(ctx)
		if err != nil {
			t.Fatal(err)
		}
		op := current.Status.Configuration.Operation
		if op == nil || op.Phase != "Verifying" || !validPnPRemovalOperation(op) {
			t.Fatal("acknowledgement not durably retained")
		}
		rebuilt := *a
		a = &rebuilt
		if scenario == "journal-public" {
			if _, err := (&configReconciler{a: a}).Reconcile(ctx, ctrl.Request{NamespacedName: ctrlclient.ObjectKeyFromObject(config)}); err != nil {
				t.Fatal(err)
			}
		} else {
			if err := a.recordOwnedPnPRemoval(ctx, config, source, op.ID); err != nil {
				t.Fatal("restarted absence verification", err)
			}
		}
		current, err = a.currentController(ctx)
		if err != nil {
			t.Fatal(err)
		}
		if !pnpRemovedReceiptPresent(current.Status.Configuration, current.Status.Configuration.Operation) {
			t.Fatal("atomic absence receipt missing")
		}
		if scenario == "journal-public" {
			for range 2 {
				if _, err := (&configReconciler{a: a}).Reconcile(ctx, ctrl.Request{NamespacedName: ctrlclient.ObjectKeyFromObject(config)}); err != nil {
					t.Fatal(err)
				}
			}
		} else {
			if err := a.finishConfiguration(ctx, op.ID); err != nil {
				t.Fatal("receipt-gated release", err)
			}
		}
		persist()
		if _, err := a.planOwnedPnPRemoval(ctx, config, source, id); err == nil {
			t.Fatal("removed record replanned")
		}
		for range 2 {
			rows, err := c.pnpDevices(ctx)
			if err != nil || !reflect.DeepEqual(rows, before) {
				t.Fatal("PnP baseline not restored", err)
			}
		}
		devices, err := c.configurationDevices(ctx)
		if err != nil || !reflect.DeepEqual(devices, network) {
			t.Fatal("network baseline changed", err)
		}
		if trace.posts != 1 || trace.deletes != 1 {
			t.Fatal("journal replayed mutation", trace.posts, trace.deletes)
		}
		t.Log("canonical source-independent Delete acknowledgement, restart, atomic absence receipt and release verified", id)
		t.Log("safe Delete acknowledgement structure", trace.deleteAckShape)
		return
	}
	if scenario == "guarded-removal" || scenario == "removal-baseline" {
		owner, err := a.currentController(ctx)
		if err != nil {
			t.Fatal(err)
		}
		created, err := ownedPnPRegistrationReceipt(owner, config, want)
		if err != nil || created == nil || created.CreatedResourceID != id {
			t.Fatal("missing original ownership", err)
		}
		authority := func(ctx context.Context) error {
			if err := a.checkConfigurationSnapshot(ctx, config, nil); err != nil {
				return err
			}
			current, err := a.currentController(ctx)
			if err != nil {
				return err
			}
			if current.UID != owner.UID || current.Generation != owner.Generation || current.ResourceVersion != owner.ResourceVersion || current.Status.Configuration.Operation != nil {
				return errConfigurationStale
			}
			retained, err := ownedPnPRegistrationReceipt(current, config, want)
			if err != nil {
				return err
			}
			if !reflect.DeepEqual(created, retained) {
				return errConfigurationOwned
			}
			devices, err := c.configurationDevices(ctx)
			if err != nil {
				return err
			}
			if !reflect.DeepEqual(network, devices) {
				return errConfigurationStale
			}
			return nil
		}
		plan, err := c.planPnPRegistrationRemoval(ctx, want, created, authority)
		if err != nil {
			t.Fatal("guarded removal preparation", err)
		}
		trace.deleteID = id
		if err := plan.submit(ctx); err != nil {
			t.Fatal("guarded removal acknowledgement uncertain; do not replay", err, trace.deleteAckShape)
		}
		if done, err := plan.observeAbsent(ctx); err != nil || !done {
			t.Fatal("guarded removal absence proof", done, err)
		}
		if err := plan.submit(ctx); err == nil || trace.deletes != 1 {
			t.Fatal("guarded removal replay", err, trace.deletes)
		}
		after, err := c.pnpDevices(ctx)
		if err != nil || !reflect.DeepEqual(before, after) {
			t.Fatal("PnP baseline not restored", err)
		}
		t.Log("original verified Create guarded exact UUID removal and repeated absence; no public Delete receipt or device operation", id)
		t.Log("safe Delete acknowledgement structure", trace.deleteAckShape)
	} else {
		cleanupLivePnPRegistrationFixture(t, ctx, c, trace, want, id, network, before)
	}

	retained := journalState(t, a).DeepCopy()
	for range 2 {
		done, err := runStage()
		if (!public && err == nil) || (public && err != nil) || done || trace.posts != 1 || !reflect.DeepEqual(retained, journalState(t, a)) {
			t.Fatal("missing owned registration was adopted/recreated or its evidence changed", err)
		}
	}

	t.Log("public reconciler qualification", public)
	t.Log("registration, serialized journal stages, owned no-op and exact UUID cleanup verified; no claim/reset/device action submitted")
}

func cleanupLivePnPRegistrationFixture(t *testing.T, ctx context.Context, c *client, trace *livePnPRegistrationTrace, want pnpDeviceIntent, id string, network []Device, before []pnpDevice) {
	t.Helper()
	serials, err := validatePnPDeviceIntent(want)
	if err != nil || len(serials) != 1 || !validAPIID(id) {
		t.Fatal("invalid exact fixture identity", err)
	}
	// Supervised fixture cleanup is separate from public prune/delete support.
	// Reobserve exact ownership and require the record to remain wholly unclaimed.
	for range 2 {
		rows, err := c.pnpDevices(ctx)
		if err != nil || len(rows) != 1 {
			t.Fatal("unexpected PnP cleanup scope", err)
		}
		row := rows[0]
		if !pnpRegistrationMatches(pnpRegistrationReceipt{deviceID: id, serial: serials[0], pid: want.PID, hostname: want.Name}, row) || row.Info.Serial != serials[0] || row.Info.State != "Unclaimed" || row.Info.OnboardingState != "Not Contacted" || row.Info.SiteID != "" || row.Info.Location.SiteID != "" || row.Info.WorkflowID != "" || row.Workflow != nil || row.SystemWorkflow != nil || row.ResetWorkflow != nil {
			t.Fatal("record is no longer the exact unclaimed fixture; cleanup refused")
		}
		current, err := c.configurationDevices(ctx)
		if err != nil || !reflect.DeepEqual(network, current) {
			t.Fatal("network baseline changed; cleanup refused", err)
		}
	}
	trace.deleteID = id
	if _, err := c.mutateQueryStatus(ctx, http.MethodDelete, pnpDevicesPath+"/"+id, nil, nil, http.StatusOK); err != nil {
		t.Fatal("fixture deletion outcome uncertain; do not replay", err)
	}
	for range 2 {
		rows, err := c.pnpDevices(ctx)
		if err != nil || !reflect.DeepEqual(before, rows) {
			t.Fatal("PnP baseline not restored", err)
		}
	}
	current, err := c.configurationDevices(ctx)
	if err != nil || !reflect.DeepEqual(network, current) {
		t.Fatal("network baseline not preserved", err)
	}
}

// Recover only the known retained registration fixture. This has no create path.
func TestLiveRetainedPnPRegistrationCleanup(t *testing.T) {
	dir := os.Getenv("CATC_PNP_REGISTRATION_RETAINED")
	if dir == "" {
		t.Skip("no private retained registration evidence supplied")
	}
	raw, err := os.ReadFile(filepath.Join(dir, "intent.json"))
	if err != nil {
		t.Fatal(err)
	}
	wants, err := parsePnPInventory(raw)
	if err != nil || len(wants) != 1 {
		t.Fatal("invalid retained intent", err)
	}
	journal, err := os.ReadFile(filepath.Join(dir, "journal.json"))
	if err != nil {
		t.Fatal(err)
	}
	var state cvk.NetworkControllerConfigurationStatus
	if json.Unmarshal(journal, &state) != nil || state.Operation != nil || len(state.ActionApplications) != 1 {
		t.Fatal("retained registration ownership is incomplete")
	}
	receipt := state.ActionApplications[0].Operation.DeepCopy()
	if !pnpRegistrationReceiptPresent(&state, receipt) {
		t.Fatal("invalid retained registration receipt")
	}
	body, err := pnpRegistrationPayload(wants[0])
	if err != nil || receipt.Resource != fmt.Sprintf("pnp-register:%x", sha256.Sum256(body)) {
		t.Fatal("retained manifest binding differs", err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
	defer cancel()
	c := liveClient(t)
	network, err := c.configurationDevices(ctx)
	if err != nil {
		t.Fatal(err)
	}
	rest, _, err := c.session(ctx)
	if err != nil {
		t.Fatal(err)
	}
	base := rest.HTTPClient.Transport
	if base == nil {
		base = http.DefaultTransport
	}
	trace := &livePnPRegistrationTrace{base: base}
	rest.HTTPClient.Transport = trace
	if os.Getenv("CATC_PNP_REMOVAL_PRIMITIVE") == "verified-uncontacted-record" {
		authority := func(ctx context.Context) error {
			current, err := os.ReadFile(filepath.Join(dir, "journal.json"))
			if err != nil {
				return err
			}
			currentIntent, err := os.ReadFile(filepath.Join(dir, "intent.json"))
			if err != nil {
				return err
			}
			if !bytes.Equal(journal, current) || !bytes.Equal(raw, currentIntent) {
				return errConfigurationStale
			}
			devices, err := c.configurationDevices(ctx)
			if err != nil {
				return err
			}
			if !reflect.DeepEqual(network, devices) {
				return errConfigurationStale
			}
			return nil
		}
		plan, err := c.planPnPRegistrationRemoval(ctx, wants[0], receipt, authority)
		if err != nil {
			t.Fatal("retained guarded preparation", err)
		}
		trace.deleteID = receipt.CreatedResourceID
		if err := plan.submit(ctx); err != nil {
			t.Fatal("retained guarded acknowledgement uncertain; no replay", err, trace.deleteAckShape)
		}
		if done, err := plan.observeAbsent(ctx); err != nil || !done {
			t.Fatal("retained guarded absence", done, err)
		}
		rows, err := c.pnpDevices(ctx)
		if err != nil || len(rows) != 0 {
			t.Fatal("retained PnP baseline not restored", err)
		}
	} else {
		cleanupLivePnPRegistrationFixture(t, ctx, c, trace, wants[0], receipt.CreatedResourceID, network, nil)
	}
	t.Log("retained exact uncontacted UUID removed and empty PnP baseline verified; no registration replay or physical operation", receipt.CreatedResourceID)
}
