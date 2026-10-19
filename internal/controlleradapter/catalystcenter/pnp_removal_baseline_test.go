// Copyright 2026 Cisco Systems Inc.
// SPDX-License-Identifier: Apache-2.0
package catalystcenter

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"reflect"
	"strings"
	"testing"
	"time"

	configapi "github.com/cisco/virtual-kubelet-cisco/api/config/v1alpha1"
	ops "github.com/cisco/virtual-kubelet-cisco/api/ops/v1alpha1"
	cvk "github.com/cisco/virtual-kubelet-cisco/api/v1alpha1"
	core "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	ctrl "sigs.k8s.io/controller-runtime"
	ctrlclient "sigs.k8s.io/controller-runtime/pkg/client"
)

var pnpBaselineModes = []string{"inline", "journal-cycle", "journal-refuse-read", "journal-refuse-handoff", "journal-refuse-handoff-worker", "journal-refuse-handoff-operation", "journal-refuse-content", "journal-refuse-owner", "journal-refuse-source", "journal-public", "journal-public-lost", "journal-public-source-change", "journal-public-missing-owner", "journal-prepared-uncertain", "journal-lost", "journal-missing-receipt", "journal-missing-creator", "journal-missing-owner", "journal-native-drift", "journal-managed-after", "journal-capacity", "configmap", "lost-ack", "conflict", "late-source", "late-configmap", "foreign-owner", "missing-creator", "active-operation", "contacted", "native-drift", "changed-baseline", "capacity-edge", "capacity-bytes", "source-gone", "report", "delete"}

func TestPnPRemovalBaseline(t *testing.T) {
	for _, mode := range pnpBaselineModes {
		t.Run(mode, func(t *testing.T) { exercisePnPRemovalBaseline(t, mode, nil) })
	}
}

type pnpBaselineFaultClient struct {
	ctrlclient.Client
	mode     string
	updates  int
	injected bool
}

func (c *pnpBaselineFaultClient) Status() ctrlclient.SubResourceWriter {
	return pnpBaselineFaultWriter{SubResourceWriter: c.Client.Status(), owner: c}
}

type pnpBaselineFaultWriter struct {
	ctrlclient.SubResourceWriter
	owner *pnpBaselineFaultClient
}

func (w pnpBaselineFaultWriter) Update(ctx context.Context, obj ctrlclient.Object, opts ...ctrlclient.SubResourceUpdateOption) error {
	n, ok := obj.(*cvk.NetworkController)
	if ok && n.Status.Configuration != nil {
		baseline := false
		for _, r := range n.Status.Configuration.ActionApplications {
			if strings.Contains(r.Resource, ":removal:v1:") {
				baseline = true
			}
		}
		if baseline {
			w.owner.updates++
			if !w.owner.injected && (w.owner.mode == "lost-ack" || w.owner.mode == "conflict") {
				w.owner.injected = true
				if w.owner.mode == "conflict" {
					var current cvk.NetworkController
					if err := w.owner.Client.Get(ctx, ctrlclient.ObjectKeyFromObject(n), &current); err != nil {
						return err
					}
					meta.SetStatusCondition(&current.Status.Conditions, condition("BaselineFixtureConcurrent", true, "Updated", "Unrelated status survives retry", current.Generation))
					if err := w.SubResourceWriter.Update(ctx, &current, opts...); err != nil {
						return err
					}
					return w.SubResourceWriter.Update(ctx, obj, opts...)
				}
				if err := w.SubResourceWriter.Update(ctx, obj, opts...); err != nil {
					return err
				}
				return errors.New("baseline status acknowledgement lost")
			}
		}
	}
	return w.SubResourceWriter.Update(ctx, obj, opts...)
}

