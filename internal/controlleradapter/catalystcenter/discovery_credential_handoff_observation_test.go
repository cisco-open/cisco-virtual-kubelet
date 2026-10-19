// Copyright 2026 Cisco Systems Inc.
// SPDX-License-Identifier: Apache-2.0
package catalystcenter

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	configapi "github.com/cisco/virtual-kubelet-cisco/api/config/v1alpha1"
	cvk "github.com/cisco/virtual-kubelet-cisco/api/v1alpha1"
	"github.com/cisco/virtual-kubelet-cisco/internal/controlleradapter"
	core "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	ctrlclient "sigs.k8s.io/controller-runtime/pkg/client"
)

var credentialHandoffObservationModes = []string{"complete", "configmap", "same-name", "unrelated-native", "global-reference", "global-id-race", "counter-reuse", "projection-lag", "old-config-present", "retired-uuid", "same-job-name", "native-error", "source-race", "projection-race", "journal-race", "config-race", "missing-condition", "changed-digest", "missing-pair", "pending-operation", "paused", "controller-generation", "unenrolled"}

func TestDiscoveryCredentialHandoffObservation(t *testing.T) {
	for _, mode := range credentialHandoffObservationModes {
		t.Run(mode, func(t *testing.T) { discoveryCredentialCleanupObservationScenario(t, "handoff-observe/"+mode, nil) })
	}
}

type credentialObservationStatusClient struct {
	ctrlclient.Client
	writes *int
}
type credentialObservationStatusWriter struct {
	ctrlclient.SubResourceWriter
	writes *int
}

func (c credentialObservationStatusClient) Status() ctrlclient.SubResourceWriter {
	return credentialObservationStatusWriter{c.Client.Status(), c.writes}
}
func (w credentialObservationStatusWriter) Update(context.Context, ctrlclient.Object, ...ctrlclient.SubResourceUpdateOption) error {
	*w.writes++
	return errors.New("handoff observation cannot write status")
}

