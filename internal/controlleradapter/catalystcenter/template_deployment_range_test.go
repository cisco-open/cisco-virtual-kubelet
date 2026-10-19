// Copyright 2026 Cisco Systems Inc.
// SPDX-License-Identifier: Apache-2.0
package catalystcenter

import (
	"encoding/json"
	"testing"
)

func TestTemplateDeploymentIntegerRange(t *testing.T) {
	for _, mode := range []string{"minimum", "maximum", "second", "gap", "below", "above", "reversed", "missing", "fraction", "string-bound", "imprecise", "noninteger", "array-bad", "exact-large"} {
		t.Run(mode, func(t *testing.T) {
			first := map[string]any{"minValue": float64(-5), "maxValue": float64(5)}
			ranges := []any{first, map[string]any{"minValue": float64(10), "maxValue": float64(20)}}
			kind := "INTEGER"
			values := []string{"-5"}
			switch mode {
			case "maximum":
				values = []string{"5"}
			case "second":
				values = []string{"15"}
			case "gap":
				values = []string{"7"}
			case "below":
				values = []string{"-6"}
			case "above":
				values = []string{"21"}
			case "reversed":
				first["minValue"] = 6
			case "missing":
				delete(first, "maxValue")
			case "fraction":
				first["maxValue"] = 1.5
			case "string-bound":
				first["maxValue"] = "5"
			case "imprecise":
				first["maxValue"] = float64(9007199254740992)
			case "noninteger":
				kind = "STRING"
			case "array-bad":
				values = []string{"0", "21"}
			case "exact-large":
				first["maxValue"] = json.Number("9007199254740993")
				values = []string{"9007199254740993"}
			}
			declaration := map[string]any{"parameterName": "value", "dataType": kind, "paramArray": true, "range": ranges}
			err := validateTemplateDeploymentParameters(map[string]any{"templateParams": []any{declaration}}, map[string][]string{"value": values})
			valid := mode == "minimum" || mode == "maximum" || mode == "second" || mode == "exact-large"
			if (err == nil) != valid {
				t.Fatal(mode, "unexpected range validation", err)
			}
		})
	}
}
