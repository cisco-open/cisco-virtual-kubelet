// Copyright 2026 Cisco Systems Inc.
// SPDX-License-Identifier: Apache-2.0
package catalystcenter

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"

	configapi "github.com/cisco/virtual-kubelet-cisco/api/config/v1alpha1"
	cvk "github.com/cisco/virtual-kubelet-cisco/api/v1alpha1"
	contract "github.com/cisco/virtual-kubelet-cisco/internal/controlleradapter"
	corev1 "k8s.io/api/core/v1"
	"sigs.k8s.io/yaml"
)

func configureSiteNativeComparisonFixture(t *testing.T, a *adapter, config *configapi.NetworkControllerConfig) (string, []byte) {
	t.Helper()
	ctx := context.Background()
	nc, err := a.currentController(ctx)
	if err != nil {
		t.Fatal(err)
	}
	nc.Spec.IntentSecretSources = []cvk.NetworkControllerIntentSecretSource{{Alias: "comparison", Name: "site-native-comparison", Key: "key"}}
	if err := a.statusWriter.Update(ctx, nc); err != nil {
		t.Fatal(err)
	}
	config.Spec.SecretRefs = []configapi.NetworkControllerSecretRef{{Section: "sites", Path: siteComparisonScope, Source: "comparison", Use: "PrivateComparisonKey"}}
	if err := a.statusWriter.Update(ctx, config); err != nil {
		t.Fatal(err)
	}
	a.intentSecretPath = t.TempDir()
	relative, err := contract.IntentSecretRelativePath(contract.IntentSecretPathInput{ConfigName: config.Name, Section: "sites", JSONPointer: siteComparisonScope, SourceAlias: "comparison", SecretName: "site-native-comparison", SecretKey: "key"})
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(a.intentSecretPath, relative)
	if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
		t.Fatal(err)
	}
	key := make([]byte, 32)
	if _, err := rand.Read(key); err != nil {
		t.Fatal(err)
	}
	encoded := []byte(hex.EncodeToString(key))
	if err := os.WriteFile(path, encoded, 0600); err != nil {
		t.Fatal(err)
	}
	return path, encoded
}

func siteCreatorForPath(records map[string]siteCreatorRecord, path string) (siteCreatorRecord, bool) {
	for resource, record := range records {
		if siteResourcePathDigest(resource) == sitePathDigest(path) {
			return record, true
		}
	}
	return siteCreatorRecord{}, false
}

func TestSitePrivateNativeMAC(t *testing.T) {
	comparison := &templateInputComparison{key: []byte(strings.Repeat("k", 32)), keyID: strings.Repeat("a", 64), check: func(context.Context) error { return nil }}
	config := &configapi.NetworkControllerConfig{}
	desired := desiredSite{kind: "area", path: "Global/Probe"}
	resource := sitePrivateCreateResource(desired, config, comparison)
	original := []byte(`{"opaque":"private-native-field","number":9007199254740993,"nested":{"x":null}}`)
	// Canonicalize using the same exact-number path as native object snapshots.
	canonical := func(raw []byte) []byte {
		value, err := decodeTemplateNativeJSON(raw)
		if err != nil {
			t.Fatal(err)
		}
		data, _ := json.Marshal(value)
		return data
	}
	expected, err := siteNativeMAC(comparison, resource, canonical(original))
	if err != nil {
		t.Fatal(err)
	}
	for _, mode := range []string{"same", "order", "unknown-edit", "number-edit", "null-absent", "other-object", "other-key", "same-key-other-scope"} {
		t.Run(mode, func(t *testing.T) {
			raw, target, cmp := original, resource, *comparison
			switch mode {
			case "order":
				raw = []byte(`{"nested":{"x":null},"number":9007199254740993,"opaque":"private-native-field"}`)
			case "unknown-edit":
				raw = []byte(`{"opaque":"changed","number":9007199254740993,"nested":{"x":null}}`)
			case "number-edit":
				raw = []byte(`{"opaque":"private-native-field","number":9007199254740992,"nested":{"x":null}}`)
			case "null-absent":
				raw = []byte(`{"opaque":"private-native-field","number":9007199254740993,"nested":{}}`)
			case "other-object":
				target = sitePrivateCreateResource(desiredSite{kind: "area", path: "Global/Elsewhere"}, config, &cmp)
			case "other-key":
				cmp.key = []byte(strings.Repeat("z", 32))
				cmp.keyID = strings.Repeat("b", 64)
			case "same-key-other-scope":
				cmp.keyID = strings.Repeat("b", 64)
				target = sitePrivateCreateResource(desired, config, &cmp)
			}
			mac, err := siteNativeMAC(&cmp, target, canonical(raw))
			if (mac == expected && err == nil) != (mode == "same" || mode == "order") {
				t.Fatal("incorrect private baseline comparison")
			}
		})
	}
}

