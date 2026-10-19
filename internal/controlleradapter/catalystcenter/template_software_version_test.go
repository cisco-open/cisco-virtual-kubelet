// Copyright 2026 Cisco Systems Inc.
// SPDX-License-Identifier: Apache-2.0
package catalystcenter

import (
	"context"
	"encoding/json"
	"fmt"
	configapi "github.com/cisco/virtual-kubelet-cisco/api/config/v1alpha1"
	"github.com/cisco/virtual-kubelet-cisco/internal/controlleradapter/catalystcenter/nacmodel"
	core "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"net/http"
	ctrl "sigs.k8s.io/controller-runtime"
	ctrlclient "sigs.k8s.io/controller-runtime/pkg/client"
	"strings"
	"testing"
)

func TestTemplateSoftwareVersionFallback(t *testing.T) {
	for _, mode := range []string{"inherit", "override", "null-override", "empty-override", "null-fallback", "empty-fallback", "omitted", "bad-fallback", "bad-overridden-fallback", "bad-override"} {
		t.Run(mode, func(t *testing.T) {
			definition := func(name string) map[string]any {
				return map[string]any{"name": name, "language": "JINJA", "software_type": "IOS-XE", "device_types": []any{map[string]any{"product_family": "Switches and Hubs"}}}
			}
			boot, base, combined := definition("boot"), definition("base"), definition("combined")
			combined["composite"] = true
			combined["containing_templates"] = []string{"base"}
			section := map[string]any{"software_version": "17.18.3", "projects": []any{map[string]any{"name": "Lab", "onboarding_templates": []any{boot}, "dayn_templates": []any{base, combined}}}}
			wantBase, wantOther := any("17.18.3"), any("17.18.3")
			valid := true
			switch mode {
			case "override":
				base["software_version"] = "26.2.1"
				wantBase = "26.2.1"
			case "null-override":
				base["software_version"] = nil
				wantBase = nil
			case "empty-override":
				base["software_version"] = ""
				wantBase = ""
			case "null-fallback":
				section["software_version"] = nil
				wantBase, wantOther = nil, nil
			case "empty-fallback":
				section["software_version"] = ""
				wantBase, wantOther = "", ""
			case "omitted":
				delete(section, "software_version")
				wantBase, wantOther = nil, nil
			case "bad-fallback":
				section["software_version"] = 17
				valid = false
			case "bad-overridden-fallback":
				section["software_version"] = false
				boot["software_version"] = "17.18.3"
				base["software_version"] = "17.18.3"
				combined["software_version"] = "17.18.3"
				valid = false
			case "bad-override":
				base["software_version"] = []any{"17.18.3"}
				valid = false
			}
			raw, _ := json.Marshal(map[string]any{"catalyst_center": map[string]any{"templates": section}})
			intent, err := parseTemplateIntent(raw, map[string]string{"base.j2": "! base\n", "boot.j2": "! boot\n"})
			if !valid {
				if err == nil {
					t.Fatal("invalid version attribute escaped whole-model preflight")
				}
				return
			}
			if err != nil || len(intent.definitions) != 3 {
				t.Fatal("fallback model rejected", err)
			}
			for _, d := range intent.definitions {
				want := wantOther
				if d.identity.Name == "base" {
					want = wantBase
				}
				got, present := d.payload["softwareVersion"]
				if got != want || present != (want != nil) {
					t.Fatal("incorrect module try precedence", d.identity.Name)
				}
			}
		})
	}
}

func TestTemplateSoftwareVersionPublicNoOp(t *testing.T) {
	for _, mode := range []string{"inherited", "cleared", "invalid"} {
		t.Run(mode, func(t *testing.T) { exerciseTemplateMetadataPublic(t, mode, nil) })
	}
}