func exercisePnPRemovalBaseline(t *testing.T, mode string, install func(*adapter, *configapi.NetworkControllerConfig)) {
	t.Helper()
	ctx := context.Background()
	a, c := journalFixture(t)
	if strings.HasPrefix(mode, "journal-refuse-handoff") {
		a.workerPodName = "worker"
		a.workerPodUID = "worker-uid"
	}
	wants, err := parsePnPInventory([]byte(pnpIntentFixture()))
	if err != nil {
		t.Fatal(err)
	}
	want := wants[0]
	c.Spec.ManagedSections = []string{"inventory"}
	c.Spec.Source.Inline = &runtime.RawExtension{Raw: []byte(pnpIntentFixture())}
	if err := a.statusWriter.Update(ctx, c); err != nil {
		t.Fatal(err)
	}
	var admin ctrlclient.Client
	var source *configSourceSnapshot
	var cm *core.ConfigMap
	reads, writes := 0, 0
	removed := false
	refused := false
	row := cleanPnPRemovalFixture("created-id", want.Serial)
	delete(row, "workflow")
	info := row["deviceInfo"].(map[string]any)
	info["hostname"] = want.Name
	info["onbState"] = "Not Contacted"
	if mode == "contacted" {
		info["firstContact"] = 123.0
	}
	a.client = testClient(t, func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == authPath {
			fmt.Fprint(w, `{"Token":"test"}`)
			return
		}
		if r.Method == "DELETE" && r.URL.Path == pnpDevicesPath+"/created-id" && strings.HasPrefix(mode, "journal-") {
			writes++
			removed = true
			if mode == "journal-lost" || mode == "journal-public-lost" {
				fmt.Fprint(w, `{`)
			} else {
				json.NewEncoder(w).Encode(row)
			}
			return
		}
		if r.Method != "GET" {
			writes++
			t.Error("baseline attempted native mutation", r.Method, r.URL.Path)
			w.WriteHeader(403)
			return
		}
		if r.URL.Path == devicesPath {
			if mode == "journal-managed-after" && removed {
				json.NewEncoder(w).Encode(map[string]any{"response": []Device{{ID: "managed", Serial: want.Serial}}})
				return
			}
			fmt.Fprint(w, `{"response":[]}`)
			return
		}
		if r.URL.Path != pnpDevicesPath {
			t.Error("unknown API", r.URL.Path)
			w.WriteHeader(404)
			return
		}
		reads++
		if strings.HasPrefix(mode, "journal-refuse-") && !refused {
			current, err := a.currentController(ctx)
			if err != nil {
				t.Error(err)
				w.WriteHeader(503)
				return
			}
			if op := current.Status.Configuration.Operation; op != nil && op.Phase == "Submitting" {
				refused = true
				switch mode {

				case "journal-refuse-handoff", "journal-refuse-handoff-worker", "journal-refuse-handoff-operation":
					prepared := current.Status.Configuration.Operation.DeepCopy()
					prepared.Phase = "Prepared"
					prepared.SubmittedAt = nil
					h := &cvk.NetworkControllerConfigurationMetadataHandoff{Protocol: cvk.NetworkControllerMetadataHandoffProtocol, BindingDigest: strings.Repeat("b", 64), LeaseNamespace: "leases", WorkerUsername: "system:serviceaccount:" + current.Namespace + ":worker", WorkerPod: ops.ConfigurationReservationReference{Name: "worker", UID: "worker-uid"}, PhysicalIdentities: []string{"foc12345678"}, Operation: *prepared}
					if mode == "journal-refuse-handoff-worker" {
						h.WorkerPod.UID = "other-worker"
					}
					if mode == "journal-refuse-handoff-operation" {
						h.Operation.ID = strings.Repeat("e", 32)
					}
					current.Status.Configuration.MetadataHandoff = h
					if err := admin.Status().Update(ctx, current); err != nil {
						t.Error(err)
					}
					w.WriteHeader(503)
					return
				case "journal-refuse-read":
					w.WriteHeader(503)
					return
				case "journal-refuse-content":
					row["dayZeroConfig"] = map[string]any{"config": "private-value"}
				case "journal-refuse-owner":
					current.Status.Configuration.Owners[0].ConfigUID = "foreign"
					if err := admin.Status().Update(ctx, current); err != nil {
						t.Error(err)
					}
				case "journal-refuse-source":
					fresh := c.DeepCopy()
					fresh.Spec.ModelSource.Exporter = "changed"
					if install == nil {
						fresh.Generation++
					}
					if err := admin.Update(ctx, fresh); err != nil {
						t.Error(err)
					}
				}
			}
		}
		if reads == 2 && (mode == "late-source" || mode == "late-configmap") {
			if cm != nil {
				cm.Data["model"] += " "
				if err := admin.Update(ctx, cm); err != nil {
					t.Error(err)
				}
			} else {
				fresh := c.DeepCopy()
				fresh.Annotations = map[string]string{"baseline-fixture": "changed"}
				if err := admin.Update(ctx, fresh); err != nil {
					t.Error(err)
				}
			}
		}
		if mode == "native-drift" && reads > 1 {
			info["authStatus"] = "changed"
		}
		if removed {
			fmt.Fprint(w, `[]`)
			return
		}
		json.NewEncoder(w).Encode([]any{row})
	})
	nc, err := a.currentController(ctx)
	if err != nil {
		t.Fatal(err)
	}
	nc.Spec.Endpoint = a.client.cfg.Endpoint
	if err := a.statusWriter.Update(ctx, nc); err != nil {
		t.Fatal(err)
	}
	if install != nil {
		install(a, c)
	}
	admin = a.statusWriter
	if mode == "configmap" || mode == "late-configmap" || mode == "source-gone" {
		cm = &core.ConfigMap{ObjectMeta: metav1.ObjectMeta{Name: "pnp-source", Namespace: c.Namespace}, Data: map[string]string{"model": pnpIntentFixture()}}
		if install == nil {
			cm.UID = "pnp-source"
		}
		if err := admin.Create(ctx, cm); err != nil {
			t.Fatal(err)
		}
		c.Spec.Source = configapi.NetworkControllerConfigurationSource{ConfigMapRef: &configapi.ConfigMapKeyRef{Name: cm.Name, Key: "model"}}
		if err := admin.Update(ctx, c); err != nil {
			t.Fatal(err)
		}
		source, err = loadControllerSource(ctx, a.statusReader, c)
		if err != nil {
			t.Fatal(err)
		}
	}
	if mode == "report" {
		c.Spec.Mode = configapi.NetworkControllerApplyModeReport
	}
	if mode == "delete" {
		c.Spec.DeletionPolicy = configapi.NetworkControllerRetentionPolicyDelete
	}
	if mode == "report" || mode == "delete" {
		if err := admin.Update(ctx, c); err != nil {
			t.Fatal(err)
		}
	}
	nc, err = a.currentController(ctx)
	if err != nil {
		t.Fatal(err)
	}
	at := metav1.NewTime(time.Now().Add(-3 * time.Second).UTC().Truncate(time.Second))
	verified := metav1.NewTime(at.Add(time.Second))
	body, _ := pnpRegistrationPayload(want)
	op := cvk.NetworkControllerConfigurationOperation{ID: strings.Repeat("a", 32), ConfigName: c.Name, ConfigUID: string(c.UID), ConfigGeneration: c.Generation, ControllerGeneration: nc.Generation, Section: "inventory", Resource: fmt.Sprintf("pnp-register:%x", sha256.Sum256(body)), Action: "Create", Phase: "Verified", CreatedResourceID: "created-id", SubmittedAt: &at}
	if source != nil {
		op.SourceUID = string(source.uid)
		op.SourceVersion = source.version
	}
	creator := cvk.NetworkControllerConfigurationActionApplication{Section: "inventory", Resource: "pnp-record:created-id", Operation: op, VerifiedAt: &verified}
	nc.Status.Configuration = &cvk.NetworkControllerConfigurationStatus{Owners: []cvk.NetworkControllerSectionOwner{{Section: "inventory", ConfigName: c.Name, ConfigUID: string(c.UID)}}, ActionApplications: []cvk.NetworkControllerConfigurationActionApplication{creator}}
	if mode == "foreign-owner" {
		nc.Status.Configuration.Owners[0].ConfigUID = "foreign"
	}
	if mode == "missing-creator" {
		nc.Status.Configuration.ActionApplications = nil
	}
	if mode == "active-operation" {
		nc.Status.Configuration.Operation = op.DeepCopy()
	}
	if mode == "capacity-edge" || mode == "journal-capacity" {
		nc.Status.Configuration.ActionApplications = append(nc.Status.Configuration.ActionApplications, pnpCapacityHistory(255, false)...)
	}
	if mode == "capacity-bytes" {
		nc.Status.Configuration.ActionApplications = append(nc.Status.Configuration.ActionApplications, pnpCapacityHistory(2, true)...)
	}
	if err := admin.Status().Update(ctx, nc); err != nil {
		t.Fatal(err)
	}
	before, err := a.currentController(ctx)
	if err != nil {
		t.Fatal(err)
	}
	// Compare to the authoritative stored representation, including API time
	// normalization, rather than the pre-serialization fixture value.
	storedCreator := creator.DeepCopy()
	if len(before.Status.Configuration.ActionApplications) > 0 {
		storedCreator = before.Status.Configuration.ActionApplications[0].DeepCopy()
	}
	if mode == "source-gone" {
		if err := admin.Delete(ctx, cm); err != nil {
			t.Fatal(err)
		}
	}
	fault := &pnpBaselineFaultClient{Client: statusOnlyFeatureClient{Client: admin}, mode: mode}
	a.statusWriter = fault
	changed, err := a.retainOwnedPnPRemovalBaseline(ctx, c, source, want)
	if mode == "lost-ack" || mode == "conflict" {
		if err == nil || changed {
			t.Fatal("fault not surfaced")
		}
		changed, err = a.retainOwnedPnPRemovalBaseline(ctx, c, source, want)
	}
	good := strings.HasPrefix(mode, "journal-") || mode == "inline" || mode == "configmap" || mode == "lost-ack" || mode == "conflict" || mode == "changed-baseline" || mode == "capacity-edge"
	if (err == nil) != good || writes != 0 {
		t.Fatal("invalid baseline result", changed, err, writes)
	}
	current, readErr := a.currentController(ctx)
	if readErr != nil {
		t.Fatal(readErr)
	}
	if !good {
		if !reflect.DeepEqual(before.Status.Configuration, current.Status.Configuration) || fault.updates != 0 {
			t.Fatal("refused baseline changed ownership/receipts")
		}
		return
	}
	if !reflect.DeepEqual(current.Status.Configuration.ActionApplications[0].Operation, storedCreator.Operation) || !reflect.DeepEqual(current.Status.Configuration.ActionApplications[0].VerifiedAt, storedCreator.VerifiedAt) || len(current.Status.Configuration.ActionApplications) != len(before.Status.Configuration.ActionApplications) || !reflect.DeepEqual(current.Status.Configuration.ActionApplications[1:], before.Status.Configuration.ActionApplications[1:]) {
		t.Fatal("baseline rewrote original/unrelated evidence")
	}
	if !pnpRegistrationReceiptPresent(current.Status.Configuration, &storedCreator.Operation) {
		t.Fatal("enriched original receipt no longer recognized")
	}
	b, err := decodePnPRecord(current.Status.Configuration.ActionApplications[0].Resource)
	if err != nil || b.Serial != "foc12345678" || b.NativeDigest == "" {
		t.Fatal("canonical identity missing", b, err)
	}
	saved := current.Status.Configuration.DeepCopy()
	updates := fault.updates
	if mode == "changed-baseline" {
		info["authStatus"] = "changed"
		if _, err := a.retainOwnedPnPRemovalBaseline(ctx, c, source, want); err == nil {
			t.Fatal("immutable baseline overwritten")
		}
	} else {
		if changed, err := a.retainOwnedPnPRemovalBaseline(ctx, c, source, want); changed || err != nil {
			t.Fatal("idempotent baseline failed", changed, err)
		}
	}
	current, err = a.currentController(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if fault.updates != updates || !reflect.DeepEqual(saved, current.Status.Configuration) {
		t.Fatal("baseline replay rewrote status")
	}
	raw, _ := json.Marshal(current.Status)
	if strings.Contains(string(raw), "fixture-secret") || strings.Contains(string(raw), "private-value") {
		t.Fatal("private model/native values persisted")
	}

	if strings.HasPrefix(mode, "journal-") {
		c.Spec.PrunePolicy = configapi.NetworkControllerRetentionPolicyDelete
		c.Spec.Source = configapi.NetworkControllerConfigurationSource{Inline: &runtime.RawExtension{Raw: []byte(`{"catalyst_center":{"inventory":{"devices":[]}}}`)}}
		if install == nil {
			c.Generation++
		}
		if err := admin.Update(ctx, c); err != nil {
			t.Fatal(err)
		}
		source, err = loadControllerSource(ctx, a.statusReader, c)
		if err != nil {
			t.Fatal(err)
		}
		if mode == "journal-native-drift" {
			info["authStatus"] = "changed"
		}
		write, err := a.planOwnedPnPRemoval(ctx, c, source, "created-id")
		if mode == "journal-native-drift" || mode == "journal-capacity" {
			if err == nil || writes != 0 {
				t.Fatal("unsafe/overcapacity journal admitted", err, writes)
			}
			return
		}
		if err != nil {
			t.Fatal("journal planning", err)
		}
		if mode == "journal-prepared-uncertain" {
			prepared, err := a.prepareConfiguration(ctx, c, source, "inventory", write.resource, "Delete", write.identity)
			if err != nil {
				t.Fatal(err)
			}
			current, err := a.currentController(ctx)
			if err != nil {
				t.Fatal(err)
			}
			now := metav1.Now()
			current.Status.Configuration.Operation.SubmittedAt = &now
			if err := admin.Status().Update(ctx, current); err != nil {
				t.Fatal(err)
			}
			if err := a.finishConfiguration(ctx, prepared.ID); err == nil || writes != 0 {
				t.Fatal("uncertain preparation released", err, writes)
			}
			if err := a.startConfiguration(ctx, c, source, prepared.ID, write.identity); err == nil || writes != 0 {
				t.Fatal("uncertain preparation obtained a new permit", err, writes)
			}
			return
		}
		err = a.submitConfigurationWrite(ctx, c, source, "inventory", write)
		current, readErr := a.currentController(ctx)
		if readErr != nil {
			t.Fatal(readErr)
		}
		op := current.Status.Configuration.Operation
		if strings.HasPrefix(mode, "journal-refuse-") {
			if !refused || err == nil || writes != 0 {
				t.Fatal("pre-submit refusal not observed", err, writes)
			}

			if strings.HasPrefix(mode, "journal-refuse-handoff") {
				h := current.Status.Configuration.MetadataHandoff
				if h == nil {
					t.Fatal("handoff fixture not retained")
				}
				if mode != "journal-refuse-handoff" {
					if op == nil || h.RefusedAt != nil || h.RefusedSubmissionAt != nil {
						t.Fatal("mismatched handoff consumed proof/cleared journal")
					}
					return
				}
				if !errors.Is(err, errConfigurationNotDispatched) || op != nil || h.RefusedAt == nil || h.RefusedSubmissionAt == nil || h.Operation.Phase != "Prepared" || h.Operation.SubmittedAt != nil {
					t.Fatal("certified refusal not retained atomically with slot retirement", err)
				}
				if len(current.Status.Configuration.ActionApplications) != len(saved.ActionApplications) || !reflect.DeepEqual(current.Status.Configuration.ActionApplications, saved.ActionApplications) {
					t.Fatal("refusal fabricated successful effect receipt")
				}
				if _, err := a.planOwnedPnPRemoval(ctx, c, source, "created-id"); err == nil {
					t.Fatal("retained custody allowed new cleanup plan")
				}
				if _, err := a.prepareConfiguration(ctx, c, source, "inventory", "pnp-register:"+strings.Repeat("f", 64), "Create"); err == nil {
					t.Fatal("retained handoff allowed new preparation")
				}
				// Reconstruct from durable API state; no in-memory guard or native
				// absence inference is needed to observe the certified outcome.
				restarted := *a
				fresh, err := restarted.currentController(ctx)
				if err != nil || !reflect.DeepEqual(fresh.Status.Configuration.MetadataHandoff, h) {
					t.Fatal("refusal lost across reconstruction", err)
				}
				if install != nil {
					altered := fresh.DeepCopy()
					altered.Status.Configuration.MetadataHandoff.BindingDigest = strings.Repeat("f", 64)
					if err := admin.Status().Update(ctx, altered); err == nil {
						t.Fatal("API allowed handoff binding rewrite")
					}
					dropped := fresh.DeepCopy()
					dropped.Status.Configuration.MetadataHandoff = nil
					if err := admin.Status().Update(ctx, dropped); err == nil {
						t.Fatal("API discarded unreleased handoff custody")
					}
					erased := fresh.DeepCopy()
					erased.Status.Configuration = nil
					if err := admin.Status().Update(ctx, erased); err == nil {
						t.Fatal("status replacement bypassed metadata custody")
					}

				}
				return
			}
			if mode == "journal-refuse-owner" {
				if op == nil {
					t.Fatal("changed owner lost its journal fence")
				}
				return
			}
			if !errors.Is(err, errConfigurationNotDispatched) || op != nil {
				t.Fatal("proven no-dispatch not retired", err)
			}
			delete(row, "dayZeroConfig")
			if err := admin.Get(ctx, ctrlclient.ObjectKeyFromObject(c), c); err != nil {
				t.Fatal(err)
			}
			source, err = loadControllerSource(ctx, a.statusReader, c)
			if err != nil {
				t.Fatal(err)
			}
			fresh, err := a.planOwnedPnPRemoval(ctx, c, source, "created-id")
			if err != nil {
				t.Fatal("fresh replan", err)
			}
			if err := a.submitConfigurationWrite(ctx, c, source, "inventory", fresh); err != nil {
				t.Fatal("fresh dispatch", err)
			}
			current, err := a.currentController(ctx)
			if err != nil {
				t.Fatal(err)
			}
			op = current.Status.Configuration.Operation
			if op == nil || op.Phase != "Verifying" || writes != 1 {
				t.Fatal("replan dispatched incorrect count")
			}
			if err := a.recordOwnedPnPRemoval(ctx, c, source, op.ID); err != nil {
				t.Fatal(err)
			}
			if err := a.finishConfiguration(ctx, op.ID); err != nil {
				t.Fatal(err)
			}
			return
		}
		if writes != 1 || op == nil {
			t.Fatal("missing one-shot journal", writes)
		}
		if mode == "journal-lost" || mode == "journal-public-lost" {
			if err == nil || op.Phase != "Unknown" {
				t.Fatal("lost acknowledgement escaped fence", err, op.Phase)
			}
			if err := a.recordOwnedPnPRemoval(ctx, c, source, op.ID); err == nil {
				t.Fatal("native absence inferred unknown acknowledgement")
			}
			if err := a.finishConfiguration(ctx, op.ID); err == nil {
				t.Fatal("unknown Delete released")
			}
			if mode == "journal-public-lost" {
				for range 2 {
					if _, err := (&configReconciler{a: a}).Reconcile(ctx, ctrl.Request{NamespacedName: ctrlclient.ObjectKeyFromObject(c)}); err != nil {
						t.Fatal(err)
					}
				}
				current, err := a.currentController(ctx)
				if err != nil {
					t.Fatal(err)
				}
				if current.Status.Configuration.Operation == nil || current.Status.Configuration.Operation.Phase != "Unknown" || writes != 1 {
					t.Fatal("public recovery inferred unknown acknowledgement")
				}
			}
			return
		}
		if err != nil || op.Phase != "Verifying" {
			t.Fatal("synchronous acknowledgement not retained", err, op.Phase)
		}
		if err := a.advanceConfiguration(ctx, op.ID, "Verified", ""); err == nil {
			t.Fatal("generic verifier bypassed absence receipt")
		}
		// Persisted state is sufficient for a fresh adapter; the submit closure is
		// deliberately discarded and cannot be used during recovery.
		data, _ := json.Marshal(current.Status.Configuration)
		restored := current.DeepCopy()
		if err := json.Unmarshal(data, &restored.Status.Configuration); err != nil {
			t.Fatal(err)
		}
		if err := admin.Status().Update(ctx, restored); err != nil {
			t.Fatal(err)
		}
		restarted := *a
		a = &restarted
		if mode == "journal-public-source-change" || mode == "journal-public-missing-owner" {
			if mode == "journal-public-source-change" {
				c.Spec.Source.Inline.Raw = []byte(pnpIntentFixture())
				if install == nil {
					c.Generation++
				}
				if err := admin.Update(ctx, c); err != nil {
					t.Fatal(err)
				}
			} else {
				current, err := a.currentController(ctx)
				if err != nil {
					t.Fatal(err)
				}
				current.Status.Configuration.Owners = nil
				if err := admin.Status().Update(ctx, current); err != nil {
					t.Fatal(err)
				}
			}
			for range 2 {
				if _, err := (&configReconciler{a: a}).Reconcile(ctx, ctrl.Request{NamespacedName: ctrlclient.ObjectKeyFromObject(c)}); err != nil {
					t.Fatal(err)
				}
			}
			current, err := a.currentController(ctx)
			if err != nil {
				t.Fatal(err)
			}
			if current.Status.Configuration.Operation == nil || current.Status.Configuration.Operation.Phase != "Verifying" || len(current.Status.Configuration.ActionApplications) != 1 || writes != 1 {
				t.Fatal("public recovery ignored changed authority")
			}
			return
		}
		if mode == "journal-public" {
			request := ctrl.Request{NamespacedName: ctrlclient.ObjectKeyFromObject(c)}
			for range 3 {
				if _, err := (&configReconciler{a: a}).Reconcile(ctx, request); err != nil {
					t.Fatal(err)
				}
			}
			current, err := a.currentController(ctx)
			if err != nil {
				t.Fatal(err)
			}
			if current.Status.Configuration.Operation != nil || len(current.Status.Configuration.ActionApplications) != 2 || writes != 1 {
				t.Fatal("public recovery did not settle without replay")
			}
			if err := admin.Get(ctx, request.NamespacedName, c); err != nil {
				t.Fatal(err)
			}
			ready := meta.FindStatusCondition(c.Status.Conditions, configapi.NetworkControllerConfigConditionReady)
			if c.Status.Phase == configapi.NetworkControllerConfigPhaseInSync || ready == nil || ready.Status == metav1.ConditionTrue || ready.Reason != "PnPRemovalDispatchUnavailable" {
				t.Fatal("public recovery advertised unsupported convergence", c.Status.Conditions)
			}
			return
		}
		err = a.recordOwnedPnPRemoval(ctx, c, source, op.ID)
		if mode == "journal-managed-after" {
			if err == nil {
				t.Fatal("managed serial accepted as absent")
			}
			return
		}
		if err != nil {
			t.Fatal("restart absence publication", err)
		}
		current, err = a.currentController(ctx)
		if err != nil {
			t.Fatal(err)
		}
		op = current.Status.Configuration.Operation
		if !pnpRemovedReceiptPresent(current.Status.Configuration, op) || len(current.Status.Configuration.ActionApplications) != len(saved.ActionApplications)+1 {
			t.Fatal("atomic Delete receipt missing")
		}
		if mode == "journal-missing-creator" || mode == "journal-missing-owner" {
			if mode == "journal-missing-creator" {
				current.Status.Configuration.ActionApplications = current.Status.Configuration.ActionApplications[1:]
			} else {
				current.Status.Configuration.Owners = nil
			}
			if err := admin.Status().Update(ctx, current); err != nil {
				t.Fatal(err)
			}
			if err := a.finishConfiguration(ctx, op.ID); err == nil {
				t.Fatal("lost creator/owner released journal")
			}
			return
		}
		if mode == "journal-missing-receipt" {
			current.Status.Configuration.ActionApplications = current.Status.Configuration.ActionApplications[:len(current.Status.Configuration.ActionApplications)-1]
			if err := admin.Status().Update(ctx, current); err != nil {
				t.Fatal(err)
			}
			if err := a.finishConfiguration(ctx, op.ID); err == nil {
				t.Fatal("missing Delete receipt released")
			}
			return
		}
		if err := a.finishConfiguration(ctx, op.ID); err != nil {
			t.Fatal("verified Delete release", err)
		}
		if _, err := a.planOwnedPnPRemoval(ctx, c, source, "created-id"); err == nil {
			t.Fatal("removed record replanned")
		}
		if writes != 1 {
			t.Fatal("Delete replayed after recovery")
		}
	}
}

