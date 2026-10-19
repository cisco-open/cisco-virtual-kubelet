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

	configapi "github.com/cisco/virtual-kubelet-cisco/api/config/v1alpha1"
	cvk "github.com/cisco/virtual-kubelet-cisco/api/v1alpha1"
	core "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	ctrlclient "sigs.k8s.io/controller-runtime/pkg/client"
)

var discoveryCleanupObservationModes = []string{"ready", "active", "tombstone", "native-edit", "native-race", "owner-race", "spec-race", "source-race", "foreign-alias", "foreign-malformed", "missing-owner", "credential-custody", "foreign-owned-section", "pending-operation", "missing-receipt", "missing-native", "wrong-policy", "mixed-source"}

func TestDiscoveryConfigCleanupObservation(t *testing.T) {
	for _, mode := range discoveryCleanupObservationModes {
		t.Run(mode, func(t *testing.T) { exerciseDiscoveryCleanupObservation(t, mode, nil) })
	}
}

func exerciseDiscoveryCleanupObservation(t *testing.T, mode string, install func(*adapter, *configapi.NetworkControllerConfig)) {
	t.Helper()
	ctx := context.Background()
	a, config, world := discoveryPruneFixture(t)
	reads, writes := 0, 0
	var cm *core.ConfigMap
	a.client = testClient(t, func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == authPath {
			fmt.Fprint(w, `{"Token":"test"}`)
			return
		}
		if r.Method != "GET" || r.URL.Path != discoveryPath+"/1/100" {
			writes++
			t.Error("cleanup observation invoked a writer/unsupported API", r.Method, r.URL.Path)
			w.WriteHeader(400)
			return
		}
		reads++
		if reads == 1 && mode == "owner-race" {
			nc, _ := a.currentController(ctx)
			nc.Status.Configuration.Owners[0].ConfigUID = "foreign"
			if err := a.statusWriter.Status().Update(ctx, nc); err != nil {
				t.Error(err)
			}
		}
		if reads == 1 && mode == "spec-race" {
			copy := config.DeepCopy()
			copy.Spec.ModelSource.Exporter = "changed"
			if err := a.statusWriter.Update(ctx, copy); err != nil {
				t.Error(err)
			}
		}
		if reads == 1 && mode == "source-race" {
			cm.Data["model"] += " "
			if err := a.statusWriter.Update(ctx, cm); err != nil {
				t.Error(err)
			}
		}
		row := world.jobs["fixture"]
		if mode == "native-edit" || mode == "native-race" && reads == 2 {
			row.Protocol = "TELNET"
		}
		if mode == "active" {
			row.Status = "Active"
			row.Condition = "In Progress"
		}
		rows := []discoveryMetadata{world.jobs["foreign"]}
		if mode != "tombstone" && mode != "missing-native" {
			rows = append(rows, row)
		}
		json.NewEncoder(w).Encode(map[string]any{"response": rows})
	})
	nc, _ := a.currentController(ctx)
	nc.Spec.Endpoint = a.client.cfg.Endpoint
	if err := a.statusWriter.Update(ctx, nc); err != nil {
		t.Fatal(err)
	}
	if install != nil {
		install(a, config)
	}
	if mode == "source-race" {
		cm = &core.ConfigMap{ObjectMeta: metav1.ObjectMeta{Name: "cleanup-source", Namespace: config.Namespace}, Data: map[string]string{"model": emptyDiscoveryModel}}
		if install == nil {
			cm.UID = "cleanup-source"
		}
		if err := a.statusWriter.Create(ctx, cm); err != nil {
			t.Fatal(err)
		}
		config.Spec.Source.Inline = nil
		config.Spec.Source.ConfigMapRef = &configapi.ConfigMapKeyRef{Name: cm.Name, Key: "model"}
	}
	config.Spec.DeletionPolicy = configapi.NetworkControllerRetentionPolicyDelete
	config.Finalizers = []string{"test.cisco.vk/observation-only"}
	if mode == "wrong-policy" {
		config.Spec.DeletionPolicy = configapi.NetworkControllerRetentionPolicyRetain
	}
	if mode == "mixed-source" {
		config.Spec.Source.Inline.Raw = []byte(`{"catalyst_center":{"inventory":{"discovery":[],"tags":[{"name":"foreign"}]}}}`)
	}
	if err := a.statusWriter.Update(ctx, config); err != nil {
		t.Fatal(err)
	}
	if err := a.statusWriter.Delete(ctx, config); err != nil {
		t.Fatal(err)
	}
	if err := a.statusReader.Get(ctx, ctrlclient.ObjectKeyFromObject(config), config); err != nil {
		t.Fatal(err)
	}
	nc, _ = a.currentController(ctx)
	state := nc.Status.Configuration
	switch mode {
	case "tombstone":
		r := &state.ActionApplications[0]
		r.Operation.Action = "Delete"
		r.Operation.TargetID = "42"
		r.Operation.Resource += discoveryPruneSuffix
	case "foreign-alias", "foreign-malformed":
		r := *state.ActionApplications[0].DeepCopy()
		r.Operation.ConfigUID = "foreign"
		r.Operation.ConfigName = "foreign"
		r.Resource = ownedDiscoveryReceiptKey("foreign", "42")
		r.Operation.Resource = strings.Replace(r.Operation.Resource, discoveryPublicDigest("fixture"), discoveryPublicDigest("foreign"), 1)
		if mode == "foreign-malformed" {
			r.Operation.Phase = "Unknown"
		}
		state.ActionApplications = append(state.ActionApplications, r)
	case "missing-owner":
		state.Owners = nil
	case "credential-custody":
		state.CredentialApplications = []cvk.NetworkControllerConfigurationCredentialApplication{{Section: "inventory", Resource: "custody", ConfigUID: string(config.UID), TargetID: "custody-id", OperationID: strings.Repeat("c", 32), Versions: []cvk.NetworkControllerConfigurationCredentialVersion{{Path: "/custody", Version: 1, SourceAlias: "unused", SecretName: "unread", SecretKey: "unread"}}}}
	case "foreign-owned-section":
		state.Owners = append(state.Owners, cvk.NetworkControllerSectionOwner{Section: "sites", ConfigUID: string(config.UID), ConfigName: config.Name})
	case "pending-operation":
		state.Operation = state.ActionApplications[0].Operation.DeepCopy()
		state.Operation.Phase = "Unknown"
	case "missing-receipt":
		state.ActionApplications[0].Operation.CreatedResourceID = "malformed"
	}
	if err := a.statusWriter.Status().Update(ctx, nc); err != nil {
		t.Fatal(err)
	}
	before := journalState(t, a).DeepCopy()
	observation, err := a.observeDiscoveryConfigCleanup(ctx, config)
	success := mode == "ready" || mode == "active" || mode == "tombstone"
	if (err == nil) != success || (observation != nil) != success {
		t.Fatal("incorrect cleanup observation", observation, err)
	}
	if success {
		count := 1
		if mode == "tombstone" {
			count = 0
		}
		if len(observation.Candidates) != count || reads != 2 || observation.ConfigUID != string(config.UID) || observation.SpecDigest != featureIdentityDigest(config.Spec) || observation.DeletionDigest != featureIdentityDigest(config.DeletionTimestamp) || observation.EvidenceDigest == "" {
			t.Fatal("cleanup scope/proof incomplete", observation)
		}
	}
	if writes != 0 || len(world.writes) != 0 {
		t.Fatal("observation mutated native state")
	}
	if mode != "owner-race" && !reflect.DeepEqual(before, journalState(t, a)) {
		t.Fatal("observation mutated controller evidence")
	}
}