func exerciseTemplateMetadataPublic(t *testing.T, mode string, wire func(*adapter, *configapi.NetworkControllerConfig)) {
	t.Helper()
	ctx := context.Background()
	a, config := journalFixture(t)
	version := "17.18.3"
	if mode == "cleared" {
		version = ""
	}
	field := fmt.Sprintf("%q", version)
	if mode == "invalid" {
		field = "false"
	}
	raw := strings.Replace(templateModel, "  templates:\n", "  templates:\n    software_version: "+field+"\n", 1)
	switch mode {
	case "nullable-definition":
		raw = strings.Replace(raw, "- name: Lab", "- name: Lab\n        description: null", 1)
		raw = strings.Replace(raw, "- name: base", "- name: base\n            description: null\n            tags: null", 1)
		raw = strings.Replace(raw, "product_family: Switches and Hubs", "product_family: Switches and Hubs, product_series: null, product_type: null", 1)
	case "nullable-variable":
		raw = strings.Replace(raw, "required: true", "binding: null\n                default_value: null\n                additional_info: null\n                field_name: null\n                hint_text: null\n                not_param: null\n                param_array: null\n                required: true", 1)
	case "scalar-default":
		raw = strings.Replace(raw, "required: true", "default_value: 7\n                required: true", 1)
		raw = strings.Replace(raw, "data_values: {first: value}", "data_values: {first: 7}", 1)
	case "cleared-variable":
		raw = strings.Replace(raw, "required: true", "default_value: \"\"\n                required: true", 1)
		raw = strings.Replace(raw, "data_values: {first: value}", "data_values: {}", 1)
	case "parameter-types":
		// All five enums are accepted by the model; native divider support is separate.
		raw += `
              - name: counter
                data_type: INTEGER
                default_value: 7
                required: false
              - name: address
                data_type: IPADDRESS
                default_value: 198.51.100.1
                binding: ""
                additional_info: CVK qualification
                field_name: Address
                hint_text: CVK qualification
                param_array: true
              - name: mac
                data_type: MACADDRESS
                default_value: "00:11:22:33:44:55"
              - name: heading
                data_type: SECTIONDIVIDER
                not_param: true
`
	case "invalid-variable":
		raw = strings.Replace(raw, "data_type: STRING", "data_type: BOOLEAN", 1)
	case "runtime-binding":
		raw = strings.Replace(raw, "required: true", "required: true\n                param_array: true\n                binding: "+fmt.Sprintf("%q", templateCommonSettingsBinding(t, "RADIUS")), 1)
		raw = strings.Replace(raw, "                data_values: {first: value}\n", "", 1)
	case "invalid-selection":
		raw = strings.Replace(raw, "selection_type: SINGLE_SELECT", "selection_type: UNKNOWN", 1)
	}

	config.Spec.ManagedSections = []string{"templates"}
	config.Spec.Source.Inline = nil
	config.Spec.Source.ConfigMapRef = &configapi.ConfigMapKeyRef{Name: "version-model", Key: "model.yaml"}
	if err := a.statusWriter.Update(ctx, config); err != nil {
		t.Fatal(err)
	}
	if wire != nil {
		wire(a, config)
	}
	assets, _ := json.Marshal(map[string]string{"base.j2": "hostname {{ hostname }}\n"})
	model := &core.ConfigMap{ObjectMeta: metav1.ObjectMeta{Name: "version-model", Namespace: config.Namespace}, Data: map[string]string{"model.yaml": raw, nacmodel.TemplateAssetsKey: string(assets)}}
	if wire == nil {
		model.UID = "template-version-source"
	}
	if err := a.statusWriter.Create(ctx, model); err != nil {
		t.Fatal(err)
	}
	// Seed an existing unchanged resource. No Create/ownership is inferred by
	// these public Retain no-op observations.
	seedRaw := strings.Replace(templateModel, "  templates:\n", "  templates:\n    software_version: "+fmt.Sprintf("%q", version)+"\n", 1)
	if mode == "nullable-variable" || mode == "scalar-default" || mode == "nullable-definition" || mode == "cleared-variable" || mode == "parameter-types" || mode == "runtime-binding" {
		seedRaw = raw
	}
	intent, err := parseTemplateIntent([]byte(seedRaw), map[string]string{"base.j2": "hostname {{ hostname }}\n"})
	if err != nil {
		t.Fatal(err)
	}
	projectID := "11111111-1111-4111-8111-111111111111"
	id := "22222222-2222-4222-8222-222222222222"
	committedID := "44444444-4444-4444-8444-000000000001"
	reads := 0
	a.client = testClient(t, func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == authPath {
			fmt.Fprint(w, `{"Token":"test"}`)
			return
		}
		if r.Method != "GET" {
			t.Error("public no-op performed a mutation")
			w.WriteHeader(400)
			return
		}
		reads++
		switch r.URL.Path {
		case templateProjectsPath:
			json.NewEncoder(w).Encode([]any{map[string]any{"id": projectID, "name": "Lab"}})
		case templatesPath:
			json.NewEncoder(w).Encode([]any{map[string]any{"templateId": id, "projectId": projectID, "name": "base"}})
		case templateVersionsPath + "/" + id:
			json.NewEncoder(w).Encode([]any{map[string]any{"templateId": id, "versionsInfo": []any{map[string]any{"id": id}, map[string]any{"id": committedID, "version": "1"}}}})
		case templatesPath + "/" + id, templatesPath + "/" + committedID:
			encoded, _ := json.Marshal(intent.definitions[0].payload)
			body := map[string]any{}
			json.Unmarshal(encoded, &body)
			if mode == "runtime-binding" {
				// Clone first: native additions cannot edit the desired fixture.
				param := body["templateParams"].([]any)[0].(map[string]any)
				param["selection"].(map[string]any)["selectionValues"] = map[string]any{}
			}
			body["id"] = id
			body["projectId"] = projectID
			if r.URL.Path == templatesPath+"/"+committedID {
				body["id"] = committedID
				body["version"] = "1"
			}
			if mode == "cleared" {
				delete(body, "softwareVersion")
			}
			json.NewEncoder(w).Encode(body)
		default:
			t.Error("unexpected native route", r.URL.Path)
			w.WriteHeader(404)
		}
	})
	key := ctrlclient.ObjectKeyFromObject(config)
	for i := 0; i < 3; i++ {
		restarted := *a
		if _, err := (&configReconciler{a: &restarted}).Reconcile(ctx, ctrl.Request{NamespacedName: key}); err != nil {
			t.Fatal(err)
		}
		if err := a.statusReader.Get(ctx, key, config); err != nil {
			t.Fatal(err)
		}
	}
	if mode == "invalid" || mode == "invalid-variable" || mode == "invalid-selection" {
		if config.Status.Phase != configapi.NetworkControllerConfigPhaseFailed || reads != 0 {
			t.Fatal("invalid fallback reached native observation or escaped preflight")
		}
		return
	}
	if config.Status.Phase != configapi.NetworkControllerConfigPhaseInSync || reads == 0 {
		t.Fatal("public fallback no-op did not converge", config.Status.Phase)
	}
	nc, err := a.currentController(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if nc.Status.Configuration != nil && (nc.Status.Configuration.Operation != nil || len(nc.Status.Configuration.ActionApplications) != 0) {
		t.Fatal("unchanged unowned template acquired a mutation or ownership receipt")
	}
	if mode == "runtime-binding" {
		for _, status := range []any{config.Status, nc.Status} {
			encoded, _ := json.Marshal(status)
			if strings.Contains(string(encoded), "__aaaendpointserver") {
				t.Fatal("opaque binding data escaped into status")
			}
		}
	}
}

