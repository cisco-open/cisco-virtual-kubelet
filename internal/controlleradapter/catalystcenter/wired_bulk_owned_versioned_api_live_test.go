// Copyright © 2026 Cisco Systems Inc.
// SPDX-License-Identifier: Apache-2.0
//go:build envtest && live

package catalystcenter

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/hex"
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
	"github.com/cisco/virtual-kubelet-cisco/internal/controlleradapter"
	core "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	ctrl "sigs.k8s.io/controller-runtime"
	ctrlclient "sigs.k8s.io/controller-runtime/pkg/client"
)

// Payload values are compared only in ephemeral memory. Never format, hash or
// retain them in diagnostics. The guard permits exactly one create and rotation
// for each generated credential and exact-UUID cleanup; no assignment/device API.
type liveOwnedVersionedTrace struct {
	base     http.RoundTripper
	expected map[string]map[string]any
	targets  map[string]string
	writes   map[string]int
	deletes  map[string]bool
}

func (trace *liveOwnedVersionedTrace) RoundTrip(req *http.Request) (*http.Response, error) {
	if req.Method == http.MethodGet || req.URL.Path == authPath {
		return trace.base.RoundTrip(req)
	}
	if req.Method == http.MethodDelete {
		for _, target := range trace.targets {
			if target != "" && req.URL.Path == globalCredentialsPath+"/"+target && !trace.deletes[target] {
				trace.deletes[target] = true
				return trace.base.RoundTrip(req)
			}
		}
		return nil, errors.New("isolated credential cleanup identity denied")
	}
	if req.URL.Path != globalCredentialsPath || req.Method != http.MethodPost && req.Method != http.MethodPut {
		return nil, errors.New("isolated credential test denied out-of-scope mutation")
	}
	raw, err := io.ReadAll(req.Body)
	if err != nil {
		return nil, err
	}
	req.Body = io.NopCloser(bytes.NewReader(raw))
	var envelope map[string]json.RawMessage
	if json.Unmarshal(raw, &envelope) != nil || len(envelope) != 1 {
		return nil, errors.New("isolated credential envelope denied")
	}
	for kind, value := range envelope {
		want, exists := trace.expected[kind]
		if !exists {
			return nil, errors.New("unselected credential kind denied")
		}
		var got map[string]any
		if req.Method == http.MethodPost {
			var list []map[string]any
			if trace.writes[kind] != 0 || json.Unmarshal(value, &list) != nil || len(list) != 1 {
				return nil, errors.New("credential create replay denied")
			}
			got = list[0]
		} else {
			if trace.writes[kind] != 1 || trace.targets[kind] == "" || json.Unmarshal(value, &got) != nil {
				return nil, errors.New("credential rotation identity/replay denied")
			}
		}
		expected := map[string]any{}
		for key, value := range want {
			expected[key] = value
		}
		if req.Method == http.MethodPut {
			expected["id"] = trace.targets[kind]
		}
		if !reflect.DeepEqual(expected, got) {
			return nil, errors.New("isolated credential payload differs from authorized generated values")
		}
		trace.writes[kind]++
	}
	return trace.base.RoundTrip(req)
}

func (trace *liveOwnedVersionedTrace) count() int {
	count := 0
	for _, n := range trace.writes {
		count += n
	}
	return count
}

