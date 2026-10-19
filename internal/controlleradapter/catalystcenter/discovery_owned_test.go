// Copyright © 2026 Cisco Systems Inc.
// Licensed under the Apache License, Version 2.0 (the "License");
package catalystcenter

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"testing"
	"time"

	cvk "github.com/cisco/virtual-kubelet-cisco/api/v1alpha1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

func TestDiscoveryOwnedSubmissionPreflightAndSingleUse(t *testing.T) {
	for _, mode := range []string{"unchanged", "credential-replaced", "job-appeared", "model-mutated", "ambiguous-result"} {
		t.Run(mode, func(t *testing.T) {
			want, err := parseDiscoveryModels([]any{map[string]any{"name": "fixture", "type": "SINGLE", "ip_address_list": "192.0.2.1", "protocol_order": "SSH", "global_credential_list": []any{"lab-cli"}}})
			if err != nil {
				t.Fatal(err)
			}
			id := "credential-id"
			present := false
			writes := 0
			c := testClient(t, func(w http.ResponseWriter, r *http.Request) {
				switch {
				case r.URL.Path == authPath:
					fmt.Fprint(w, `{"Token":"test"}`)
				case r.URL.Path == globalCredentialsPath:
					json.NewEncoder(w).Encode(map[string]any{"response": map[string]any{"cliCredential": []map[string]any{{"id": id, "description": "lab-cli"}}}})
				case r.Method == "GET" && r.URL.Path == discoveryPath+"/1/100":
					jobs := []discoveryMetadata{}
					if present {
						jobs = append(jobs, discoveryMetadata{ID: "job-id", Name: "fixture", Type: "SINGLE", Addresses: "192.0.2.1", Protocol: "SSH", CredentialIDs: []string{id}, Condition: "Complete", Status: "Inactive"})
					}
					json.NewEncoder(w).Encode(map[string]any{"response": jobs})
				case r.Method == "POST" && r.URL.Path == discoveryPath:
					writes++
					if mode == "ambiguous-result" {
						fmt.Fprint(w, `{"response":{}}`)
					} else {
						fmt.Fprint(w, `{"response":{"taskId":"task"}}`)
					}
				default:
					t.Errorf("unexpected %s %s", r.Method, r.URL.Path)
					w.WriteHeader(500)
				}
			})
			e := &ownedDiscoveryExecutor{base: &discoveryExecutor{client: c, desired: want}, owner: "owner"}
			write, err := e.next(context.Background())
			if err != nil || write == nil {
				t.Fatal("plan", err)
			}
			switch mode {
			case "credential-replaced":
				id = "replaced-id"
			case "job-appeared":
				present = true
			case "model-mutated":
				want[0].fields["retry"] = 3
			}
			_, err = write.submit(context.Background())
			expected := mode == "unchanged" || mode == "ambiguous-result"
			if (err == nil) != (mode == "unchanged") || expected && writes != 1 || !expected && writes != 0 {
				t.Fatal("unexpected submission", err, writes)
			}
			if _, err = write.submit(context.Background()); err == nil {
				t.Fatal("consumed plan reused")
			}
			if expected && writes != 1 || !expected && writes != 0 {
				t.Fatal("native request replayed")
			}
		})
	}
}

