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
	cvk "github.com/cisco/virtual-kubelet-cisco/api/v1alpha1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
)

var pnpRemovalCohortModes = []string{"cycle", "late-blocked-second", "final-blocked-second", "managed-second", "blocked-second", "legacy-second", "duplicate-serial", "capacity-count", "capacity-bytes", "retained-second", "bad-tombstone"}

func TestPnPRemovalCohort(t *testing.T) {
	for _, mode := range pnpRemovalCohortModes {
		t.Run(mode, func(t *testing.T) { exercisePnPRemovalCohort(t, mode, nil) })
	}
}

func exercisePnPRemovalCohort(t *testing.T, mode string, install func(*adapter, *configapi.NetworkControllerConfig), inspect ...func(*adapter, *configapi.NetworkControllerConfig, *configSourceSnapshot, *configurationWrite)) {
	t.Helper()
	ctx := context.Background()
	a, c := journalFixture(t)
	c.Spec.ManagedSections = []string{"inventory"}
	c.Spec.PrunePolicy = configapi.NetworkControllerRetentionPolicyDelete
	c.Spec.Source.Inline = &runtime.RawExtension{Raw: []byte(`{"catalyst_center":{"inventory":{"devices":[]}}}`)}
	c.Spec.Source.ConfigMapRef = nil
	wants := []pnpDeviceIntent{
		{Name: "edge1", State: "PNP", Serial: "FOC12345678", PID: "C9300-48P", Site: "Global/Lab/Building"},
		{Name: "edge2", State: "PNP", Serial: "FOC87654321", PID: "C9300-48P", Site: "Global/Lab/Building"},
	}
	if mode == "retained-second" {
		raw, err := json.Marshal(map[string]any{"catalyst_center": map[string]any{"inventory": map[string]any{"devices": wants[1:]}}})
		if err != nil {
			t.Fatal(err)
		}
		c.Spec.Source.Inline.Raw = raw
	}
	if err := a.statusWriter.Update(ctx, c); err != nil {
		t.Fatal(err)
	}
	rows := map[string]map[string]any{}
	order := []string{"aaa-id", "zzz-id"}
	removed := map[string]bool{}
	deletes := []string{}
	injected := false
	activeReads := 0
	for i, id := range order {
		row := cleanPnPRemovalFixture(id, wants[i].Serial)
		delete(row, "workflow")
		info := row["deviceInfo"].(map[string]any)
		info["hostname"] = wants[i].Name
		info["pid"] = wants[i].PID
		info["onbState"] = "Not Contacted"
		rows[id] = row
	}
	a.client = testClient(t, func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == authPath {
			fmt.Fprint(w, `{"Token":"test"}`)
			return
		}
		if r.Method == http.MethodGet && r.URL.Path == devicesPath {
			if mode == "managed-second" {
				json.NewEncoder(w).Encode(map[string]any{"response": []Device{{ID: "managed", Serial: wants[1].Serial}}})
			} else {
				fmt.Fprint(w, `{"response":[]}`)
			}
			return
		}
		if r.Method == http.MethodGet && r.URL.Path == pnpDevicesPath {
			if (mode == "late-blocked-second" || mode == "final-blocked-second") && !injected {
				nc, err := a.currentController(ctx)
				if err != nil {
					t.Error(err)
					w.WriteHeader(503)
					return
				}
				if nc.Status.Configuration.Operation != nil && nc.Status.Configuration.Operation.Phase == "Submitting" {
					activeReads++
					if mode != "final-blocked-second" || activeReads == 5 {
						rows["zzz-id"]["dayZeroConfig"] = map[string]any{"config": "private-content"}
						injected = true
					}
				}
			}
			values := []any{}
			for _, id := range order {
				if !removed[id] {
					values = append(values, rows[id])
				}
			}
			json.NewEncoder(w).Encode(values)
			return
		}
		if r.Method == http.MethodDelete {
			id := strings.TrimPrefix(r.URL.Path, pnpDevicesPath+"/")
			if rows[id] != nil && !removed[id] {
				removed[id] = true
				deletes = append(deletes, id)
				if mode == "metadata-lost" {
					fmt.Fprint(w, `{`)
					return
				}
				json.NewEncoder(w).Encode(rows[id])
				return
			}
		}
		t.Error("unexpected native request", r.Method, r.URL.Path)
		w.WriteHeader(403)
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
	nc, err = a.currentController(ctx)
	if err != nil {
		t.Fatal(err)
	}
	at := metav1.NewTime(time.Now().Add(-3 * time.Second).UTC().Truncate(time.Second))
	verified := metav1.NewTime(at.Add(time.Second))
	nc.Status.Configuration = &cvk.NetworkControllerConfigurationStatus{Owners: []cvk.NetworkControllerSectionOwner{{Section: "inventory", ConfigName: c.Name, ConfigUID: string(c.UID)}}}
	for i, id := range order {
		body, err := pnpRegistrationPayload(wants[i])
		if err != nil {
			t.Fatal(err)
		}
		serial, canonical, err := pnpCanonicalRegistrationDigest(wants[i])
		if err != nil {
			t.Fatal(err)
		}
		raw, _ := json.Marshal(rows[id])
		var row pnpRemovalDevice
		if err := json.Unmarshal(raw, &row); err != nil {
			t.Fatal(err)
		}
		// Native catalog normalization is part of the existing baseline contract.
		if _, err := validatePnPInventory([]pnpDevice{row.Device}); err != nil {
			t.Fatal(err)
		}
		b := pnpRemovalBaseline{ID: id, Serial: serial, CanonicalDigest: canonical, NativeDigest: featureIdentityDigest(row)}
		if mode == "duplicate-serial" && i == 1 {
			b.Serial = "foc12345678"
		}
		result, err := b.resource()
		if err != nil {
			t.Fatal(err)
		}
		if mode == "legacy-second" && i == 1 {
			result = pnpRecordPrefix + id
		}
		op := cvk.NetworkControllerConfigurationOperation{ID: strings.Repeat(fmt.Sprint(i+1), 32), ConfigName: c.Name, ConfigUID: string(c.UID), ConfigGeneration: c.Generation, ControllerGeneration: nc.Generation, Section: "inventory", Resource: fmt.Sprintf("pnp-register:%x", sha256.Sum256(body)), Action: "Create", Phase: "Verified", CreatedResourceID: id, SubmittedAt: &at}
		nc.Status.Configuration.ActionApplications = append(nc.Status.Configuration.ActionApplications, cvk.NetworkControllerConfigurationActionApplication{Section: "inventory", Resource: result, Operation: op, VerifiedAt: &verified})
	}
	switch mode {
	case "capacity-count":
		nc.Status.Configuration.ActionApplications = append(nc.Status.Configuration.ActionApplications, pnpCapacityHistory(253, false)...)
	case "capacity-bytes":
		nc.Status.Configuration.ActionApplications = append(nc.Status.Configuration.ActionApplications, pnpCapacityHistory(2, true)...)
	case "bad-tombstone":
		nc.Status.Configuration.ActionApplications = append(nc.Status.Configuration.ActionApplications, cvk.NetworkControllerConfigurationActionApplication{Section: "inventory", Resource: pnpRemovedPrefix + "foreign", Operation: cvk.NetworkControllerConfigurationOperation{ID: strings.Repeat("f", 32), Section: "inventory", Resource: pnpRemovalJournalPrefix + "aaa-id:" + strings.Repeat("a", 64), Action: "Delete", Phase: "Verified", TargetID: "aaa-id", ConfigName: c.Name, ConfigUID: string(c.UID), ConfigGeneration: c.Generation, ControllerGeneration: nc.Generation, SubmittedAt: &at}, VerifiedAt: &verified})
	case "blocked-second":
		rows["zzz-id"]["dayZeroConfig"] = map[string]any{"config": "private-content"}
	}
	if err := a.statusWriter.Status().Update(ctx, nc); err != nil {
		t.Fatal(err)
	}
	source, err := loadControllerSource(ctx, a.statusReader, c)
	if err != nil {
		t.Fatal(err)
	}
	before := journalState(t, a).DeepCopy()
	if _, err := a.planOwnedPnPRemoval(ctx, c, source, "zzz-id"); err == nil {
		t.Fatal("later/retained target bypassed deterministic whole-cohort order")
	}
	write, err := a.planOwnedPnPRemoval(ctx, c, source, "aaa-id")
	good := mode == "metadata-lost" || mode == "cycle" || mode == "retained-second" || mode == "late-blocked-second" || mode == "final-blocked-second"
	if !good {
		if err == nil || len(deletes) != 0 || !reflect.DeepEqual(before, journalState(t, a)) {
			t.Fatal("invalid cohort admitted or state changed", err, deletes)
		}
		return
	}
	if err != nil {
		t.Fatal("cohort plan", err)
	}
	if len(inspect) > 0 {
		inspect[0](a, c, source, write)
		return
	}
	err = a.submitConfigurationWrite(ctx, c, source, "inventory", write)
	if mode == "late-blocked-second" || mode == "final-blocked-second" {
		if !injected || !errors.Is(err, errConfigurationNotDispatched) || len(deletes) != 0 || journalState(t, a).Operation != nil {
			t.Fatal("late sibling conflict did not prevent first Delete and retire exact no-dispatch", err, deletes)
		}
		if !reflect.DeepEqual(before.ActionApplications, journalState(t, a).ActionApplications) {
			t.Fatal("no-dispatch rewrote creators")
		}
		if mode == "final-blocked-second" && activeReads != 5 {
			t.Fatal("final native bracket was not exercised", activeReads)
		}
		delete(rows["zzz-id"], "dayZeroConfig")
		write, err = a.planOwnedPnPRemoval(ctx, c, source, "aaa-id")
		if err != nil {
			t.Fatal(err)
		}
		err = a.submitConfigurationWrite(ctx, c, source, "inventory", write)
	}
	if err != nil {
		t.Fatal(err)
	}
	for i, id := range order {
		if i == 1 && mode == "retained-second" {
			break
		}
		if i > 0 {
			// Reconstruct through a fresh adapter after the prior receipt, never reuse
			// the prior write closure as a batch grant.
			restarted := *a
			a = &restarted
			write, err = a.planOwnedPnPRemoval(ctx, c, source, id)
			if err != nil {
				t.Fatal(err)
			}
			if err := a.submitConfigurationWrite(ctx, c, source, "inventory", write); err != nil {
				t.Fatal(err)
			}
		}
		state := journalState(t, a)
		op := state.Operation
		if op == nil || op.Phase != "Verifying" || op.TargetID != id {
			t.Fatal("missing exact acknowledged operation")
		}
		if err := a.recordOwnedPnPRemoval(ctx, c, source, op.ID); err != nil {
			t.Fatal(err)
		}
		if err := a.finishConfiguration(ctx, op.ID); err != nil {
			t.Fatal(err)
		}
	}
	expected := order
	if mode == "retained-second" {
		expected = order[:1]
	}
	if !reflect.DeepEqual(expected, deletes) {
		t.Fatal("wrong native Deletes", deletes)
	}
	state := journalState(t, a)
	if state.Operation != nil || !reflect.DeepEqual(before.ActionApplications, state.ActionApplications[:2]) || len(state.ActionApplications) != 2+len(expected) {
		t.Fatal("lost creator/tombstone/journal evidence")
	}
	nc, err = a.currentController(ctx)
	if err != nil {
		t.Fatal(err)
	}
	pending, err := ownedPnPRemovalCohort(nc, c, source)
	if err != nil || len(pending) != 0 {
		t.Fatal("settled removals reselected", err)
	}
	if _, err := a.planOwnedPnPRemoval(ctx, c, source, "aaa-id"); err == nil {
		t.Fatal("completed Delete replay planned")
	}
}