func TestSitePrivateCreatorReceiptBoundaries(t *testing.T) {
	comparison := &templateInputComparison{key: []byte(strings.Repeat("k", 32)), keyID: strings.Repeat("a", 64), check: func(context.Context) error { return nil }}
	for _, mode := range []string{"valid", "missing-baseline", "bad-baseline", "wrong-key", "lost-producer", "lost-source", "alias-legacy"} {
		t.Run(mode, func(t *testing.T) {
			r := siteCreatorTestRecord()
			config := &configapi.NetworkControllerConfig{}
			config.Spec.Source.ConfigMapRef = &configapi.ConfigMapKeyRef{Name: "source", Key: "model"}
			r.Operation.SourceUID, r.Operation.SourceVersion = "source", "revision"
			r.Operation.Resource = sitePrivateCreateResource(desiredSite{kind: "area", path: "Global/Probe"}, config, comparison)
			r.Resource = siteCreatedResource(&r.Operation, "area", siteOwnedTestUUID, strings.Repeat("b", 64))
			switch mode {
			case "missing-baseline":
				r.Resource = strings.TrimSuffix(r.Resource, ":"+strings.Repeat("b", 64))
			case "bad-baseline":
				r.Resource = strings.TrimSuffix(r.Resource, strings.Repeat("b", 64)) + "invalid"
			case "wrong-key":
				r.Resource = strings.Replace(r.Resource, comparison.keyID, strings.Repeat("c", 64), 1)
			case "lost-producer":
				r.Operation.Resource = "Global/Probe"
			case "lost-source":
				r.Operation.SourceUID, r.Operation.SourceVersion = "", ""
			}
			state := &cvk.NetworkControllerConfigurationStatus{ActionApplications: []cvk.NetworkControllerConfigurationActionApplication{r}}
			if mode == "alias-legacy" {
				other := siteCreatorTestRecord()
				other.Operation.ID = strings.Repeat("f", 32)
				other.Resource = siteCreatedResource(&other.Operation, "area", "22222222-2222-4222-8222-222222222222")
				state.ActionApplications = append(state.ActionApplications, other)
			}
			_, err := siteCreatedRecords(state)
			if (err == nil) != (mode == "valid") {
				t.Fatal("invalid private creator receipt accepted")
			}
		})
	}
}

func TestSitePrivatePublicCreatorBaseline(t *testing.T) {
	for _, stage := range []string{"area-create", "building-create", "floor-create"} {
		for _, mode := range []string{"private-known", "private-native-edit", "private-key-change", "private-option-loss", "recover-private"} {
			t.Run(stage+"/"+mode, func(t *testing.T) { exerciseSiteSingularPublic(t, stage, mode, nil) })
		}
	}
}

