// Copyright 2026 Cisco Systems Inc.
// SPDX-License-Identifier: Apache-2.0
package catalystcenter

import (
	"context"
	"encoding/json"
	"fmt"
	cvk "github.com/cisco/virtual-kubelet-cisco/api/v1alpha1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"net/http"
	"reflect"
	"strings"
	"testing"
	"time"
)

func switchingNativeFixture(t *testing.T, raw string) *switchingProfileNativeObservation {
	t.Helper()
	out, err := decodeSwitchingProfileNative([]byte(raw), networkProfile{ID: switchingResultUUID, Name: "A", Namespace: "switching"})
	if err != nil {
		t.Fatal("native fixture refused", err)
	}
	return out
}

const switchingNativeRaw = `{"response":{"siteProfileUuid":"4ad1552c-78b7-4c01-bb88-7a8b15e78352","name":"A","namespace":"switching","extension":{"private":"fixture-protected-value","count":9007199254740992,"values":["one","two"]},"profileAttributes":[{"key":"cli.templates","attribs":[{"key":"template.id","value":"second","extension":"keep"},{"key":"template.id","value":"first"}]},{"key":"site.settings","attribs":[{"key":"b","value":"two"},{"key":"a","value":"fixture-protected-value"}]}]}}`

func TestSwitchingNativeFullObservation(t *testing.T) {
	base := switchingNativeFixture(t, switchingNativeRaw)
	for _, mode := range []string{"same", "object-order", "template-order", "inherited-order", "unknown-private", "precision", "opaque-order", "unknown-array"} {
		t.Run(mode, func(t *testing.T) {
			raw := switchingNativeRaw
			switch mode {
			case "object-order":
				raw = strings.Replace(raw, `"name":"A","namespace":"switching"`, `"namespace":"switching","name":"A"`, 1)
			case "template-order":
				raw = strings.Replace(raw, `{"key":"template.id","value":"second","extension":"keep"},{"key":"template.id","value":"first"}`, `{"key":"template.id","value":"first"},{"key":"template.id","value":"second","extension":"keep"}`, 1)
			case "inherited-order":
				raw = strings.Replace(raw, `{"key":"b","value":"two"},{"key":"a","value":"fixture-protected-value"}`, `{"key":"a","value":"fixture-protected-value"},{"key":"b","value":"two"}`, 1)
			case "unknown-private":
				raw = strings.Replace(raw, `"private":"fixture-protected-value"`, `"private":"changed-private"`, 1)
			case "precision":
				raw = strings.Replace(raw, "9007199254740992", "9007199254740993", 1)
			case "opaque-order":
				raw = strings.Replace(raw, `["one","two"]`, `["two","one"]`, 1)
			case "unknown-array":
				raw = strings.Replace(raw, `"extension":"keep"`, `"extension":"changed"`, 1)
			}
			other := switchingNativeFixture(t, raw)
			want := mode == "same" || mode == "object-order" || mode == "template-order" || mode == "inherited-order"
			if sameSwitchingNative(base, other) != want {
				t.Fatal("complete native comparison lost fields or changed collection semantics")
			}
			for _, text := range []string{fmt.Sprint(other), fmt.Sprintf("%#v", other)} {
				if strings.Contains(text, "fixture-protected-value") || strings.Contains(text, "changed-private") || strings.Contains(text, "900719") {
					t.Fatal("private formatting leaked native values")
				}
			}
			encoded, _ := json.Marshal(other)
			if string(encoded) != "{}" {
				t.Fatal("private observation serialized native values")
			}
		})
	}
}
func TestSwitchingNativeRejectsMalformedPopulatedState(t *testing.T) {
	for _, mode := range []string{"null", "missing-attrs", "wrong-id", "wrong-name", "wrong-namespace", "duplicate-group", "duplicate-template", "unknown-key-shape", "invalid-template"} {
		t.Run(mode, func(t *testing.T) {
			raw := switchingNativeRaw
			switch mode {
			case "null":
				raw = `{"response":null}`
			case "missing-attrs":
				raw = `{"response":{"siteProfileUuid":"` + switchingResultUUID + `","name":"A","namespace":"switching"}}`
			case "wrong-id":
				raw = strings.Replace(raw, switchingResultUUID, "other", 1)
			case "wrong-name":
				raw = strings.Replace(raw, `"name":"A"`, `"name":"B"`, 1)
			case "wrong-namespace":
				raw = strings.Replace(raw, `"namespace":"switching"`, `"namespace":"wireless"`, 1)
			case "duplicate-group":
				raw = strings.Replace(raw, `"key":"site.settings"`, `"key":"cli.templates"`, 1)
			case "duplicate-template":
				raw = strings.Replace(raw, `"value":"second"`, `"value":"first"`, 1)
			case "unknown-key-shape":
				raw = strings.Replace(raw, `"key":"site.settings"`, `"key":null`, 1)
			case "invalid-template":
				raw = strings.Replace(raw, `"value":"second"`, `"value":"../bad"`, 1)
			}
			if got, err := decodeSwitchingProfileNative([]byte(raw), networkProfile{ID: switchingResultUUID, Name: "A", Namespace: "switching"}); err == nil || got != nil {
				t.Fatal("malformed observation accepted")
			}
		})
	}
}
func TestSwitchingNativeProtectedComparison(t *testing.T) {
	ctx := context.Background()
	observed := switchingNativeFixture(t, switchingNativeRaw)
	binding := "sp2-" + strings.Repeat("c", 64)
	comparator := func() *templateInputComparison {
		return &templateInputComparison{key: []byte(strings.Repeat("K", 32)), keyID: strings.Repeat("b", 64), check: func(context.Context) error { return nil }}
	}
	first, err := switchingProfileNativeMAC(ctx, comparator(), binding, observed)
	if err != nil || !validTemplateComparisonDigest(first) {
		t.Fatal("protected comparison failed", err)
	}
	for _, mode := range []string{"same", "key", "key-id", "binding", "unknown-native", "missing-key", "short-key", "invalid-key-id", "missing-check", "projection-refused", "projection-changed-during", "nil-native"} {
		t.Run(mode, func(t *testing.T) {
			c := comparator()
			b := binding
			n := observed
			refused := false
			switch mode {
			case "key":
				c.key = []byte(strings.Repeat("Z", 32))
			case "key-id":
				c.keyID = strings.Repeat("a", 64)
			case "binding":
				b = "sp2-" + strings.Repeat("a", 64)
			case "unknown-native":
				n = switchingNativeFixture(t, strings.Replace(switchingNativeRaw, `"private":"fixture-protected-value"`, `"private":"changed-private"`, 1))
			case "missing-key":
				c = nil
				refused = true
			case "short-key":
				c.key = []byte("short")
				refused = true
			case "invalid-key-id":
				c.keyID = "invalid"
				refused = true
			case "missing-check":
				c.check = nil
				refused = true
			case "projection-refused":
				c.check = func(context.Context) error { return errConfigurationSecret }
				refused = true
			case "projection-changed-during":
				calls := 0
				c.check = func(context.Context) error {
					calls++
					if calls == 2 {
						return errConfigurationSecret
					}
					return nil
				}
				refused = true
			case "nil-native":
				n = nil
				refused = true
			}
			got, err := switchingProfileNativeMAC(ctx, c, b, n)
			if refused {
				if err == nil || got != "" {
					t.Fatal("unbound key or source accepted")
				}
				return
			}
			if err != nil || !validTemplateComparisonDigest(got) || (got == first) != (mode == "same") {
				t.Fatal("protected native comparison lost domain separation", err)
			}
			if strings.Contains(got, "fixture-protected-value") {
				t.Fatal("native value exposed")
			}
		})
	}
}