func TestPnPRemovalBaselineCodec(t *testing.T) {
	b := pnpRemovalBaseline{ID: strings.Repeat("i", 128), Serial: strings.Repeat("a", 126) + ":b", CanonicalDigest: strings.Repeat("a", 64), NativeDigest: strings.Repeat("b", 64)}
	resource, err := b.resource()
	if err != nil || len(resource) > 512 {
		t.Fatal("bounded canonical resource", err)
	}
	decoded, err := decodePnPRecord(resource)
	if err != nil || decoded != b {
		t.Fatal("canonical round trip", err)
	}
	legacy, err := decodePnPRecord("pnp-record:" + b.ID)
	if err != nil || legacy.Serial != "" {
		t.Fatal("legacy registration compatibility", err)
	}
	for _, mutated := range []string{resource + ":extra", strings.Replace(resource, ":v1:", ":v2:", 1), strings.Replace(resource, b.CanonicalDigest, strings.Repeat("g", 64), 1), "pnp-record:id:removal:v1:Rk9D:" + b.CanonicalDigest + ":" + b.NativeDigest, "pnp-record:" + strings.Repeat("i", 129), "pnp-record:id:removal:v1:YQ==:" + b.CanonicalDigest + ":" + b.NativeDigest} {
		if _, err := decodePnPRecord(mutated); err == nil {
			t.Fatal("noncanonical/corrupt baseline accepted")
		}
	}
}
