// Copyright 2026 Cisco Systems Inc.
// SPDX-License-Identifier: Apache-2.0
package catalystcenter

import (
	"encoding/json"
	"math"
	"math/big"
)

type templateIntegerRange struct{ min, max *big.Int }

func templateRangeInteger(value any) (*big.Int, error) {
	// Native maps currently decode JSON numbers as float64. Refuse bounds that
	// may already have lost integer precision; never round a constraint silently.
	if n, ok := value.(float64); ok {
		if math.IsNaN(n) || math.IsInf(n, 0) || math.Trunc(n) != n || math.Abs(n) > 9007199254740991 {
			return nil, errInvalidResponse
		}
	}
	switch value.(type) {
	case int, int64, uint64, float64, json.Number:
	default:
		return nil, errInvalidResponse
	}
	raw, err := json.Marshal(value)
	if err != nil {
		return nil, errInvalidResponse
	}
	number, ok := new(big.Rat).SetString(string(raw))
	if !ok || !number.IsInt() {
		return nil, errInvalidResponse
	}
	return new(big.Int).Set(number.Num()), nil
}

// Integer bounds are inclusive. Multiple ranges describe alternative allowed
// intervals. Other parameter types require separately qualified semantics.
func readTemplateIntegerRanges(kind string, raw any) ([]templateIntegerRange, error) {
	if raw == nil {
		return nil, nil
	}
	rows, ok := raw.([]any)
	if !ok || len(rows) > 1000 {
		return nil, errInvalidResponse
	}
	if len(rows) == 0 {
		return nil, nil
	}
	if kind != "INTEGER" {
		return nil, errConfigurationTransition
	}
	result := make([]templateIntegerRange, 0, len(rows))
	for _, row := range rows {
		fields, ok := row.(map[string]any)
		if !ok {
			return nil, errInvalidResponse
		}
		min, err := templateRangeInteger(fields["minValue"])
		if err != nil {
			return nil, err
		}
		max, err := templateRangeInteger(fields["maxValue"])
		if err != nil || min.Cmp(max) > 0 {
			return nil, errInvalidResponse
		}
		result = append(result, templateIntegerRange{min: min, max: max})
	}
	return result, nil
}
func validateTemplateIntegerRange(ranges []templateIntegerRange, value string) error {
	if len(ranges) == 0 {
		return nil
	}
	number, ok := new(big.Int).SetString(value, 10)
	if !ok {
		return errConfigurationTransition
	}
	for _, interval := range ranges {
		if number.Cmp(interval.min) >= 0 && number.Cmp(interval.max) <= 0 {
			return nil
		}
	}
	return errConfigurationTransition
}