func TestDiscoveryOwnedReceiptReleaseAndCapacity(t *testing.T) {
	ctx := context.Background()
	a, config := journalFixture(t)
	config.Spec.ManagedSections = []string{"inventory"}
	config.Spec.Source.Inline.Raw = discoveryModel()
	if err := a.statusWriter.Update(ctx, config); err != nil {
		t.Fatal(err)
	}
	payload := map[string]any{"name": "fixture", "discoveryType": "SINGLE", "ipAddressList": "192.0.2.1", "protocolOrder": "SSH", "globalCredentialIdList": []string{"credential-id"}}
	resource := ownedDiscoveryResource("fixture", payload)
	op, err := a.prepareConfiguration(ctx, config, nil, "inventory", resource, "Create")
	if err != nil {
		t.Fatal(err)
	}
	nc, err := a.currentController(ctx)
	if err != nil {
		t.Fatal(err)
	}
	submitted := metav1.NewTime(time.Now().Add(-time.Second))
	verified := metav1.Now()
	nc.Status.Configuration.Operation.Phase = "Verified"
	nc.Status.Configuration.Operation.TaskID = "task"
	nc.Status.Configuration.Operation.SubmittedAt = &submitted
	if err := a.statusWriter.Status().Update(ctx, nc); err != nil {
		t.Fatal(err)
	}
	if err := a.finishConfiguration(ctx, op.ID); err == nil {
		t.Fatal("marker released without result receipt")
	}
	receipt := cvk.NetworkControllerConfigurationActionApplication{Section: "inventory", Resource: ownedDiscoveryReceiptKey("fixture", "42"), Operation: *nc.Status.Configuration.Operation.DeepCopy(), VerifiedAt: &verified}
	for _, mode := range []string{"missing-task", "missing-native-id", "wrong-native-name", "duplicate", "foreign-owner"} {
		t.Run(mode, func(t *testing.T) {
			state := nc.Status.Configuration.DeepCopy()
			r := *receipt.DeepCopy()
			switch mode {
			case "missing-task":
				r.Operation.TaskID = ""
				state.Operation.TaskID = ""
			case "missing-native-id":
				r.Resource = ownedDiscoveryReceiptKey("fixture", "")
			case "wrong-native-name":
				r.Resource = ownedDiscoveryReceiptKey("other", "42")
			case "foreign-owner":
				r.Operation.ConfigUID = "foreign"
			}
			state.ActionApplications = []cvk.NetworkControllerConfigurationActionApplication{r}
			if mode == "duplicate" {
				state.ActionApplications = append(state.ActionApplications, r)
			}
			if ownedDiscoveryReceiptPresent(state, state.Operation) {
				t.Fatal("pruned/foreign receipt authorized release")
			}
		})
	}
	nc.Status.Configuration.ActionApplications = []cvk.NetworkControllerConfigurationActionApplication{receipt}
	if err := a.statusWriter.Status().Update(ctx, nc); err != nil {
		t.Fatal(err)
	}
	if err := a.finishConfiguration(ctx, op.ID); err != nil {
		t.Fatal("valid result receipt held", err)
	}
	state := &cvk.NetworkControllerConfigurationStatus{}
	for i := 0; i < 256; i++ {
		state.ActionApplications = append(state.ActionApplications, cvk.NetworkControllerConfigurationActionApplication{Section: "templates", Resource: fmt.Sprint(i)})
	}
	if err := ownedDiscoveryCapacity(state, op); err == nil {
		t.Fatal("status capacity not reserved")
	}
	if ownedDiscoveryOperation(&cvk.NetworkControllerConfigurationOperation{Section: "inventory", Resource: ownedDiscoveryPrefix + strings.Repeat("a", 64) + ":bad", Action: "Create"}) {
		t.Fatal("malformed marker accepted")
	}
}

func TestDiscoveryOwnedResultRecoversOriginalCredentialIDs(t *testing.T) {
	desired, err := parseDiscoveryModels([]any{map[string]any{"name": "fixture", "type": "SINGLE", "ip_address_list": "192.0.2.1", "protocol_order": "SSH", "global_credential_list": []any{"lab-cli"}}})
	if err != nil {
		t.Fatal(err)
	}
	effect := map[string]any{}
	for k, v := range desired[0].fields {
		effect[k] = v
	}
	effect["globalCredentialIdList"] = []string{"original-id"}
	nativeID, credentialID := "42", "original-id"
	c := testClient(t, func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.URL.Path == authPath:
			fmt.Fprint(w, `{"Token":"test"}`)
		case r.URL.Path == taskPath+"task":
			fmt.Fprint(w, `{"response":{"id":"task","data":"42"}}`)
		case r.URL.Path == discoveryPath+"/1/100":
			json.NewEncoder(w).Encode(map[string]any{"response": []discoveryMetadata{{ID: nativeID, Name: "fixture", Type: "Range", Addresses: "192.0.2.1-192.0.2.1", Protocol: "SSH", CredentialIDs: []string{credentialID}, Condition: "Complete", Status: "Inactive"}}})
		default:
			t.Errorf("result verification must not resolve current credential names or mutate: %s %s", r.Method, r.URL.Path)
			w.WriteHeader(500)
		}
	})
	e := &ownedDiscoveryExecutor{base: &discoveryExecutor{client: c, desired: desired}, owner: "owner"}
	op := &cvk.NetworkControllerConfigurationOperation{Section: "inventory", Resource: ownedDiscoveryResource("fixture", effect), Action: "Create", TaskID: "task"}
	id, done, err := e.verifyOwned(context.Background(), op)
	if err != nil || !done || id != "42" {
		t.Fatal("original result binding unavailable", err)
	}
	legacy := op.DeepCopy()
	legacy.Resource = ownedDiscoveryPrefix + discoveryPublicDigest("fixture") + ":" + discoveryPublicDigest(effect)
	if id, done, err := e.verifyOwned(context.Background(), legacy); err != nil || !done || id != "42" {
		t.Fatal("legacy result digest recovery", err)
	}
	credentialID = "replaced-id"
	if _, _, err := e.verifyOwned(context.Background(), op); err == nil {
		t.Fatal("changed effect accepted")
	}
	credentialID = "original-id"
	nativeID = "replacement"
	if _, _, err := e.verifyOwned(context.Background(), op); err == nil {
		t.Fatal("same-name native replacement adopted")
	}
}

