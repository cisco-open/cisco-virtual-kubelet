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

// System Phase-1 writer. The netascode "system" family covers a
// handful of global leaves on the native root container; Phase-1
// manages a narrow, well-defined set rather than owning the whole
// /native subtree, to avoid disturbing leaves the operator hasn't
// declared in CVK.
//
// netascode shape (relevant keys only):
//
//	system:
//	  hostname: edge-01
//	  mtu: 1500
//	  ip_routing: true
//	  login_on_failure: true
//	  ipv6_unicast_routing: false
//
// YANG mapping: each managed leaf is a leaf or small container under
// /Cisco-IOS-XE-native:native. The writer addresses them individually
// with targeted PATCHes rather than touching /native as a whole.
const systemRoot = "/Cisco-IOS-XE-native:native"

// systemLeaf describes one managed system leaf: its netascode key,
// the RESTCONF subpath under /native, and the YANG key used in the
// JSON envelope the device expects.
type systemLeaf struct {
	netKey    string // key under the netascode "system" container
	yangPath  string // subpath under /Cisco-IOS-XE-native:native
	yangField string // key name inside the YANG payload envelope
}

// systemLeaves enumerates the Phase-1 managed set. Adding a leaf here
// is a one-line change and does not require a writer-level refactor.
var systemLeaves = []systemLeaf{
	{netKey: "hostname", yangPath: systemRoot + "/hostname", yangField: "Cisco-IOS-XE-native:hostname"},
}

type systemWriter struct{}

func init() { Override(systemWriter{}) }

func (systemWriter) Family() string { return "system" }

func (systemWriter) YANGPaths() []string {
	out := make([]string, 0, len(systemLeaves))
	for _, l := range systemLeaves {
		out = append(out, l.yangPath)
	}
	return out
}

// Fetch reads every managed leaf individually and re-assembles them
// into a netascode-shaped map. Missing leaves (404) are represented as
// absent keys so Diff can detect "leaf declared but not on device".
func (systemWriter) Fetch(ctx context.Context, c transport.Interface) (any, error) {
	out := map[string]any{}
	for _, l := range systemLeaves {
		raw, err := c.Fetch(ctx, l.yangPath)
		if err != nil {
			if isRESTCONF404(err) {
				continue
			}
			return nil, fmt.Errorf("system: fetch %s: %w", l.netKey, err)
		}
		var env map[string]json.RawMessage
		if err := json.Unmarshal(raw, &env); err != nil {
			return nil, fmt.Errorf("system: decode %s: %w", l.netKey, err)
		}
		body, ok := env[l.yangField]
		if !ok {
			continue
		}
		var v any
		if err := json.Unmarshal(body, &v); err != nil {
			return nil, fmt.Errorf("system: decode %s leaf: %w", l.netKey, err)
		}
		out[l.netKey] = v
	}
	return out, nil
}

func (systemWriter) Diff(desired, observed any) ([]transport.Op, error) {
	desiredMap, err := coerceMap(desired, "system.desired")
	if err != nil {
		return nil, err
	}
	observedMap, err := coerceMap(observed, "system.observed")
	if err != nil {
		return nil, err
	}
	if desiredMap == nil {
		return nil, nil
	}
	if observedMap == nil {
		observedMap = map[string]any{}
	}

	ops := make([]transport.Op, 0, len(systemLeaves))
	for _, l := range systemLeaves {
		dv, dHas := desiredMap[l.netKey]
		if !dHas {
			continue
		}
		ov, oHas := observedMap[l.netKey]
		if oHas && scalarEqual(dv, ov) {
			continue
		}
		body, err := json.Marshal(map[string]any{l.yangField: dv})
		if err != nil {
			return nil, err
		}
		ops = append(ops, transport.Op{
			Verb: transport.VerbReplace,
			Path: l.yangPath,
			Body: body,
		})
	}
	return ops, nil
}

func (systemWriter) Apply(ctx context.Context, c transport.Interface, ops []transport.Op) error {
	if len(ops) == 0 {
		return nil
	}
	return c.Mutate(ctx, "", ops)
}
