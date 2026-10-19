// Copyright 2026 Cisco Systems Inc.
// SPDX-License-Identifier: Apache-2.0
package catalystcenter

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"reflect"
	"strings"
	"testing"
	"time"

	configapi "github.com/cisco/virtual-kubelet-cisco/api/config/v1alpha1"
	cvk "github.com/cisco/virtual-kubelet-cisco/api/v1alpha1"
	ctrl "sigs.k8s.io/controller-runtime"
	ctrlclient "sigs.k8s.io/controller-runtime/pkg/client"
)

var profileSiteRecoveryModes = []string{"complete", "no-dispatch", "lost-ack", "child-failed", "child-pending", "tree-missing", "foreign-root", "removed", "site-replaced", "profile-replaced", "mid-tree-site", "source", "owner", "controller", "accepted-partial", "pending-peer", "slash-name"}

func exerciseProfileSiteRecovery(t *testing.T, wireless bool, mode string, install func(*adapter, *configapi.NetworkControllerConfig), receipts ...*profileSiteReceiptQualification) {
	t.Helper()
	ctx := context.Background()
	a, config := journalFixture(t)
	kind, namespace, profilePath := "switching", "sites/", networkProfilesPath
	if wireless {
		kind, namespace, profilePath = "wireless", "wireless-sites/", wirelessProfilesPath
	}
	name := "P"
	if mode == "slash-name" {
		name = "P/Interior"
	}
	model := func(paths []string) []byte {
		profile := map[string]any{"name": name}
		if paths != nil {
			profile["sites"] = paths
		}
		rows := []any{profile}
		if mode == "pending-peer" {
			rows = append(rows, map[string]any{"name": "Q", "sites": []string{"Global/B"}})
		}
		raw, err := json.Marshal(map[string]any{"catalyst_center": map[string]any{"network_profiles": map[string]any{kind: rows}}})
		if err != nil {
			t.Fatal(err)
		}
		return raw
	}
	config.Spec.ManagedSections = []string{"network_profiles"}
	config.Spec.Source.Inline.Raw = model([]string{"Global/A"})
	if err := a.statusWriter.Update(ctx, config); err != nil {
		t.Fatal(err)
	}
	var receipt *profileSiteReceiptQualification
	if len(receipts) > 0 {
		receipt = receipts[0]
	}
	if install != nil {
		install(a, config)
	}
	if receipt != nil {
		receipt.prepare(t, a)
	}
	profileID, siteID := "profile-id", "site-a"
	nativePath := "Global/A"
	assigned := map[string]bool{"foreign-site": true}
	if receipt != nil && receipt.mode == "import" {
		assigned["site-a"] = true
	}
	writes, treeReads := 0, 0
	stamp := int64(0)
	taskStamps := map[string]int64{}
	injected, interrupted := false, false
	var interruptedState *cvk.NetworkControllerConfigurationStatus
	var beforeRefusal *cvk.NetworkControllerConfigurationStatus
	refused := false
	a.client = testClient(t, func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == authPath {
			fmt.Fprint(w, `{"Token":"test"}`)
			return
		}
		if mode == "no-dispatch" && !injected && r.Method == http.MethodGet && r.URL.Path == profilePath {
			nc, err := a.currentController(ctx)
			if err != nil {
				t.Error(err)
				w.WriteHeader(500)
				return
			}
			if nc.Status.Configuration != nil && nc.Status.Configuration.Operation != nil && nc.Status.Configuration.Operation.Phase == "Submitting" {
				if !noDispatchOperationSupported(nc.Status.Configuration.Operation) {
					t.Error("association lacks certified refusal scope")
				}
				injected = true
				beforeRefusal = journalState(t, a).DeepCopy()
				w.WriteHeader(503)
				return
			}
		}
		if receipt != nil && receipt.prune != nil {
			handled := receipt.prune.handle(t, w, r, assigned, &writes, taskStamps)
			if handled {
				return
			}
		}
		switch {
		case r.URL.Path == profilePath:
			if wireless {
				rows := []map[string]any{{"id": profileID, "wirelessProfileName": name}}
				if mode == "pending-peer" {
					rows = append(rows, map[string]any{"id": "peer-id", "wirelessProfileName": "Q"})
				}
				json.NewEncoder(w).Encode(map[string]any{"response": rows})
			} else {
				rows := []networkProfile{{ID: profileID, Name: name, Namespace: "switching", Attributes: []networkProfileAttribute{}}}
				if mode == "pending-peer" {
					rows = append(rows, networkProfile{ID: "peer-id", Name: "Q", Namespace: "switching", Attributes: []networkProfileAttribute{}})
				}
				json.NewEncoder(w).Encode(map[string]any{"response": rows})
			}
		case !wireless && (r.URL.Path == profilePath+"/"+profileID || r.URL.Path == profilePath+"/peer-id"):
			row := networkProfile{ID: profileID, Name: name, Namespace: "switching", Attributes: []networkProfileAttribute{}}
			if strings.HasSuffix(r.URL.Path, "/peer-id") {
				row.ID, row.Name = "peer-id", "Q"
			}
			json.NewEncoder(w).Encode(map[string]any{"response": row})
		case wireless && r.URL.Path == wirelessSSIDPath("root"):
			fmt.Fprint(w, `{"response":[]}`)
		case r.URL.Path == sitesPath:
			rows := []Site{{ID: "root", Name: "Global", NameHierarchy: "Global", Type: "global"}, {ID: siteID, Name: lastSiteName(nativePath), NameHierarchy: nativePath, ParentID: "root", Type: "area"}}
			if receipt != nil && receipt.prune != nil {
				rows = append(rows, Site{ID: "child-site", Name: "C", NameHierarchy: nativePath + "/C", ParentID: siteID, Type: "area"})
			}
			if nativePath != "Global/B" && (mode != "pending-peer" || !interrupted) {
				rows = append(rows, Site{ID: "site-b", Name: "B", NameHierarchy: "Global/B", ParentID: "root", Type: "area"})
			}
			json.NewEncoder(w).Encode(map[string]any{"response": rows})
		case r.URL.Path == profileSiteAssignmentsPath("peer-id"):
			if interrupted {
				t.Error("retained association read an unrelated pending pair")
			}
			fmt.Fprint(w, `{"response":[]}`)
		case r.URL.Path == profileSiteAssignmentsPath(profileID):
			if r.Method == http.MethodPost {
				var body map[string]string
				if json.NewDecoder(r.Body).Decode(&body) != nil || len(body) != 1 || body["id"] != "site-a" {
					t.Error("association body escaped selected pair")
				}
				writes++
				assigned[body["id"]] = true
				stamp = time.Now().UnixMilli()
				taskStamps[fmt.Sprintf("task-%d", writes)] = stamp
				if mode == "lost-ack" {
					w.WriteHeader(503)
					return
				}
				fmt.Fprintf(w, `{"response":{"taskId":"task-%d"}}`, writes)
				return
			}
			rows := []map[string]string{}
			for id := range assigned {
				rows = append(rows, map[string]string{"id": id})
			}
			json.NewEncoder(w).Encode(map[string]any{"response": rows})
		case strings.HasPrefix(r.URL.Path, taskPath):
			id := strings.TrimSuffix(strings.TrimPrefix(r.URL.Path, taskPath), "/tree")
			taskStamp, known := taskStamps[id]
			if !known {
				t.Error("unknown task queried")
				w.WriteHeader(404)
				return
			}
			root := map[string]any{"id": id, "isError": false, "startTime": taskStamp, "endTime": taskStamp}
			children := []map[string]any{}
			if interrupted {
				if mode == "foreign-root" {
					root["id"] = "foreign"
				}
				if strings.HasSuffix(r.URL.Path, "/tree") {
					treeReads++
					if mode == "tree-missing" {
						w.WriteHeader(404)
						return
					}
					if mode == "child-failed" || mode == "child-pending" {
						child := map[string]any{"id": "child", "rootId": id, "parentId": id, "isError": mode == "child-failed", "startTime": taskStamp, "endTime": taskStamp}
						if mode == "child-pending" {
							child["endTime"] = 0
						}
						children = append(children, child)
					}
				}
			}
			writeDefinitionTaskFixture(w, r, root, children...)
			if interrupted && mode == "mid-tree-site" && strings.HasSuffix(r.URL.Path, "/tree") {
				siteID = "replacement-site"
			}
		default:
			t.Errorf("unexpected %s %s", r.Method, r.URL.Path)
			w.WriteHeader(404)
		}
	})
	req := ctrl.Request{NamespacedName: ctrlclient.ObjectKeyFromObject(config)}
	tick := func() {
		t.Helper()
		restart := *a
		if _, err := (&configReconciler{a: &restart}).Reconcile(ctx, req); err != nil {
			t.Fatal(err)
		}
		if err := a.statusReader.Get(ctx, req.NamespacedName, config); err != nil {
			t.Fatal(err)
		}
	}
	positive := mode == "complete" || mode == "no-dispatch" || mode == "pending-peer" || mode == "slash-name"
	for range 45 {
		tick()
		state := journalState(t, a)
		if receipt != nil {
			if receipt.observe(t, a, config, tick, assigned, writes, namespace, name, model, &nativePath, &profileID, &siteID) {
				return
			}
			continue
		}
		if injected && !refused {
			for _, condition := range config.Status.Conditions {
				if condition.Reason == "NativePreflightRefused" {
					if state.Operation != nil || writes != 0 || !reflect.DeepEqual(state.Owners, beforeRefusal.Owners) || !reflect.DeepEqual(state.ActionApplications, beforeRefusal.ActionApplications) || !reflect.DeepEqual(state.CredentialApplications, beforeRefusal.CredentialApplications) {
						t.Fatal("association refusal lost custody or caused an effect")
					}
					refused = true
					break
				}
			}
		}
		if op := state.Operation; op != nil && (op.Phase == "Accepted" || mode == "lost-ack" && op.Phase == "Unknown") && !interrupted {
			if !strings.HasPrefix(op.Resource, namespace) || !profileSiteResourceMatches(strings.TrimPrefix(op.Resource, namespace), tagMembershipIdentity("Global/A", name)) || op.ParentID != "profile-id" || op.TargetID != "site-a" {
				t.Fatal("persisted association identity changed")
			}
			if mode != "lost-ack" && mode != "accepted-partial" {
				if err := a.advanceConfiguration(ctx, op.ID, "Unknown", ""); err != nil {
					t.Fatal(err)
				}
			}
			interrupted = true
			switch mode {
			case "removed", "accepted-partial":
				delete(assigned, "site-a")
			case "site-replaced":
				siteID = "replacement-site"
			case "profile-replaced":
				profileID = "replacement-profile"
			case "source":
				config.Generation++
				config.Spec.Source.Inline.Raw = model([]string{"Global/B"})
				if err := a.statusWriter.Update(ctx, config); err != nil {
					t.Fatal(err)
				}
			case "owner":
				nc, _ := a.currentController(ctx)
				nc.Status.Configuration.Owners[0].ConfigUID = "different-owner"
				if err := a.statusWriter.Status().Update(ctx, nc); err != nil {
					t.Fatal(err)
				}
			case "controller":
				nc, _ := a.currentController(ctx)
				nc.Spec.PreferredAPIVersion = "qualification-generation-change"
				nc.Generation++
				if err := a.statusWriter.Update(ctx, nc); err != nil {
					t.Fatal(err)
				}
			}
			interruptedState = journalState(t, a).DeepCopy()
		}
		if interrupted && !positive {
			if mode == "accepted-partial" && state.Operation != nil && state.Operation.Phase != "Verifying" {
				continue
			}
			for range 5 {
				tick()
			}
			current := journalState(t, a)
			if writes != 1 || current.Operation == nil || config.Status.Phase == configapi.NetworkControllerConfigPhaseInSync {
				t.Fatal("uncertain association replayed or settled")
			}
			if mode == "accepted-partial" {
				if current.Operation.Phase != "Verifying" {
					t.Fatal("ordinary partial readback left verification")
				}
			} else if !reflect.DeepEqual(current.Operation, interruptedState.Operation) {
				t.Fatal("retained original association evidence changed")
			}
			if (current.Operation.TaskID == "") != (mode == "lost-ack") {
				t.Fatal("task identity invented or lost")
			}
			if !assigned["foreign-site"] {
				t.Fatal("foreign association removed")
			}
			return
		}
		if interrupted && state.Operation == nil {
			if writes != 1 || treeReads < 1 || !assigned["site-a"] || !assigned["foreign-site"] || mode == "no-dispatch" && !refused {
				t.Fatal("original association task did not recover exactly once")
			}
			if mode != "pending-peer" {
				for range 3 {
					tick()
				}
				if config.Status.Phase != configapi.NetworkControllerConfigPhaseInSync || writes != 1 {
					t.Fatal("association no-op did not converge")
				}
			}
			return
		}
	}
	t.Fatal("association lifecycle did not reach its expected boundary", config.Status.Conditions)
}

func TestProfileSitePublicRecovery(t *testing.T) {
	for _, wireless := range []bool{false, true} {
		for _, mode := range profileSiteRecoveryModes {
			t.Run(fmt.Sprintf("wireless-%t/%s", wireless, mode), func(t *testing.T) { exerciseProfileSiteRecovery(t, wireless, mode, nil) })
		}
	}
}
