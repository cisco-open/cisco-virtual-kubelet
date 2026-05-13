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

	"github.com/cisco/virtual-kubelet-cisco/internal/drivers/iosxe/configdriver/transport"
)

// SNMP server Phase-2 writer.
//
// netascode (common subset):
//
//   snmp_server:
//     community:
//       - name: public
//         access: ro
//     location: "colo-1"
//     contact: "noc@example.com"
//     trap_source_interface:
//       Loopback: "0"
//
// YANG: /Cisco-IOS-XE-native:native/snmp-server. Phase-2 manages the
// commonly-configured leaves; v3 groups/users and engine-id
// management are Phase-3.
//
// The SNMP community list uses empty leaves `RO` and `RW` to
// encode the access level — YANG `type empty;` not a string.
// Caught against C8000V 17.16.01a: {"community":[{"name":"public",
// "access":"ro"}]} rejected with malformed-message.

func init() {
	Override(snmpWriter{})
}

type snmpWriter struct{}

func (snmpWriter) Family() string      { return "snmp_server" }
func (snmpWriter) YANGPaths() []string { return []string{"/Cisco-IOS-XE-native:native/snmp-server"} }

func (w snmpWriter) Fetch(ctx context.Context, c transport.Interface) (any, error) {
	sw := singletonWriter{
		family:        "snmp_server",
		yangPath:      "/Cisco-IOS-XE-native:native/snmp-server",
		envelopeKey:   "Cisco-IOS-XE-snmp:snmp-server",
		managedLeaves: snmpManagedLeaves,
	}
	observed, err := sw.Fetch(ctx, c)
	if err != nil {
		return nil, err
	}
	m, _ := observed.(map[string]any)
	if m == nil {
		return observed, nil
	}
	// Normalise community entries: YANG RO/RW → netascode access.
	if comms, ok := m["community"].([]any); ok {
		for i, c := range comms {
			entry, ok := c.(map[string]any)
			if !ok {
				continue
			}
			comms[i] = snmpCommunityFromYANG(entry)
		}
	}
	return m, nil
}

func (w snmpWriter) Diff(desired, observed any) ([]transport.Op, error) {
	desiredMap, err := coerceMap(desired, "snmp_server.desired")
	if err != nil {
		return nil, err
	}
	observedMap, err := coerceMap(observed, "snmp_server.observed")
	if err != nil {
		return nil, err
	}
	if desiredMap == nil {
		return nil, nil
	}
	if observedMap == nil {
		observedMap = map[string]any{}
	}
	if leavesEqual(desiredMap, observedMap, snmpManagedLeaves) {
		return nil, nil
	}
	proj := projectManagedLeaves(desiredMap, snmpManagedLeaves)
	// Transform community entries for YANG wire shape.
	if comms, ok := proj["community"].([]any); ok {
		fixed := make([]any, 0, len(comms))
		for _, c := range comms {
			entry, ok := c.(map[string]any)
			if !ok {
				fixed = append(fixed, c)
				continue
			}
			fixed = append(fixed, snmpCommunityToYANG(entry))
		}
		proj["community"] = fixed
	}
	body, err := wrapYANGPayload("Cisco-IOS-XE-snmp:snmp-server", proj)
	if err != nil {
		return nil, err
	}
	return []transport.Op{{
		Verb: transport.VerbMerge,
		Path: "/Cisco-IOS-XE-native:native/snmp-server",
		Body: body,
	}}, nil
}

func (w snmpWriter) Apply(ctx context.Context, c transport.Interface, ops []transport.Op) error {
	if len(ops) == 0 {
		return nil
	}
	return c.Mutate(ctx, "", ops)
}

var snmpManagedLeaves = []string{
	"community",
	"location",
	"contact",
	"trap-source",
	"host",
}

// snmpCommunityToYANG transforms the netascode community entry to
// the YANG shape: access: "ro" → RO: [null], access: "rw" → RW: [null].
func snmpCommunityToYANG(entry map[string]any) map[string]any {
	out := make(map[string]any, len(entry))
	for k, v := range entry {
		if k == "access" {
			s, _ := v.(string)
			switch s {
			case "ro", "RO":
				out["RO"] = []any{nil}
			case "rw", "RW":
				out["RW"] = []any{nil}
			}
			continue
		}
		out[k] = v
	}
	return out
}

// snmpCommunityFromYANG inverts the transform for observed state.
func snmpCommunityFromYANG(entry map[string]any) map[string]any {
	out := make(map[string]any, len(entry))
	for k, v := range entry {
		switch k {
		case "RO":
			out["access"] = "ro"
		case "RW":
			out["access"] = "rw"
		default:
			out[k] = v
		}
	}
	return out
}
