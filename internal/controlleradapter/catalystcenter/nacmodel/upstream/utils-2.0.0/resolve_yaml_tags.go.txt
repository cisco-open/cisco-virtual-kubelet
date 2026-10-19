// Copyright © 2022 Cisco Systems, Inc. and its affiliates.
// All rights reserved.
//
// Licensed under the Mozilla Public License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//     https://mozilla.org/MPL/2.0/
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.
//
// SPDX-License-Identifier: MPL-2.0

package provider

import (
	"fmt"
	"os"
	"strings"
)

// resolveYamlTags recursively walks a native Go value and resolves YAML tag strings.
// Currently supports the "!env VARNAME" tag, which is resolved to the value of the
// corresponding environment variable.
func resolveYamlTags(v any) (any, error) {
	switch val := v.(type) {
	case string:
		return resolveTagString(val)
	case *OrderedMap:
		result := NewOrderedMap(val.Len())
		for _, e := range val.Entries() {
			resolved, err := resolveYamlTags(e.Value)
			if err != nil {
				return nil, err
			}
			result.Set(e.Key, resolved)
		}
		return result, nil
	case map[string]any:
		result := make(map[string]any, len(val))
		for k, v := range val {
			resolved, err := resolveYamlTags(v)
			if err != nil {
				return nil, err
			}
			result[k] = resolved
		}
		return result, nil
	case []any:
		result := make([]any, len(val))
		for i, v := range val {
			resolved, err := resolveYamlTags(v)
			if err != nil {
				return nil, err
			}
			result[i] = resolved
		}
		return result, nil
	default:
		// int, float64, bool, nil — pass through unchanged
		return v, nil
	}
}

// resolveTagString checks if a string contains a known YAML tag prefix and resolves it.
func resolveTagString(s string) (any, error) {
	if strings.HasPrefix(s, "!env ") {
		varName := strings.TrimPrefix(s, "!env ")
		varName = strings.TrimSpace(varName)
		value := os.Getenv(varName)
		if value == "" {
			return nil, fmt.Errorf("environment variable %s not set", varName)
		}
		return value, nil
	}
	return s, nil
}