// Unknown top-level native fields must survive the existing metadata observer,
// even when all managed attributes and dependency identities remain unchanged.
func TestSwitchingMetadataObservationIncludesUnknownNative(t *testing.T) {
	ctx := context.Background()
	extension := "first-private-value"
	c := testClient(t, func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case authPath:
			w.Write([]byte(`{"Token":"test"}`))
		case networkProfilesPath:
			json.NewEncoder(w).Encode(map[string]any{"response": []networkProfile{{ID: switchingResultUUID, Name: "A", Namespace: "switching"}}})
		case networkProfilesPath + "/" + switchingResultUUID:
			json.NewEncoder(w).Encode(map[string]any{"response": map[string]any{"siteProfileUuid": switchingResultUUID, "name": "A", "namespace": "switching", "profileAttributes": []any{}, "extension": extension}})
		default:
			t.Errorf("unexpected native read %s", r.URL.Path)
			w.WriteHeader(404)
		}
	})
	e, err := c.compileNetworkProfileConfiguration([]byte(`{"catalyst_center":{"network_profiles":{"switching":[{"name":"A"}]}}}`))
	if err != nil {
		t.Fatal(err)
	}
	rows, err := e.parts[0].executor.(*switchingProfileExecutor).observe(ctx)
	if err != nil {
		t.Fatal(err)
	}
	at := metav1.NewTime(time.Now().Add(-time.Second))
	op := &cvk.NetworkControllerConfigurationOperation{ID: strings.Repeat("a", 32), Section: "network_profiles", Resource: "profiles/A", Action: "Update", Phase: "Unknown", ParentID: rows[0].binding, TargetID: switchingResultUUID, TaskID: "task", SubmittedAt: &at, ConfigName: "config", ConfigUID: "owner", ConfigGeneration: 1, ControllerGeneration: 1}
	before, done, err := e.observeProfileMetadata(ctx, op)
	if err != nil || !done {
		t.Fatal("initial metadata observation unavailable", err)
	}
	extension = "changed-private-value"
	after, done, err := e.observeProfileMetadata(ctx, op)
	if err != nil || !done {
		t.Fatal("updated metadata observation unavailable", err)
	}
	if reflect.DeepEqual(before, after) {
		t.Fatal("metadata observation discarded an unknown native field")
	}
}