func exerciseDiscoveryCredentialHandoffObservation(t *testing.T, a *adapter, old *configapi.NetworkControllerConfig, mode string, realAPI bool, successorRefs ...[]configapi.NetworkControllerSecretRef) {
	t.Helper()
	custodyMode := ""
	if strings.HasPrefix(mode, "custody-") {
		custodyMode = strings.TrimPrefix(mode, "custody-")
		mode = "complete"
		if strings.HasPrefix(custodyMode, "public-revision-") && strings.Contains(custodyMode, "configmap") || strings.HasPrefix(custodyMode, "public-configmap") || custodyMode == "configmap" || custodyMode == "source-race" || strings.HasPrefix(custodyMode, "execute-configmap") || strings.HasPrefix(custodyMode, "execute-cleanup-configmap") || strings.HasPrefix(custodyMode, "execute-cancel-") && (strings.Contains(custodyMode, "configmap") || strings.Contains(custodyMode, "source")) {
			mode = "configmap"
		}
		if custodyMode == "same-name" || custodyMode == "public-same-name" {
			mode = "same-name"
		}
		if custodyMode == "public-old-config-present" {
			mode = "old-config-present"
		}
		if custodyMode == "public-counter-reuse" {
			mode = "counter-reuse"
		}
	}
	ctx := context.Background()
	admin := a.statusWriter
	nc, err := a.currentController(ctx)
	if err != nil {
		t.Fatal(err)
	}
	oldUID := string(old.UID)
	if mode != "old-config-present" {
		old.Finalizers = nil
		if err := admin.Update(ctx, old); err != nil {
			t.Fatal(err)
		}
		var gone configapi.NetworkControllerConfig
		if err := admin.Get(ctx, ctrlclient.ObjectKeyFromObject(old), &gone); !apierrors.IsNotFound(err) {
			t.Fatal("old config not fully removed", err)
		}
	}
	version := int64(1)
	for _, app := range nc.Status.Configuration.CredentialApplications {
		for _, v := range app.Versions {
			if v.Version >= version {
				version = v.Version + 1
			}
		}
	}
	if mode == "counter-reuse" {
		version = 1
	}
	raw := discoveryCredentialSource(t, map[string]any{"user_name_list": []any{"operator"}, "password_list_version": version}, mode == "global-reference" || mode == "global-id-race")
	c := &configapi.NetworkControllerConfig{ObjectMeta: metav1.ObjectMeta{Name: old.Name + "-next", Namespace: old.Namespace}, Spec: *old.Spec.DeepCopy()}
	// An intentionally emptied predecessor has no current projection paths.
	// Supply the new owner's explicit paths without editing the retired spec.
	if len(successorRefs) > 1 {
		t.Fatal("multiple successor projection declarations")
	}
	if len(successorRefs) == 1 {
		c.Spec.SecretRefs = append([]configapi.NetworkControllerSecretRef(nil), successorRefs[0]...)
	}
	if mode == "same-name" {
		c.Name = old.Name
	}
	if !realAPI {
		c.UID = types.UID(string(old.UID) + "-successor")
		c.Generation = 1
	}
	if custodyMode == "execute-known-unknown" {
		c.Spec.TaskTimeout = &metav1.Duration{Duration: time.Nanosecond}
	}
	c.Spec.Source.Inline = &runtime.RawExtension{Raw: raw}
	c.Spec.Source.ConfigMapRef = nil
	// Keep credentials in the same authorized projection mechanism. A new config
	// name gets its own path; no original credential file or audit is rewritten.
	var projected string
	for _, ref := range c.Spec.SecretRefs {
		var source cvk.NetworkControllerIntentSecretSource
		for _, candidate := range nc.Spec.IntentSecretSources {
			if candidate.Alias == ref.Source {
				source = candidate
			}
		}
		relative, err := controlleradapter.IntentSecretRelativePath(controlleradapter.IntentSecretPathInput{ConfigName: c.Name, Section: ref.Section, JSONPointer: ref.Path, SourceAlias: ref.Source, SecretName: source.Name, SecretKey: source.Key})
		if err != nil {
			t.Fatal(err)
		}
		projected = filepath.Join(a.intentSecretPath, relative)
		if err := os.MkdirAll(filepath.Dir(projected), 0700); err != nil {
			t.Fatal(err)
		}
		projectedVersion := version
		if mode == "projection-lag" {
			projectedVersion = 1
		}
		if err := os.WriteFile(projected, versionedSecretFixture(t, projectedVersion, `["CVK_SENSITIVE_TEST_VALUE"]`), 0600); err != nil {
			t.Fatal(err)
		}
	}
	if err := admin.Create(ctx, c); err != nil {
		t.Fatal(err)
	}
	var cm *core.ConfigMap
	if mode == "configmap" || mode == "source-race" {
		cm = &core.ConfigMap{ObjectMeta: metav1.ObjectMeta{Name: "handoff-source", Namespace: c.Namespace}, Data: map[string]string{"model": string(raw)}}
		if !realAPI {
			cm.UID = "handoff-model"
		}
		if err := admin.Create(ctx, cm); err != nil {
			t.Fatal(err)
		}
		c.Spec.Source.Inline = nil
		c.Spec.Source.ConfigMapRef = &configapi.ConfigMapKeyRef{Name: cm.Name, Key: "model"}
		if err := admin.Update(ctx, c); err != nil {
			t.Fatal(err)
		}
	}
	unused := 0
	enrollPublicCredentialFixture(t, a, c, mode, &unused)
	if mode == "unenrolled" {
		c.Finalizers = []string{"example.com/keeper"}
		if err := admin.Update(ctx, c); err != nil {
			t.Fatal(err)
		}
	}
	nc, err = a.currentController(ctx)
	if err != nil {
		t.Fatal(err)
	}
	switch mode {
	case "missing-condition":
		meta.RemoveStatusCondition(&nc.Status.Conditions, discoveryRetirementCondition)
	case "changed-digest":
		cond := meta.FindStatusCondition(nc.Status.Conditions, discoveryRetirementCondition)
		var r discoveryOwnershipRetirement
		json.Unmarshal([]byte(cond.Message), &r)
		r.RetainedDigest = strings.Repeat("a", 64)
		encoded, _ := json.Marshal(r)
		cond.Message = string(encoded)
	case "missing-pair":
		nc.Status.Configuration.CredentialApplications = nil
	case "pending-operation":
		nc.Status.Configuration.Operation = nc.Status.Configuration.ActionApplications[0].Operation.DeepCopy()
	case "paused":
		nc.Spec.Paused = true
	case "controller-generation":
		nc.Generation++
		nc.Spec.Connection.MaxConcurrentRequests = 8
	}
	if mode == "paused" || mode == "controller-generation" {
		if err := admin.Update(ctx, nc); err != nil {
			t.Fatal(err)
		}
		a.generation = nc.Generation
	} else if mode == "missing-condition" || mode == "changed-digest" || mode == "missing-pair" || mode == "pending-operation" {
		if err := admin.Status().Update(ctx, nc); err != nil {
			t.Fatal(err)
		}
	}
	reads, mutations, globalReads := 0, 0, 0
	nativeReturned := false
	a.client = testClient(t, func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == authPath {
			fmt.Fprint(w, `{"Token":"fixture"}`)
			return
		}
		if r.Method == "GET" && r.URL.Path == globalCredentialsPath {
			globalReads++
			id := "global-id"
			if mode == "global-id-race" && globalReads > 1 {
				id = "changed-global-id"
			}
			json.NewEncoder(w).Encode(map[string]any{"response": map[string]any{"cliCredential": []map[string]any{{"id": id, "description": "lab-cli"}}}})
			return
		}
		if r.Method != "GET" || r.URL.Path != discoveryPath+"/1/100" {
			mutations++
			t.Error("handoff observation issued mutation", r.Method, r.URL.Path)
			w.WriteHeader(500)
			return
		}
		reads++
		if mode == "native-error" {
			w.WriteHeader(503)
			return
		}
		if reads == 1 {
			switch mode {
			case "source-race":
				cm.Data["model"] += " "
				if err := admin.Update(ctx, cm); err != nil {
					t.Error(err)
				}
			case "projection-race":
				if err := os.Remove(projected); err != nil {
					t.Error(err)
				}
			case "journal-race":
				current, err := a.currentController(ctx)
				if err != nil {
					t.Error(err)
					break
				}
				current.Status.Configuration.CredentialApplications = nil
				if err := admin.Status().Update(ctx, current); err != nil {
					t.Error(err)
				}
			case "config-race":
				current := c.DeepCopy()
				current.Spec.PrunePolicy = configapi.NetworkControllerRetentionPolicyDelete
				if err := admin.Update(ctx, current); err != nil {
					t.Error(err)
				}
			}
		}
		rows := []discoveryMetadata{}
		if nativeReturned {
			rows = append(rows, discoveryMetadata{ID: "job-id", Name: "returned-native", Type: "SINGLE"})
		}
		switch mode {
		case "retired-uuid":
			rows = append(rows, discoveryMetadata{ID: "job-id", Name: "external-renamed-job", Type: "SINGLE"})
		case "same-job-name":
			rows = append(rows, discoveryMetadata{ID: "foreign-job", Name: "fixture", Type: "SINGLE"})
		case "unrelated-native":
			rows = append(rows, discoveryMetadata{ID: "unrelated", Name: "unrelated", Type: "SINGLE"})
		}
		json.NewEncoder(w).Encode(map[string]any{"response": rows})
	})
	source, err := loadControllerSource(ctx, a.statusReader, c)
	if err != nil {
		t.Fatal(err)
	}
	var baseline cvk.NetworkController
	if err := admin.Get(ctx, a.key, &baseline); err != nil {
		t.Fatal(err)
	}
	statusWrites := 0
	a.statusWriter = credentialObservationStatusClient{Client: admin, writes: &statusWrites}
	defer func() { a.statusWriter = admin }()
	observed, err := a.observeDiscoveryCredentialOwnershipHandoff(ctx, c, source)
	success := mode == "global-reference" || mode == "complete" || mode == "configmap" || mode == "same-name" || mode == "unrelated-native"
	if (err == nil) != success || (observed != nil) != success {
		t.Fatal("unexpected handoff observation", mode, err, observed)
	}
	switch mode {
	case "retired-uuid", "same-job-name", "native-error", "source-race", "projection-race", "journal-race", "config-race", "global-id-race":
		if reads == 0 {
			t.Fatal("native/race qualification did not reach its intended observation boundary")
		}
	}
	if mode == "global-id-race" && globalReads != 2 {
		t.Fatal("resolved global-reference drift was not exercised")
	}
	if mutations != 0 || statusWrites != 0 {
		t.Fatal("observation granted mutation or ownership authority")
	}
	var current cvk.NetworkController
	if err := admin.Get(ctx, a.key, &current); err != nil {
		t.Fatal(err)
	}
	if success {
		if reads != 2 || observed.ConfigUID != string(c.UID) || observed.RetiringConfigUID != oldUID || observed.ControllerVersion != baseline.ResourceVersion || !lowerDiscoveryDigest(observed.InputsDigest) || observed.EvidenceDigest != featureIdentityDigest(baseline.Status.Configuration) || !reflect.DeepEqual(baseline.Status, current.Status) {
			t.Fatal("candidate lost source/evidence scope or mutated authority")
		}
	}
	if current.Status.Configuration.Owners[0].ConfigUID != oldUID {
		t.Fatal("read-only observation transferred ownership")
	}
	if strings.HasPrefix(custodyMode, "public-") {
		a.statusWriter = admin
		exerciseDiscoveryCredentialPublicHandoff(t, a, c, strings.TrimPrefix(custodyMode, "public-"), cm, projected, admin, realAPI)
		return
	}
	if custodyMode != "" {
		a.statusWriter = admin
		exerciseDiscoveryCredentialCustody(t, a, c, source, observed, custodyMode, cm, projected, func() { nativeReturned = true }, &mutations, realAPI)
	}
}

