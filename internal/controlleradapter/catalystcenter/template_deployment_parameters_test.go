// Copyright 2026 Cisco Systems Inc.
// SPDX-License-Identifier: Apache-2.0
package catalystcenter

import (
	"strings"
	"testing"
)

func TestTemplateDeploymentParameters(t *testing.T) {
	for _, mode := range []string{"string", "integer", "ip", "mac", "array", "empty-array", "default", "binding", "optional", "missing", "empty-required", "extra", "nil", "duplicate", "bad-type", "bad-flag", "divider", "not-param", "fraction", "bad-ip", "scoped-ip", "bad-mac", "scalar-many", "bad-default"} {
		t.Run(mode, func(t *testing.T) {
			declaration := map[string]any{"parameterName": "param", "dataType": "STRING", "required": true}
			params := map[string][]string{"param": {"secret-value"}}
			declarations := []any{declaration}
			switch mode {
			case "integer":
				declaration["dataType"] = "INTEGER"
				params["param"] = []string{"-42"}
			case "ip":
				declaration["dataType"] = "IPADDRESS"
				params["param"] = []string{"2001:db8::1"}
			case "mac":
				declaration["dataType"] = "MACADDRESS"
				params["param"] = []string{"0011.2233.4455"}
			case "array":
				declaration["paramArray"] = true
				params["param"] = []string{"a", "b"}
			case "empty-array":
				declaration["required"] = false
				declaration["paramArray"] = true
				params["param"] = []string{}
			case "default":
				delete(params, "param")
				declaration["defaultValue"] = "secret-default"
			case "binding":
				delete(params, "param")
				declaration["binding"] = "inventory-binding"
			case "optional":
				delete(params, "param")
				declaration["required"] = false
			case "missing":
				delete(params, "param")
			case "empty-required":
				params["param"] = []string{""}
			case "extra":
				params["extra"] = []string{"secret-value"}
			case "nil":
				params["param"] = nil
			case "duplicate":
				declarations = append(declarations, declaration)
			case "bad-type":
				declaration["dataType"] = "UNKNOWN"
			case "bad-flag":
				declaration["required"] = "true"
			case "divider":
				declaration["dataType"] = "SECTIONDIVIDER"
			case "not-param":
				declaration["notParam"] = true
			case "fraction":
				declaration["dataType"] = "INTEGER"
				params["param"] = []string{"1.5"}
			case "bad-ip":
				declaration["dataType"] = "IPADDRESS"
				params["param"] = []string{"hostname"}
			case "scoped-ip":
				declaration["dataType"] = "IPADDRESS"
				params["param"] = []string{"fe80::1%eth0"}
			case "bad-mac":
				declaration["dataType"] = "MACADDRESS"
				params["param"] = []string{"secret-value"}
			case "scalar-many":
				params["param"] = []string{"a", "b"}
			case "bad-default":
				declaration["defaultValue"] = 42
			}
			err := validateTemplateDeploymentParameters(map[string]any{"templateParams": declarations}, params)
			valid := mode == "string" || mode == "integer" || mode == "ip" || mode == "mac" || mode == "array" || mode == "empty-array" || mode == "default" || mode == "binding" || mode == "optional"
			if (err == nil) != valid {
				t.Fatal("unexpected validation result", mode, err)
			}
			if err != nil && strings.Contains(err.Error(), "secret") {
				t.Fatal("value leaked")
			}
		})
	}
}
