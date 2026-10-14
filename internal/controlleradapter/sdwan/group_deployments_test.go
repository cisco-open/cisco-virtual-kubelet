// Copyright 2026 Cisco Systems Inc.
// SPDX-License-Identifier: Apache-2.0
package sdwan

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"reflect"
	"strings"
	"testing"
)

func TestGroupDeploymentModel(t *testing.T) {
	raw, err := os.ReadFile("../../../examples/configs/sdwan/group-device-deployment.yaml")
	if err != nil {
		t.Fatal(err)
	}
	resources, err := Compile(raw)
	if err != nil {
		t.Fatal(err)
	}
	var deployments []Resource
	for _, resource := range resources {
		if resource.Kind == "group-deployment" {
			deployments = append(deployments, resource)
		}
	}
	if len(deployments) != 2 {
		t.Fatal("expected configuration and policy group deployments")
	}
	for _, deployment := range deployments {
		if len(deployment.GroupDevices) != 1 || !deployment.GroupDevices[0].Deploy {
			t.Fatal("default deployment target missing")
		}
		if deployment.Type == "configuration_group" && deployment.GroupDevices[0].TopologyLabel != "cvk-lab" {
			t.Fatal("topology label missing")
		}
		if _, ok := deployment.GroupDevices[0].Variables["cvkAddressList"].([]any); deployment.Type == "configuration_group" && !ok {
			t.Fatal("configuration group list variable missing")
		}
		if _, ok := deployment.GroupDevices[0].Variables["cvkPolicyVariable"].([]any); deployment.Type == "policy_group" && !ok {
			t.Fatal("policy group list variable missing")
		}
	}
	withoutDeploy := strings.Replace(string(raw), "topology_label: cvk-lab", "topology_label: cvk-lab\n          configuration_group_deploy: false", 1)
	resolved, err := Compile([]byte(withoutDeploy))
	if err != nil {
		t.Fatal(err)
	}
	for _, resource := range resolved {
		if resource.Kind == "group-deployment" && resource.Type == "configuration_group" && resource.GroupDevices[0].Deploy {
			t.Fatal("explicit no-deploy ignored")
		}
	}
	missing := strings.Replace(string(raw), "configuration_group: CVK-GROUP-DEPLOY-CONFIG", "configuration_group: MISSING", 1)
	if _, err := Compile([]byte(missing)); err == nil {
		t.Fatal("undeclared group accepted")
	}
}

