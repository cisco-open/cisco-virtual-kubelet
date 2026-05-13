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
	"fmt"

	"github.com/cisco/virtual-kubelet-cisco/internal/drivers/iosxe/configdriver/transport"
)

// Logging Phase-2 writer.
//
// netascode (common subset):
//
//   logging:
//     buffered:    50000
//     host_ipv4:
//       - name: 10.0.0.10
//     facility: local7
//     trap: informational
//     source_interface:
//       Loopback: "0"
//
// YANG: /Cisco-IOS-XE-native:native/logging. Managed leaves are the
// commonly-configured knobs; the full logging container is large and
// Phase-3 can grow the set as needs appear.
//
// The "buffered" leaf in netascode is a flat integer (buffer size),
// but the YANG model wraps it in a container: {size: <int>}.
// Caught against C8000V 17.16.01a: {"buffered":16384} rejected
// with invalid-value.

func init() {
	Override(loggingWriter{})
}

type loggingWriter struct{}

func (loggingWriter) Family() string      { return "logging" }
func (loggingWriter) YANGPaths() []string { return []string{"/Cisco-IOS-XE-native:native/logging"} }

var loggingManagedLeaves = []string{
	"buffered",
	"host",
	"facility",
	"trap",
	"source-interface",
	"origin-id",
}

func (w loggingWriter) Fetch(ctx context.Context, c transport.Interface) (any, error) {
	sw := singletonWriter{
		family:        "logging",
		yangPath:      "/Cisco-IOS-XE-native:native/logging",
		envelopeKey:   "Cisco-IOS-XE-native:logging",
		managedLeaves: loggingManagedLeaves,
	}
	observed, err := sw.Fetch(ctx, c)
	if err != nil {
		return nil, err
	}
	m, _ := observed.(map[string]any)
	if m == nil {
		return observed, nil
	}
	// Normalise: YANG {buffered: {size: N}} → netascode {buffered: N}
	if buf, ok := m["buffered"].(map[string]any); ok {
		if size, ok := buf["size"]; ok {
			m["buffered"] = size
		}
	}
	return m, nil
}

func (w loggingWriter) Diff(desired, observed any) ([]transport.Op, error) {
	desiredMap, err := coerceMap(desired, "logging.desired")
	if err != nil {
		return nil, err
	}
	observedMap, err := coerceMap(observed, "logging.observed")
	if err != nil {
		return nil, err
	}
	if desiredMap == nil {
		return nil, nil
	}
	if observedMap == nil {
		observedMap = map[string]any{}
	}
	if leavesEqual(desiredMap, observedMap, loggingManagedLeaves) {
		return nil, nil
	}
	proj := projectManagedLeaves(desiredMap, loggingManagedLeaves)
	// Transform: netascode {buffered: N} → YANG {buffered: {size: N}}
	if buf, ok := proj["buffered"]; ok {
		switch buf.(type) {
		case float64, int, int64:
			proj["buffered"] = map[string]any{"size": buf}
		case map[string]any:
			// Already in YANG shape — pass through.
		default:
			return nil, fmt.Errorf("logging: buffered: unsupported type %T", buf)
		}
	}
	body, err := wrapYANGPayload("Cisco-IOS-XE-native:logging", proj)
	if err != nil {
		return nil, err
	}
	return []transport.Op{{
		Verb: transport.VerbMerge,
		Path: "/Cisco-IOS-XE-native:native/logging",
		Body: body,
	}}, nil
}

func (w loggingWriter) Apply(ctx context.Context, c transport.Interface, ops []transport.Op) error {
	if len(ops) == 0 {
		return nil
	}
	return c.Mutate(ctx, "", ops)
}