func TestLivePrivateAPIOwnedVersionedComposition(t *testing.T) {
	if os.Getenv("CATC_OWNED_VERSIONED_API_EXECUTION") != "private-api-native-unassigned-credentials" {
		t.Skip("private API/native versioned credential composition not enabled")
	}
	api := qualificationAPIServerClient(t, "../../../config/crd/config.cisco.vk_networkcontrollerconfigs.yaml")
	c := liveClient(t)
	ctx, cancel := context.WithTimeout(context.Background(), 9*time.Minute)
	defer cancel()
	before, err := c.globalCredentials(ctx)
	if err != nil {
		t.Fatal("credential baseline unavailable")
	}
	seed := make([]byte, 12)
	if _, err := rand.Read(seed); err != nil {
		t.Fatal(err)
	}
	prefix := "CVK-Owned-" + hex.EncodeToString(seed)
	definitions := map[string]any{}
	names := map[string]string{}
	for _, kind := range globalCredentialKinds {
		var entry map[string]any
		if err := json.Unmarshal([]byte(globalCredentialFixtureEntries[kind.model]), &entry); err != nil {
			t.Fatal(err)
		}
		name := prefix + "-" + strings.ToLower(kind.api)
		entry["name"] = name
		if _, exists := before[kind.model][name]; exists {
			t.Fatal("generated credential name occupied")
		}
		definitions[kind.model] = []any{entry}
		names[kind.model] = name
	}
	modelBytes := func() []byte {
		raw, err := json.Marshal(map[string]any{"catalyst_center": map[string]any{"network_settings": map[string]any{"device_credentials": definitions}}})
		if err != nil {
			t.Fatal(err)
		}
		return raw
	}
	wants, err := parseGlobalCredentials(modelBytes(), true)
	if err != nil {
		t.Fatal(err)
	}
	const namespace = "owned-versioned-native"
	if err := api.Create(ctx, &core.Namespace{ObjectMeta: metav1.ObjectMeta{Name: namespace}}); err != nil {
		t.Fatal(err)
	}
	nc := &cvk.NetworkController{ObjectMeta: metav1.ObjectMeta{Name: "cc", Namespace: namespace}, Spec: cvk.NetworkControllerSpec{Type: TypeName, Endpoint: c.cfg.Endpoint, CredentialSecretRef: cvk.NetworkControllerSecretReference{Name: "connection-credentials"}}}
	owner := &configapi.NetworkControllerConfig{ObjectMeta: metav1.ObjectMeta{Name: "credential-owner", Namespace: namespace}, Spec: configapi.NetworkControllerConfigSpec{ControllerRef: configapi.NetworkControllerRef{Name: nc.Name}, Scope: "global", Mode: configapi.NetworkControllerApplyModeApply, ManagedSections: []string{"network_settings"}, ModelSource: configapi.NetworkControllerNetAsCodeModelSource{Format: "netascode-catalyst-center", ModelVersion: "0.5.0", Resolved: true}, Source: configapi.NetworkControllerConfigurationSource{Inline: &runtime.RawExtension{Raw: modelBytes()}}}}
	projectionRoot := t.TempDir()
	paths := map[string]string{}
	values := map[string]string{}
	allValues := []string{}
	for _, want := range wants {
		for _, leaf := range want.credentials {
			alias := fmt.Sprintf("source-%d", len(nc.Spec.IntentSecretSources))
			nc.Spec.IntentSecretSources = append(nc.Spec.IntentSecretSources, cvk.NetworkControllerIntentSecretSource{Alias: alias, Name: "qualification-values", Key: alias})
			owner.Spec.SecretRefs = append(owner.Spec.SecretRefs, configapi.NetworkControllerSecretRef{Section: "network_settings", Path: leaf.path, Source: alias})
			relative, err := controlleradapter.IntentSecretRelativePath(controlleradapter.IntentSecretPathInput{ConfigName: owner.Name, Section: "network_settings", JSONPointer: leaf.path, SourceAlias: alias, SecretName: "qualification-values", SecretKey: alias})
			if err != nil {
				t.Fatal(err)
			}
			paths[leaf.path] = filepath.Join(projectionRoot, relative)
		}
	}
	if err := api.Create(ctx, nc); err != nil {
		t.Fatal(err)
	}
	if err := api.Create(ctx, owner); err != nil {
		t.Fatal(err)
	}
	a := &adapter{client: c, statusReader: api, statusWriter: api, key: ctrlclient.ObjectKeyFromObject(nc), uid: nc.UID, generation: nc.Generation, intentSecretPath: projectionRoot}
	rest, _, err := c.session(ctx)
	if err != nil {
		t.Fatal("native session unavailable")
	}
	base := rest.HTTPClient.Transport
	if base == nil {
		base = http.DefaultTransport
	}
	trace := &liveOwnedVersionedTrace{base: base, expected: map[string]map[string]any{}, targets: map[string]string{}, writes: map[string]int{}, deletes: map[string]bool{}}
	rest.HTTPClient.Transport = trace
	settled := map[string]globalCredentialMetadata{}
	// Only exact verified identities are removed. An unsettled operation keeps
	// public diagnostic identities and its redacted existing journal for recovery.
	t.Cleanup(func() {
		cleanupCtx, done := context.WithTimeout(context.Background(), 2*time.Minute)
		defer done()
		current, err := a.currentController(cleanupCtx)
		if err != nil || current.Status.Configuration == nil || current.Status.Configuration.Operation != nil || len(settled) != 6 {
			diagnostic := struct {
				Names         map[string]string
				Targets       map[string]string
				Configuration *cvk.NetworkControllerConfigurationStatus
			}{names, trace.targets, nil}
			if current != nil {
				diagnostic.Configuration = current.Status.Configuration
			}
			raw, _ := json.Marshal(diagnostic)
			for _, value := range allValues {
				raw = bytes.ReplaceAll(raw, []byte(value), []byte("<redacted>"))
			}
			file, fileErr := os.CreateTemp("/tmp", "cvk-owned-versioned-retained-*.json")
			if fileErr == nil {
				_, fileErr = file.Write(raw)
				file.Close()
				t.Log("unsettled generated credential identities and redacted journal retained", file.Name())
			}
			if fileErr != nil {
				t.Error("cannot retain non-secret qualification identities")
			}
			return
		}
		for _, kind := range globalCredentialKinds {
			actual, err := c.globalCredentials(cleanupCtx)
			if err != nil {
				t.Error("cleanup observation unavailable")
				return
			}
			got, exists := actual[kind.model][names[kind.model]]
			expected := settled[kind.model]
			if !exists || !reflect.DeepEqual(got, expected) || got.ID != trace.targets[kind.api] {
				t.Error("cleanup refused changed or replaced generated credential")
				return
			}
			submitted := time.Now()
			raw, err := c.mutate(cleanupCtx, http.MethodDelete, globalCredentialsPath+"/"+got.ID, nil)
			if err != nil {
				t.Error("credential cleanup submission uncertain")
				return
			}
			var ack struct {
				Response Task `json:"response"`
			}
			if json.Unmarshal(raw, &ack) != nil || !validAPIID(ack.Response.ID) {
				t.Error("credential cleanup acknowledgement unavailable")
				return
			}
			if err := waitSiteCredentialTask(cleanupCtx, c, ack.Response, submitted); err != nil {
				t.Error("credential cleanup task uncertain")
				return
			}
			t.Log("generated credential removed", kind.model, got.ID, ack.Response.ID)
		}
		after, err := c.globalCredentials(cleanupCtx)
		if err != nil || !reflect.DeepEqual(before, after) {
			t.Error("complete non-secret credential baseline was not restored")
			return
		}
		t.Log("six generated unassigned credentials removed; complete non-secret native baseline restored")
	})
	writeProjection := func(path string, version int64, value string) {
		if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
			t.Fatal(err)
		}
		raw, err := json.Marshal(map[string]any{"version": version, "value": value})
		if err != nil {
			t.Fatal(err)
		}
		file, err := os.CreateTemp(filepath.Dir(path), ".projection-*")
		if err != nil {
			t.Fatal(err)
		}
		name := file.Name()
		if _, err := file.Write(raw); err != nil {
			file.Close()
			t.Fatal(err)
		}
		if err := file.Close(); err != nil {
			t.Fatal(err)
		}
		if err := os.Rename(name, path); err != nil {
			t.Fatal(err)
		}
	}
	tick := func() {
		if _, err := a.currentController(ctx); err != nil {
			t.Fatal("pinned API worker identity changed")
		}
		restarted := *a
		if _, err := (&configReconciler{a: &restarted}).Reconcile(ctx, ctrl.Request{NamespacedName: ctrlclient.ObjectKeyFromObject(owner)}); err != nil {
			t.Fatalf("reconcile failed (%T)", err)
		}
		if err := api.Get(ctx, ctrlclient.ObjectKeyFromObject(owner), owner); err != nil {
			t.Fatal(err)
		}
	}
	var previousObserver wiredBulkDependencyObserver
	previousDigest := ""
	for phase := int64(1); phase <= 2; phase++ {
		if phase == 2 {
			for _, kind := range globalCredentialKinds {
				entry := definitions[kind.model].([]any)[0].(map[string]any)
				for key := range entry {
					if strings.HasSuffix(key, "_version") {
						entry[key] = phase
					}
				}
			}
			owner.Spec.Source.Inline.Raw = modelBytes()
			if err := api.Update(ctx, owner); err != nil {
				t.Fatal(err)
			}
		}
		wants, err = parseGlobalCredentials(modelBytes(), true)
		if err != nil {
			t.Fatal(err)
		}
		for _, want := range wants {
			payload := globalCredentialMetadataPayload(want.metadata)
			for _, leaf := range want.credentials {
				random := make([]byte, 12)
				if _, err := rand.Read(random); err != nil {
					t.Fatal(err)
				}
				value := hex.EncodeToString(random)
				values[leaf.path] = value
				allValues = append(allValues, value)
				payload[leaf.field] = value
			}
			trace.expected[want.kind.api] = payload
		}
		if phase == 2 {
			beforeWrites := trace.count()
			tick()
			if trace.count() != beforeWrites || journalState(t, a).Operation != nil {
				t.Fatal("stale versioned projections submitted a rotation")
			}
			// Update the complete first selected credential while leaving later
			// definitions stale. Whole-selection preflight must still refuse.
			for _, leaf := range wants[0].credentials {
				writeProjection(paths[leaf.path], phase, values[leaf.path])
			}
			tick()
			if trace.count() != beforeWrites || journalState(t, a).Operation != nil {
				t.Fatal("partial projection rollout bypassed whole-selection preflight")
			}
			t.Log("stale and partial projection rollout refused with zero native rotations")
		}
		for _, want := range wants {
			for _, leaf := range want.credentials {
				writeProjection(paths[leaf.path], phase, values[leaf.path])
			}
		}
		deadline := time.Now().Add(4 * time.Minute)
		observed := map[string]string{}
		for {
			tick()
			state := journalState(t, a)
			if state != nil {
				for _, receipt := range state.CredentialApplications {
					if observed[receipt.Resource] != receipt.OperationID {
						observed[receipt.Resource] = receipt.OperationID
						t.Log("real API applied receipt", receipt.Resource, receipt.TargetID, "phase", phase)
					}
				}
			}
			if owner.Status.Phase == configapi.NetworkControllerConfigPhaseInSync && owner.Status.ObservedGeneration == owner.Generation {
				break
			}
			if ctx.Err() != nil || time.Now().After(deadline) {
				t.Fatal("versioned native manifest did not converge; generated identities retained", owner.Status.Conditions)
			}
			time.Sleep(500 * time.Millisecond)
		}
		state := journalState(t, a)
		if state.Operation != nil || len(state.CredentialApplications) != 6 || trace.count() != int(phase)*6 {
			t.Fatal("native credential phase did not publish exactly one write per selected type")
		}
		actual, err := c.globalCredentials(ctx)
		if err != nil {
			t.Fatal("native credential verification unavailable")
		}
		for _, want := range wants {
			got, exists := actual[want.kind.model][want.metadata.Name]
			if !exists || !globalCredentialMatches(want.metadata, got) {
				t.Fatal("generated credential metadata did not match declared type")
			}
			if old := trace.targets[want.kind.api]; old != "" && old != got.ID {
				t.Fatal("rotation replaced native credential UUID")
			}
			trace.targets[want.kind.api] = got.ID
			settled[want.kind.model] = got
		}
		for range 3 {
			tick()
		}
		if trace.count() != int(phase)*6 {
			t.Fatal("restart/no-op replayed credential mutation")
		}
		serialized, _ := json.Marshal(struct {
			Controller cvk.NetworkControllerStatus
			Config     configapi.NetworkControllerConfigStatus
		}{func() cvk.NetworkControllerStatus {
			fresh, err := a.currentController(ctx)
			if err != nil {
				t.Fatal(err)
			}
			return fresh.Status
		}(), owner.Status})
		for _, value := range allValues {
			if bytes.Contains(serialized, []byte(value)) {
				t.Fatal("credential value leaked into API status")
			}
		}
		catalog, err := c.ListSites(ctx)
		if err != nil {
			t.Fatal("native dependency site context unavailable")
		}
		var area *Site
		for _, site := range catalog {
			if site.Type == "area" && parentSitePath(site.NameHierarchy) == "Global" {
				copy := site
				area = &copy
				break
			}
		}
		if area == nil {
			t.Fatal("native root-area dependency context missing")
		}
		var doc map[string]any
		json.Unmarshal(modelBytes(), &doc)
		root := doc["catalyst_center"].(map[string]any)
		root["sites"] = map[string]any{"areas": []any{map[string]any{"name": area.Name, "parent_name": "Global"}}}
		root["inventory"] = map[string]any{"devices": []any{map[string]any{"name": "qualification-no-device", "site": area.NameHierarchy, "state": "REPROVISION"}}}
		raw, _ := json.Marshal(doc)
		consumer := *a
		consumer.intentSecretPath = "/nonexistent-consumer-projection-root"
		observers, err := consumer.wiredBulkDependencies(ctx, &configSourceSnapshot{raw: raw})
		if err != nil || len(observers) != 1 {
			t.Fatal("real API/native versioned owner receipt cannot compose with bulk")
		}
		digest, err := observers[0](ctx)
		if err != nil {
			t.Fatal("owned versioned dependency unavailable")
		}
		if current, err := observers[0](ctx); err != nil || current != digest {
			t.Fatal("unchanged versioned dependency changed identity")
		}
		if previousObserver != nil {
			if _, err := previousObserver(ctx); err == nil || previousDigest == digest {
				t.Fatal("old versioned prerequisite survived owner rotation")
			}
		}
		previousObserver, previousDigest = observers[0], digest
		t.Log("native owner phase composed with real API bulk dependency without consumer projection reads", phase)
	}
	var secrets core.SecretList
	if err := api.List(ctx, &secrets, ctrlclient.InNamespace(namespace)); err != nil || len(secrets.Items) != 0 {
		t.Fatal("qualification unexpectedly required Kubernetes Secret API values")
	}
	t.Log("six native create/rotation UUIDs and real API task/version receipts verified; source projection, stale/partial refusal, restart no replay and secret exclusion passed; no device authentication or installed workers claimed")
}
