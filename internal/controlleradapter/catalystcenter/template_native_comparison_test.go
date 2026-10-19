// Copyright 2026 Cisco Systems Inc.
// SPDX-License-Identifier: Apache-2.0
package catalystcenter

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
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
	adaptercontract "github.com/cisco/virtual-kubelet-cisco/internal/controlleradapter"
)

// The same approved mount layout as manager/worker projections. No Secret API
// object is created or read. Configure before an envtest installer pins API UIDs.
func configureTemplateNativeComparisonFixture(t *testing.T, a *adapter, config *configapi.NetworkControllerConfig) (string, []byte) {
	t.Helper()
	ctx := context.Background()
	nc, err := a.currentController(ctx)
	if err != nil {
		t.Fatal(err)
	}
	nc.Spec.IntentSecretSources = []cvk.NetworkControllerIntentSecretSource{{Alias: "comparison", Name: "template-native-comparison", Key: "key"}}
	if err := a.statusWriter.Update(ctx, nc); err != nil {
		t.Fatal(err)
	}
	config.Spec.SecretRefs = []configapi.NetworkControllerSecretRef{{Section: "templates", Path: templateNativeComparisonScope, Source: "comparison", Use: "PrivateComparisonKey"}}
	a.intentSecretPath = t.TempDir()
	relative, err := adaptercontract.IntentSecretRelativePath(adaptercontract.IntentSecretPathInput{ConfigName: config.Name, Section: "templates", JSONPointer: templateNativeComparisonScope, SourceAlias: "comparison", SecretName: "template-native-comparison", SecretKey: "key"})
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(a.intentSecretPath, relative)
	if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
		t.Fatal(err)
	}
	key := []byte(strings.Repeat("ab", 32))
	if err := os.WriteFile(path, key, 0600); err != nil {
		t.Fatal(err)
	}
	return path, key
}

func TestTemplateNativeComparisonMAC(t *testing.T) {
	comparison := &templateInputComparison{key: []byte(strings.Repeat("k", 32)), keyID: strings.Repeat("a", 64)}
	for _, kind := range []string{"p", "r", "c"} {
		t.Run(kind, func(t *testing.T) {
			receipt := templateCreatorReceiptFixture(kind)
			receipt.Operation.Resource += ":" + templateComparisonTag + ":" + comparison.keyID
			original := map[string]any{"id": templateOwnedTestUUID, "name": "base", "privateBody": "private-synthetic-content", "unknown": map[string]any{"secretParam": "private-synthetic-param"}, "references": []any{"one", "two"}}
			mac, err := templateNativeMAC(comparison, receipt.Operation.Resource, original)
			if err != nil {
				t.Fatal(err)
			}
			receipt.Resource = templateCreatorResultResource(receipt.Operation.Resource, templateOwnedTestUUID, mac)
			for _, mode := range []string{"match", "field-order", "private-edit", "unknown-edit", "reference-edit", "key-rotation", "wrong-object", "corrupt-mac", "missing-mac", "wrong-key-marker", "missing-operation-marker"} {
				t.Run(mode, func(t *testing.T) {
					raw, _ := json.Marshal(original)
					var current map[string]any
					json.Unmarshal(raw, &current)
					r := receipt
					cmp := *comparison
					switch mode {
					case "field-order":
						current = map[string]any{"references": []any{"one", "two"}, "unknown": map[string]any{"secretParam": "private-synthetic-param"}, "privateBody": "private-synthetic-content", "name": "base", "id": templateOwnedTestUUID}
					case "private-edit":
						current["privateBody"] = "edited"
					case "unknown-edit":
						current["unknown"].(map[string]any)["secretParam"] = "edited"
					case "reference-edit":
						current["references"] = []any{"two", "one"}
					case "key-rotation":
						cmp.keyID = strings.Repeat("b", 64)
						cmp.key = []byte(strings.Repeat("z", 32))
					case "wrong-object":
						r.Operation.Resource = templateCreatorResource(kind, "Elsewhere", "other") + ":c:" + templateComparisonTag + ":" + comparison.keyID
					case "corrupt-mac":
						r.Resource = r.Resource[:len(r.Resource)-64] + strings.Repeat("f", 64)
					case "missing-mac":
						r.Resource = strings.Split(r.Resource, ":"+templateComparisonTag+":")[0]
					case "wrong-key-marker":
						r.Resource = strings.Replace(r.Resource, comparison.keyID, strings.Repeat("c", 64), 1)
					case "missing-operation-marker":
						r.Operation.Resource = strings.Split(r.Operation.Resource, ":"+templateComparisonTag+":")[0]
					}
					matched, err := matchTemplateCreatorBaseline(&cmp, r, current)
					want := mode == "match" || mode == "field-order"
					if matched != want || want && err != nil {
						t.Fatal("incorrect private baseline comparison", matched, err)
					}
				})
			}
			encoded, _ := json.Marshal(receipt)
			plain := string(encoded)
			for _, value := range []string{"private-synthetic-content", "private-synthetic-param", strings.Repeat("k", 32)} {
				if strings.Contains(plain, value) {
					t.Fatal("private data entered receipt")
				}
				sum := sha256.Sum256([]byte(value))
				if strings.Contains(plain, hex.EncodeToString(sum[:])) {
					t.Fatal("unkeyed private-value hash entered receipt")
				}
			}
			if strings.Contains(fmt.Sprintf("%v %#v", comparison, comparison), strings.Repeat("k", 32)) {
				t.Fatal("comparison key exposed")
			}
		})
	}
}