func TestSitePrivateDispatchFootprint(t *testing.T) {
	for _, stage := range []string{"area-create", "building-create", "floor-create"} {
		for _, mode := range []string{"stable", "unknown-edit", "number-edit", "partial-raw", "uncertain"} {
			t.Run(stage+"/"+mode, func(t *testing.T) {
				desired, actual := siteDispatchFixture(stage)
				late, posts, lateReads := false, 0, 0
				comparison := &templateInputComparison{key: []byte(strings.Repeat("k", 32)), keyID: strings.Repeat("a", 64), check: func(context.Context) error { return nil }}
				c := testClient(t, func(w http.ResponseWriter, r *http.Request) {
					if r.URL.Path == authPath {
						fmt.Fprint(w, `{"Token":"test"}`)
						return
					}
					if r.Method != "GET" {
						posts++
						if mode == "uncertain" {
							w.WriteHeader(503)
							return
						}
						fmt.Fprint(w, `{"response":{"taskId":"site-task"}}`)
						return
					}
					if r.URL.Path != sitesPath {
						t.Error("unexpected native path")
						w.WriteHeader(500)
						return
					}
					if late {
						lateReads++
					}
					if late && mode == "partial-raw" && lateReads == 2 {
						w.WriteHeader(206)
					}
					raw, _ := json.Marshal(actual)
					var rows []map[string]any
					json.Unmarshal(raw, &rows)
					rows[0]["privateNative"] = "private-original"
					rows[0]["counter"] = json.Number("9007199254740993")
					if late && mode == "unknown-edit" {
						rows[0]["privateNative"] = "private-changed"
					}
					if late && mode == "number-edit" {
						rows[0]["counter"] = json.Number("9007199254740992")
					}
					json.NewEncoder(w).Encode(map[string]any{"response": rows})
				})
				e := &siteConfigurationExecutor{client: c, desired: desired, comparison: comparison, privateResource: func(site desiredSite) string {
					return sitePrivateCreateResource(site, &configapi.NetworkControllerConfig{}, comparison)
				}}
				write, err := e.next(context.Background())
				if err != nil || write == nil {
					t.Fatal("private initial plan failed", err)
				}
				late = true
				_, err = write.submit(context.Background())
				switch mode {
				case "stable":
					if err != nil || posts != 1 {
						t.Fatal("stable private create refused", err)
					}
				case "uncertain":
					if err == nil || posts != 1 || write.noDispatch(err) != nil {
						t.Fatal("dispatched uncertainty acquired refusal proof")
					}
				default:
					if err == nil || posts != 0 || write.noDispatch(err) == nil {
						t.Fatal("changed full native footprint dispatched")
					}
				}
				before := posts
				if _, err := write.submit(context.Background()); !errors.Is(err, errConfigurationTransition) || posts != before {
					t.Fatal("private sender replayed")
				}
			})
		}
	}
}

func TestSitePrivateDoesNotBackfillOwnership(t *testing.T) {
	for _, mode := range []string{"legacy-creator", "imported", "new-absent"} {
		t.Run(mode, func(t *testing.T) {
			desired, actual := siteDispatchFixture("area-create")
			r := siteCreatorTestRecord()
			records, err := siteCreatedRecords(&cvk.NetworkControllerConfigurationStatus{ActionApplications: []cvk.NetworkControllerConfigurationActionApplication{r}})
			if err != nil {
				t.Fatal(err)
			}
			e := &siteConfigurationExecutor{desired: desired, creators: records, creatorOwner: "owner", comparison: &templateInputComparison{key: []byte(strings.Repeat("k", 32)), keyID: strings.Repeat("a", 64), check: func(context.Context) error { return nil }}}
			if mode != "new-absent" {
				actual = append(actual, Site{ID: siteOwnedTestUUID, Name: "Probe", NameHierarchy: "Global/Probe", Type: "area", ParentID: "root"})
			}
			if mode != "legacy-creator" {
				e.creators = nil
			}
			if (e.checkPrivateBaselines(context.Background(), actual) == nil) != (mode == "new-absent") {
				t.Fatal("private baseline silently adopted/rebased existing state")
			}
		})
	}
}