func TestTemplateSoftwareVersionClearComparison(t *testing.T) {
	for _, test := range []struct {
		name           string
		got            any
		present, match bool
	}{
		{"omitted", nil, false, true}, {"null", nil, true, true}, {"empty", "", true, true},
		{"constrained", "17.18.3", true, false}, {"malformed-number", 0, true, false}, {"malformed-list", []any{}, true, false},
	} {
		t.Run(test.name, func(t *testing.T) {
			got := map[string]any{}
			if test.present {
				got["softwareVersion"] = test.got
			}
			if templateDefinitionMatches(map[string]any{"softwareVersion": ""}, got) != test.match {
				t.Fatal("explicit clear has incorrect drift semantics")
			}
			if templateDefinitionMatches(map[string]any{"softwareVersion": "26.2.1"}, got) {
				t.Fatal("nonempty constraint matched unconstrained/foreign native state")
			}
		})
	}
}

func TestTemplateSoftwareVersionReportAndApply(t *testing.T) {
	for _, mode := range []string{"same", "changed", "cleared"} {
		t.Run(mode, func(t *testing.T) {
			version := "17.18.3"
			if mode == "cleared" {
				version = ""
			}
			raw := strings.Replace(templateModel, "  templates:\n", "  templates:\n    software_version: "+fmt.Sprintf("%q", version)+"\n", 1)
			files := map[string]string{"base.j2": "hostname {{ hostname }}\n"}
			intent, err := parseTemplateIntent([]byte(raw), files)
			if err != nil {
				t.Fatal(err)
			}
			c := testClient(t, func(w http.ResponseWriter, r *http.Request) {
				if r.URL.Path == authPath {
					fmt.Fprint(w, `{"Token":"test"}`)
					return
				}
				if r.Method != "GET" {
					t.Error("observation/planning performed a write")
					w.WriteHeader(400)
					return
				}
				switch r.URL.Path {
				case templateProjectsPath:
					fmt.Fprint(w, `[{"id":"project","name":"Lab"}]`)
				case templatesPath:
					fmt.Fprint(w, `[{"templateId":"template","projectId":"project","name":"base"}]`)
				case templateVersionsPath + "/template":
					fmt.Fprint(w, `[{"templateId":"template","versionsInfo":[{"id":"template"},{"id":"committed","version":"1"}]}]`)
				case templatesPath + "/template", templatesPath + "/committed":
					body := map[string]any{}
					for key, value := range intent.definitions[0].payload {
						body[key] = value
					}
					body["id"] = "template"
					body["projectId"] = "project"
					if r.URL.Path == templatesPath+"/committed" {
						body["id"] = "committed"
						body["version"] = "1"
					}
					if mode == "changed" {
						body["softwareVersion"] = "17.9.1"
					}
					if mode == "cleared" {
						delete(body, "softwareVersion")
					}
					json.NewEncoder(w).Encode(body)
				default:
					w.WriteHeader(404)
				}
			})
			reports, err := c.compileConfigReports([]byte(raw), []string{"templates"}, files)
			if err != nil || len(reports) != 1 {
				t.Fatal("public report compilation rejected fallback", err)
			}
			drift, err := reports[0].observe(context.Background())
			if err != nil {
				t.Fatal(err)
			}
			want := 0
			if mode == "changed" {
				want = 1
			}
			if len(drift) != want {
				t.Fatal("fallback/clear drift incorrect", len(drift))
			}
			ex, err := c.compileConfigurationExecutor([]byte(raw), "templates", files)
			if err != nil {
				t.Fatal("apply compilation rejected fallback", err)
			}
			write, err := ex.next(context.Background())
			if err != nil || (write != nil) != (mode == "changed") {
				t.Fatal("apply planning did not honor inherited constraint", err)
			}
		})
	}
}