var templateBaselineModes = []string{"known", "missing-key", "malformed-key", "key-change-during-proof", "key-change-restore", "native-state-race", "wrong-declared-body", "baseline-pruned", "baseline-wrong-key"}

func TestTemplateCreatorPrivateBaseline(t *testing.T) {
	for _, stage := range []string{"project-create", "draft-create", "composite-draft"} {
		for _, mode := range templateBaselineModes {
			t.Run(stage+"/"+mode, func(t *testing.T) { exerciseTemplateNoDispatchReplan(t, stage, "baseline-"+mode, nil) })
		}
	}
}

func TestTemplateNativeComparisonCompiler(t *testing.T) {
	ctx := context.Background()
	a, config := journalFixture(t)
	config.Spec.ManagedSections = []string{"templates"}
	config.Spec.Source.Inline.Raw = []byte(projectModel)
	path, keyText := configureTemplateNativeComparisonFixture(t, a, config)
	if err := a.statusWriter.Update(ctx, config); err != nil {
		t.Fatal(err)
	}
	nc, err := a.currentController(ctx)
	if err != nil {
		t.Fatal(err)
	}
	source, err := loadControllerSource(ctx, a.statusReader, config)
	if err != nil {
		t.Fatal(err)
	}
	if !supportedConfigurationApply(config) {
		t.Fatal("definition comparison configuration refused")
	}
	plan, err := a.compilePrivateTemplateDefinitions(nc, config, source)
	if err != nil || plan.executor == nil || source.secrets == nil || source.templateComparison == nil {
		t.Fatal("private compiler did not retain the approved projection", err)
	}
	key, err := hex.DecodeString(string(keyText))
	if err != nil {
		t.Fatal(err)
	}
	expected := func(domain string) string {
		m := hmac.New(sha256.New, key)
		m.Write([]byte(domain + "\x00" + string(nc.UID) + "\x00" + string(config.UID) + "\x00" + nc.Spec.Endpoint + "\x00"))
		return hex.EncodeToString(m.Sum(nil))
	}
	if source.templateComparison.keyID != expected("cvk-template-native-key-v1") {
		t.Fatal("native key identity escaped its owner/controller/origin domain")
	}
	legacy, err := a.resolveTemplateComparison(nc, config, source, "templates", templateNativeComparisonScope, "cvk-template-key-v1")
	if err != nil || legacy.keyID != expected("cvk-template-key-v1") || legacy.keyID == source.templateComparison.keyID {
		t.Fatal("existing inventory domain changed or native scope borrowed its identity", err)
	}
	for _, mode := range []string{"value-use", "inventory-pointer", "wrong-section", "combined", "duplicate", "prune", "delete"} {
		t.Run(mode, func(t *testing.T) {
			copy := config.DeepCopy()
			switch mode {
			case "value-use":
				copy.Spec.SecretRefs[0].Use = "Value"
			case "inventory-pointer":
				copy.Spec.SecretRefs[0].Path = templateComparisonScope
			case "wrong-section":
				copy.Spec.SecretRefs[0].Section = "inventory"
			case "combined":
				copy.Spec.ManagedSections = []string{"templates", "inventory"}
			case "duplicate":
				copy.Spec.SecretRefs = append(copy.Spec.SecretRefs, copy.Spec.SecretRefs[0])
			case "prune":
				copy.Spec.PrunePolicy = configapi.NetworkControllerRetentionPolicyDelete
			case "delete":
				copy.Spec.DeletionPolicy = configapi.NetworkControllerRetentionPolicyDelete
			}
			if supportedConfigurationApply(copy) {
				t.Fatal("unsupported comparison or deletion policy acquired execution")
			}
		})
	}
	if err := os.Remove(path); err != nil {
		t.Fatal(err)
	}
	invalid := &configSourceSnapshot{raw: []byte(`{"catalyst_center":{"templates":{"unsupported":true}}}`)}
	_, err = a.compilePrivateTemplateDefinitions(nc, config, invalid)
	if err == nil || errors.Is(err, errConfigurationSecret) || invalid.secrets != nil {
		t.Fatal("invalid model reached key resolution", err)
	}
}

func TestTemplateEmptyRetainIntent(t *testing.T) {
	c := testClient(t, func(_ http.ResponseWriter, _ *http.Request) {
		t.Error("empty retained template execution accessed native APIs")
	})
	for _, raw := range []string{`{"catalyst_center":{"templates":{"projects":[]}}}`, `{"catalyst_center":{"templates":{"projects":[],"tags":[]}}}`} {
		plan, err := c.compileConfigurationApply([]byte(raw), []string{"templates"}, nil)
		if err != nil || plan.executor == nil || plan.executor.count() != 0 {
			t.Fatal("empty retained intent did not compile to a no-op", err)
		}
		write, err := plan.executor.next(context.Background())
		if err != nil || write != nil {
			t.Fatal("empty intent became an effect", err)
		}
	}
}