func TestGroupDeploymentLifecycle(t *testing.T) {
	for _, kind := range []string{"configuration_group", "policy_group"} {
		t.Run(kind, func(t *testing.T) {
			collection := "/dataservice/v1/config-group"
			if kind == "policy_group" {
				collection = "/dataservice/v1/policy-group"
			}
			associated, label, variable, deployments := false, "", "", 0
			c := testClient(t, func(w http.ResponseWriter, request *http.Request) {
				if auth(w, request) {
					return
				}
				w.Header().Set("Content-Type", "application/json")
				switch request.URL.Path {
				case collection:
					fmt.Fprint(w, `[{"id":"group1","name":"fixture"}]`)
				case collection + "/group1/device/associate/":
					switch request.Method {
					case "GET":
						if associated {
							fmt.Fprintf(w, `{"devices":[{"id":"router1","groupTopologyLabel":%q,"configGroupUpToDate":"True","policyGroupUpToDate":"True"}]}`, label)
						} else {
							fmt.Fprint(w, `{"devices":[]}`)
						}
					case "POST", "PUT":
						var body struct {
							Devices []map[string]any `json:"devices"`
						}
						if err := json.NewDecoder(request.Body).Decode(&body); err != nil || len(body.Devices) != 1 || body.Devices[0]["id"] != "router1" {
							t.Error("invalid association", err)
						}
						label, _ = body.Devices[0]["groupTopologyLabel"].(string)
						associated = true
						fmt.Fprint(w, `{}`)
					}
				case collection + "/group1/device/variables/":
					if request.Method == "GET" {
						fmt.Fprintf(w, `{"devices":[{"device-id":"router1","variables":[{"name":"cvkVariable","value":%q}]}]}`, variable)
						return
					}
					var body struct {
						Devices []struct {
							ID        string `json:"device-id"`
							Variables []struct {
								Name  string `json:"name"`
								Value string `json:"value"`
							} `json:"variables"`
						} `json:"devices"`
					}
					if err := json.NewDecoder(request.Body).Decode(&body); err != nil || len(body.Devices) != 1 || len(body.Devices[0].Variables) != 1 || body.Devices[0].Variables[0].Name != "cvkVariable" {
						t.Error("invalid variable payload", err)
					}
					variable = body.Devices[0].Variables[0].Value
					fmt.Fprint(w, `{}`)
				case collection + "/group1/device/variables/schema":
					fmt.Fprint(w, `[]`)
				case collection + "/group1/device/deploy/":
					deployments++
					fmt.Fprint(w, `{"parentTaskId":"task4"}`)
				case "/dataservice/device/action/status/task4":
					fmt.Fprint(w, `{"summary":{"status":"done"},"validation":{"status":"success"},"data":[]}`)
				default:
					t.Errorf("unexpected group path %s", request.URL.Path)
				}
			})
			r := Resource{Name: "fixture", Kind: "group-deployment", Type: kind, Collection: collection, GroupDevices: []GroupDeviceTarget{{ID: "router1", TopologyLabel: "east", Deploy: true, Variables: map[string]any{"cvkVariable": "one"}}}}
			ctx := context.Background()
			if o, err := c.Observe(ctx, r); err != nil || o != nil {
				t.Fatal("unassociated observation", err)
			}
			if id, err := c.Create(ctx, r); err != nil || id != "group1" || deployments != 1 {
				t.Fatal("association and deployment", id, err)
			}
			o, err := c.Observe(ctx, r)
			if err != nil || o == nil {
				t.Fatal("associated observation", err)
			}
			same, err := Equal(r, o.Body)
			if err != nil || !same {
				t.Fatal("association readback", err)
			}
			r.GroupDevices[0].Variables["cvkVariable"] = "two"
			if err := c.Update(ctx, r, o); err != nil || variable != "two" || deployments != 2 {
				t.Fatal("variable update", err)
			}
			if kind == "configuration_group" {
				r.GroupDevices[0].TopologyLabel = "west"
				o, _ = c.Observe(ctx, r)
				if err := c.Update(ctx, r, o); err != nil || label != "west" {
					t.Fatal("topology label update", err)
				}
			}
		})
	}
}

func TestGroupVariableValue(t *testing.T) {
	for _, sample := range []struct {
		value any
		kind  string
		want  any
	}{
		{"42", "integer", 42}, {"true", "boolean", true}, {"1.5", "number", 1.5}, {[]any{"1", "2"}, "integer", []any{1, 2}}, {"001", "string", "001"},
	} {
		got, err := groupVariableValue(sample.value, sample.kind)
		if err != nil || !reflect.DeepEqual(got, sample.want) {
			t.Fatalf("group variable %v (%s): %v, %v", sample.value, sample.kind, got, err)
		}
	}
}

func TestGroupVariableSchema(t *testing.T) {
	c := testClient(t, func(w http.ResponseWriter, request *http.Request) {
		if auth(w, request) {
			return
		}
		if request.URL.Path != "/dataservice/v1/config-group/group1/device/variables/schema" {
			t.Errorf("unexpected schema path %s", request.URL.Path)
		}
		fmt.Fprint(w, `[{"variables":[{"schema":{"properties":{"region-id":{"properties":{"value":{"type":"integer"}}},"cvkList":{"properties":{"value":{"type":"array","items":{"type":"boolean"}}}}}}}]}]`)
	})
	types := c.groupVariableTypes(context.Background(), "/dataservice/v1/config-group/group1/device/")
	if types["region-id"] != "integer" || types["cvkList"] != "boolean" {
		t.Fatal("group variable schema types missing", types)
	}
}