func TestSitePrivateUpdateRequiresOwnedProtocol(t *testing.T) {
	desired, actual := siteDispatchFixture("building-update")
	m, err := nextSiteMutation(desired, actual)
	if err != nil || m == nil {
		t.Fatal(err)
	}
	e := &siteConfigurationExecutor{desired: desired, comparison: &templateInputComparison{}}
	if write, err := e.singularWrite(context.Background(), actual, m); !errors.Is(err, errConfigurationTransition) || write != nil {
		t.Fatal("private Update proceeded without its owned producer protocol")
	}
}

func TestSitePrivateExampleContract(t *testing.T) {
	for _, fixture := range []struct {
		name  string
		aps   int
		keyed bool
	}{{"site-private-creator-execution.yaml", 0, false}, {"planned-ap-private-state.yaml", 1, true}, {"planned-ap-type-update.yaml", 1, true}, {"planned-ap-floor-handoff.yaml", 1, true}} {
		t.Run(fixture.name, func(t *testing.T) {
			raw, err := os.ReadFile("../../../examples/configs/catalyst-center/" + fixture.name)
			if err != nil {
				t.Fatal(err)
			}
			documents := bytes.Split(raw, []byte("\n---\n"))
			if len(documents) != 2 {
				t.Fatal("expected model and execution manifest")
			}
			var cm corev1.ConfigMap
			var config configapi.NetworkControllerConfig
			if err := yaml.UnmarshalStrict(documents[0], &cm); err != nil {
				t.Fatal(err)
			}
			if err := yaml.UnmarshalStrict(documents[1], &config); err != nil {
				t.Fatal(err)
			}
			if !supportedConfigurationApply(&config) || !siteComparisonConfig(&config) || config.Spec.Source.ConfigMapRef.Name != cm.Name {
				t.Fatal("private site example contract invalid")
			}
			desired, placements, images, err := parseSitesWithFloorImages([]byte(cm.Data[config.Spec.Source.ConfigMapRef.Key]), nil)
			if err != nil || len(desired) != 3 || len(placements) != fixture.aps || len(images) != 0 {
				t.Fatal("private site example model invalid", err)
			}
			if err := validateSiteExecution(desired); err != nil {
				t.Fatal(err)
			}
			if (config.Annotations[plannedAPBaselineAnnotation] == "true") != fixture.keyed {
				t.Fatal("keyed AP example opt-in missing or enabled implicitly")
			}
			if (config.Annotations[siteFloorHandoffAnnotation] == "true") != (fixture.name == "planned-ap-floor-handoff.yaml") {
				t.Fatal("floor handoff example opt-in missing or enabled implicitly")
			}
		})
	}
}
func TestSitePrivateFootprintSeparatesParentSettings(t *testing.T) {
	_, actual := siteDispatchFixture("area-create")
	actual = append(actual, Site{ID: siteOwnedTestUUID, ParentID: "root", Name: "Probe", NameHierarchy: "Global/Probe", Type: "area"})
	parentValue := "private-parent-before"
	c := testClient(t, func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == authPath {
			fmt.Fprint(w, `{"Token":"test"}`)
			return
		}
		raw, _ := json.Marshal(actual)
		var rows []map[string]any
		json.Unmarshal(raw, &rows)
		for _, row := range rows {
			if row["nameHierarchy"] == "Global" {
				row["opaqueParent"] = parentValue
			}
		}
		json.NewEncoder(w).Encode(map[string]any{"response": rows})
	})
	baseline, err := c.siteNativeFootprint(context.Background(), actual, "Global/Probe", false)
	if err != nil {
		t.Fatal(err)
	}
	dispatch, err := c.siteNativeFootprint(context.Background(), actual, "Global/Probe", true)
	if err != nil {
		t.Fatal(err)
	}
	parentValue = "private-parent-after"
	again, err := c.siteNativeFootprint(context.Background(), actual, "Global/Probe", false)
	if err != nil {
		t.Fatal(err)
	}
	fresh, err := c.siteNativeFootprint(context.Background(), actual, "Global/Probe", true)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(baseline, again) || bytes.Equal(dispatch, fresh) {
		t.Fatal("parent settings were silently rebased or excluded from pre-dispatch checks")
	}
}
