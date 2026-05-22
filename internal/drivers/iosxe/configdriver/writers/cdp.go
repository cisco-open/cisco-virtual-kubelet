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

// CDP Phase-2 writer.
//
// netascode:
//   cdp:
//     run: true
//     advertise_v2: true
//
// YANG: /Cisco-IOS-XE-native:native/cdp.
//
// Leaf-name translation (netascode -> Cisco-IOS-XE-native YANG):
//
//   - run          -> run-enable   (the YANG `run` leaf is `status
//                                   obsolete` in Cisco-IOS-XE-cdp.yang
//                                   and rejected by RESTCONF on
//                                   17.15/17.16; `run-enable` is the
//                                   non-obsolete boolean enable leaf)
//   - advertise_v2 -> advertise-v2 (underscore -> hyphen)
//
// Both YANG leaves are `type boolean`, so they are emitted as plain
// JSON booleans — not [null] empty-leaf encoding. Anything outside the
// managed set is left untouched on the device per the additive-merge
// semantics.
//
// On IOS-XE < 17.18 these augmented leaves additionally require a
// "Cisco-IOS-XE-cdp:" module prefix in the RESTCONF body. That
// release-conditional rename is applied by the version override table
// (yang_version_override_table.go), which stacks after cdpToYANG on the
// Diff path and is reversed before cdpFromYANG on the Fetch path.

func init() {
	Override(singletonWriter{
		family:         "cdp",
		yangPath:       "/Cisco-IOS-XE-native:native/cdp",
		envelopeKey:    "Cisco-IOS-XE-native:cdp",
		managedLeaves:  []string{"run", "advertise_v2"},
		yangBodyShape:  cdpToYANG,
		yangFetchShape: cdpFromYANG,
	})
}

// cdpToYANG maps the netascode canonical CDP leaves to their
// Cisco-IOS-XE-native YANG names for the Diff/Apply path. It runs on
// the projected managed-leaf body before the RESTCONF envelope is
// applied.
func cdpToYANG(flat map[string]any) map[string]any {
	out := make(map[string]any, len(flat))
	for k, v := range flat {
		switch k {
		case "run":
			out["run-enable"] = v
		case "advertise_v2":
			out["advertise-v2"] = v
		default:
			out[k] = v
		}
	}
	return out
}

// cdpFromYANG is the Fetch/Diff-side inverse of cdpToYANG. It maps the
// device's YANG leaf names back to the netascode canonical shape so
// leavesEqual compares like with like.
//
// It runs against two different inputs: the observed body fetched from
// the device (YANG-shaped: run-enable / advertise-v2) and the desired
// body (already netascode-shaped: run / advertise_v2). It must be a
// no-op on canonical input, so `run` is honoured as a fallback when no
// `run-enable` is present.
//
// `run-enable` is authoritative for the device's CDP enable state; the
// bare `run` empty leaf is obsolete. When a device reports both,
// `run-enable` wins deterministically (the result does not depend on
// map iteration order). A `run`-only observed body is decoded from the
// YANG empty-leaf encoding ([null] → true) so drift comparison sees a
// boolean, not a slice.
func cdpFromYANG(yang map[string]any) map[string]any {
	out := make(map[string]any, len(yang))
	for k, v := range yang {
		switch k {
		case "run", "run-enable":
			// Resolved after the loop so run-enable wins deterministically.
		case "advertise-v2":
			out["advertise_v2"] = v
		default:
			out[k] = v
		}
	}
	if v, ok := yang["run-enable"]; ok {
		out["run"] = v
	} else if v, ok := yang["run"]; ok {
		// The obsolete `run` leaf is YANG type empty: a [null] presence
		// value means enabled. Decode to a canonical boolean so
		// leavesEqual compares bool against bool — a raw []any{nil}
		// would read as perpetual drift. isTrue is a no-op on the
		// boolean `run` of canonical desired intent.
		out["run"] = isTrue(v)
	}
	return out
}