func TestDiscoveryOwnedReplacementRefusesDriftAndReplay(t *testing.T) {
	for _, mode := range []string{"unchanged", "foreign-id", "changed-footprint", "active-job", "credential-replaced", "lost-ack", "unowned"} {
		t.Run(mode, func(t *testing.T) {
			desired, err := parseDiscoveryModels([]any{map[string]any{"name": "fixture", "type": "SINGLE", "ip_address_list": "192.0.2.1", "protocol_order": "SSH,TELNET", "global_credential_list": []any{"lab-cli"}}})
			if err != nil {
				t.Fatal(err)
			}
			original := map[string]any{"name": "fixture", "discoveryType": "SINGLE", "ipAddressList": "192.0.2.1", "protocolOrder": "SSH", "globalCredentialIdList": []string{"credential-id"}}
			at := metav1.NewTime(time.Now().Add(-time.Second))
			verified := metav1.Now()
			receipt := cvk.NetworkControllerConfigurationActionApplication{Section: "inventory", Resource: ownedDiscoveryReceiptKey("fixture", "42"), Operation: cvk.NetworkControllerConfigurationOperation{ID: strings.Repeat("a", 32), ConfigName: "config", ConfigUID: "owner", ConfigGeneration: 1, ControllerGeneration: 1, Section: "inventory", Resource: ownedDiscoveryResource("fixture", original), Action: "Create", Phase: "Verified", TaskID: "create-task", SubmittedAt: &at}, VerifiedAt: &verified}
			nativeID, protocol, status, credential := "42", "SSH", "Inactive", "credential-id"
			writes := 0
			c := testClient(t, func(w http.ResponseWriter, r *http.Request) {
				switch {
				case r.URL.Path == authPath:
					fmt.Fprint(w, `{"Token":"test"}`)
				case r.URL.Path == globalCredentialsPath:
					json.NewEncoder(w).Encode(map[string]any{"response": map[string]any{"cliCredential": []map[string]any{{"id": credential, "description": "lab-cli"}}}})
				case r.Method == "GET" && r.URL.Path == discoveryPath+"/1/100":
					json.NewEncoder(w).Encode(map[string]any{"response": []discoveryMetadata{{ID: nativeID, Name: "fixture", Type: "Range", Addresses: "192.0.2.1-192.0.2.1", Protocol: protocol, CredentialIDs: []string{"credential-id"}, Condition: "Complete", Status: status}}})
				case r.Method == "DELETE" && r.URL.Path == discoveryPath+"/42":
					writes++
					if mode == "lost-ack" {
						fmt.Fprint(w, `{"response":{}}`)
					} else {
						fmt.Fprint(w, `{"response":{"taskId":"delete-task"}}`)
					}
				default:
					t.Errorf("unexpected %s %s", r.Method, r.URL.Path)
					w.WriteHeader(500)
				}
			})
			e := &ownedDiscoveryExecutor{base: &discoveryExecutor{client: c, desired: desired}, owner: "owner", receipts: []cvk.NetworkControllerConfigurationActionApplication{receipt}}
			if mode == "unowned" {
				e.receipts = nil
				if _, err := e.next(context.Background()); err == nil {
					t.Fatal("unowned job eligible for replacement")
				}
				if writes != 0 {
					t.Fatal("unowned mutation")
				}
				return
			}
			write, err := e.next(context.Background())
			if err != nil || write == nil || write.action != "Delete" || write.identity.target != "42" {
				t.Fatal("replacement planning", err)
			}
			switch mode {
			case "foreign-id":
				nativeID = "43"
			case "changed-footprint":
				protocol = "TELNET"
			case "active-job":
				status = "Active"
			case "credential-replaced":
				credential = "replacement-id"
			}
			_, err = write.submit(context.Background())
			allowed := mode == "unchanged" || mode == "active-job" || mode == "lost-ack"
			if (err == nil) != (mode == "unchanged" || mode == "active-job") || allowed && writes != 1 || !allowed && writes != 0 {
				t.Fatal("invalid replacement submission", err, writes)
			}
			if _, err := write.submit(context.Background()); err == nil {
				t.Fatal("replacement plan replayed")
			}
			if allowed && writes != 1 || !allowed && writes != 0 {
				t.Fatal("delete repeated")
			}
		})
	}
}