func TestDiscoveryCredentialSuccessorVersionFloors(t *testing.T) {
	f := newDiscoveryLineageFixture()
	f.create(t, discoveryLineageVersions(2, 5), "")
	context := f.retire(t, discoveryLineageVersions(3, 0))
	f.create(t, discoveryLineageVersions(3, 0), context)
	f.retire(t, discoveryLineageVersions(99, 0))
	history, err := discoveryCredentialLineages(f.state, "owner")
	if err != nil {
		t.Fatal(err)
	}
	for _, test := range []struct {
		name             string
		password, enable int64
		ok               bool
	}{{"advanced", 4, 6, true}, {"same-password", 3, 6, false}, {"old-password", 2, 6, false}, {"removed-enable-reused", 4, 5, false}, {"queued-is-not-delivered", 4, 0, true}} {
		t.Run(test.name, func(t *testing.T) {
			versions := discoveryLineageVersions(test.password, test.enable)
			for i := range versions {
				versions[i].Path = strings.Replace(versions[i].Path, "/0/", "/9/", 1)
			}
			err := discoveryCredentialSuccessorVersions(history, discoveryPublicDigest("fixture"), versions)
			if (err == nil) != test.ok {
				t.Fatal("wrong logical-field floor", err)
			}
		})
	}
}
