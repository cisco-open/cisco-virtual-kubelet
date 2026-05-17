// Copyright © 2026 Cisco Systems Inc.
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//     http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.

package writers

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/cisco/virtual-kubelet-cisco/internal/drivers/iosxe/configdriver/transport"
)

// AAA Phase-2 writer.
//
// netascode shape (YANG keys used directly):
//
//	aaa:
//	  session-id: common
//	  authentication:
//	    login:
//	      - name: default
//	        a1: {local: [~]}
//	  authorization:
//	    exec:
//	      - name: default
//	        a1: {local: [~]}
//	  accounting:
//	    commands:
//	      - name: default
//	        level: "15"
//	        action-type-config: {action-type: start-stop}
//	        broadcast: [~]
//	        group: {name-lst: [TACACS_GROUP]}
//
// YANG: /Cisco-IOS-XE-native:native/aaa/*.
//
// IOS-XE 17.x rejects a PATCH to the whole /aaa container with
// "malformed-message / Internal error" on both C8000V and C9KV virtual
// instances. Each managed leaf must be PATCHed at its own sub-path.
// The pattern mirrors systemWriter: one RESTCONF op per declared leaf.

const aaaRoot = "/Cisco-IOS-XE-native:native/aaa"

// aaaLeaf describes one managed AAA leaf.
type aaaLeaf struct {
	netKey    string
	yangPath  string
	yangField string
}

var aaaLeaves = []aaaLeaf{
	{
		netKey:    "new-model",
		yangPath:  aaaRoot + "/Cisco-IOS-XE-aaa:new-model",
		yangField: "Cisco-IOS-XE-aaa:new-model",
	},
	{
		netKey:    "session-id",
		yangPath:  aaaRoot + "/Cisco-IOS-XE-aaa:session-id",
		yangField: "Cisco-IOS-XE-aaa:session-id",
	},
	{
		netKey:    "authentication",
		yangPath:  aaaRoot + "/Cisco-IOS-XE-aaa:authentication",
		yangField: "Cisco-IOS-XE-aaa:authentication",
	},
	{
		netKey:    "authorization",
		yangPath:  aaaRoot + "/Cisco-IOS-XE-aaa:authorization",
		yangField: "Cisco-IOS-XE-aaa:authorization",
	},
	{
		netKey:    "accounting",
		yangPath:  aaaRoot + "/Cisco-IOS-XE-aaa:accounting",
		yangField: "Cisco-IOS-XE-aaa:accounting",
	},
	{
		netKey:    "group",
		yangPath:  aaaRoot + "/Cisco-IOS-XE-aaa:group",
		yangField: "Cisco-IOS-XE-aaa:group",
	},
}

type aaaWriter struct{}

func init() { Override(aaaWriter{}) }

func (aaaWriter) Family() string { return "aaa" }

func (aaaWriter) YANGPaths() []string {
	out := make([]string, 0, len(aaaLeaves))
	for _, l := range aaaLeaves {
		out = append(out, l.yangPath)
	}
	return out
}

func (aaaWriter) Fetch(ctx context.Context, c transport.Interface) (any, error) {
	out := map[string]any{}
	for _, l := range aaaLeaves {
		raw, err := c.Fetch(ctx, l.yangPath)
		if err != nil {
			if isRESTCONF404(err) {
				continue
			}
			return nil, fmt.Errorf("aaa: fetch %s: %w", l.netKey, err)
		}
		if len(raw) == 0 {
			continue
		}
		var env map[string]json.RawMessage
		if err := json.Unmarshal(raw, &env); err != nil {
			continue
		}
		body, ok := env[l.yangField]
		if !ok {
			continue
		}
		var v any
		if err := json.Unmarshal(body, &v); err != nil {
			return nil, fmt.Errorf("aaa: decode %s leaf: %w", l.netKey, err)
		}
		if l.netKey == "new-model" {
			out[l.netKey] = isTrue(v)
			continue
		}
		out[l.netKey] = v
	}
	return out, nil
}

func (aaaWriter) Diff(desired, observed any) ([]transport.Op, error) {
	desiredMap, err := coerceMap(desired, "aaa.desired")
	if err != nil {
		return nil, err
	}
	observedMap, err := coerceMap(observed, "aaa.observed")
	if err != nil {
		return nil, err
	}
	if desiredMap == nil {
		return nil, nil
	}
	if observedMap == nil {
		observedMap = map[string]any{}
	}

	ops := make([]transport.Op, 0, len(aaaLeaves))
	for _, l := range aaaLeaves {
		dv, dHas := desiredMap[l.netKey]
		if !dHas {
			continue
		}
		ov, oHas := observedMap[l.netKey]
		if l.netKey == "new-model" {
			want := isTrue(dv)
			have := oHas && isTrue(ov)
			if want == have {
				continue
			}
			if !want {
				ops = append(ops, transport.Op{
					Verb: transport.VerbDelete,
					Path: l.yangPath,
				})
				continue
			}
			body, err := json.Marshal(map[string]any{l.yangField: []any{nil}})
			if err != nil {
				return nil, err
			}
			ops = append(ops, transport.Op{
				Verb: transport.VerbMerge,
				Path: l.yangPath,
				Body: body,
			})
			continue
		}
		if oHas && scalarEqual(dv, ov) {
			continue
		}
		body, err := json.Marshal(map[string]any{l.yangField: dv})
		if err != nil {
			return nil, err
		}
		ops = append(ops, transport.Op{
			Verb: transport.VerbMerge,
			Path: l.yangPath,
			Body: body,
		})
	}
	return ops, nil
}

func (aaaWriter) Apply(ctx context.Context, c transport.Interface, ops []transport.Op) error {
	if len(ops) == 0 {
		return nil
	}
	return c.Mutate(ctx, "", ops)
}

func (aaaWriter) PruneDiff(desired, observed any) ([]transport.Op, error) {
	return nil, nil
}

func aaaManagedLeaves() []string {
	out := make([]string, 0, len(aaaLeaves))
	for _, l := range aaaLeaves {
		out = append(out, l.netKey)
	}
	return out
}