func TestDiscoveryOwnedDeletionRequiresLineageAndAbsence(t *testing.T) {
	desired, err := parseDiscoveryModels([]any{map[string]any{"name": "fixture", "type": "SINGLE", "ip_address_list": "192.0.2.1", "protocol_order": "SSH,TELNET", "global_credential_list": []any{"lab-cli"}}})
	if err != nil {
		t.Fatal(err)
	}
	original := map[string]any{"name": "fixture", "discoveryType": "SINGLE", "ipAddressList": "192.0.2.1", "protocolOrder": "SSH", "globalCredentialIdList": []string{"credential-id"}}
	submitted := metav1.NewTime(time.Now().Add(-time.Second))
	verified := metav1.Now()
	receipt := cvk.NetworkControllerConfigurationActionApplication{Section: "inventory", Resource: ownedDiscoveryReceiptKey("fixture", "42"), Operation: cvk.NetworkControllerConfigurationOperation{ID: strings.Repeat("a", 32), ConfigName: "cfg", ConfigUID: "owner", ConfigGeneration: 1, ControllerGeneration: 1, Section: "inventory", Resource: ownedDiscoveryResource("fixture", original), Action: "Create", Phase: "Verified", TaskID: "create-task", SubmittedAt: &submitted}, VerifiedAt: &verified}
	op := receipt.Operation.DeepCopy()
	op.ID = strings.Repeat("b", 32)
	op.Action = "Delete"
	op.TargetID = "42"
	op.TaskID = "delete-task"
	op.Phase = "Verifying"
	state := &cvk.NetworkControllerConfigurationStatus{ActionApplications: []cvk.NetworkControllerConfigurationActionApplication{receipt}}
	if err := ownedDiscoveryCapacity(state, op); err != nil {
		t.Fatal("valid delete lineage", err)
	}
	if err := ownedDiscoveryCapacity(&cvk.NetworkControllerConfigurationStatus{}, op); err == nil {
		t.Fatal("delete without creation provenance")
	}
	foreign := op.DeepCopy()
	foreign.TargetID = "43"
	if err := ownedDiscoveryCapacity(state, foreign); err == nil {
		t.Fatal("delete for different target")
	}
	present, otherName := true, false
	c := testClient(t, func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == authPath {
			fmt.Fprint(w, `{"Token":"test"}`)
			return
		}
		if r.Method != "GET" || r.URL.Path != discoveryPath+"/1/100" {
			t.Errorf("unexpected request %s %s", r.Method, r.URL.Path)
			w.WriteHeader(500)
			return
		}
		rows := []discoveryMetadata{}
		if present {
			name := "fixture"
			id := "42"
			if otherName {
				id = "competing-id"
			}
			rows = append(rows, discoveryMetadata{ID: id, Name: name, Type: "Range", Addresses: "192.0.2.1-192.0.2.1", Protocol: "SSH", CredentialIDs: []string{"credential-id"}, Condition: "Complete", Status: "Inactive"})
		}
		json.NewEncoder(w).Encode(map[string]any{"response": rows})
	})
	e := &ownedDiscoveryExecutor{base: &discoveryExecutor{client: c, desired: desired}, owner: "owner", receipts: []cvk.NetworkControllerConfigurationActionApplication{receipt}}
	if _, done, err := e.verifyOwned(context.Background(), op); err != nil || done {
		t.Fatal("successful task inferred absence", err)
	}
	present = false
	if id, done, err := e.verifyOwned(context.Background(), op); err != nil || !done || id != "42" {
		t.Fatal("stable absence not verified", err)
	}
	present = true
	otherName = true
	if _, _, err := e.verifyOwned(context.Background(), op); err == nil {
		t.Fatal("competing same-name job accepted")
	}
}
