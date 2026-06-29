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

package main

import (
	"encoding/json"
	"fmt"
	"io"
	"sort"
	"strings"
)

// renderHuman writes a terminal-friendly report to w. Layout is
// stable across runs so CI logs diff cleanly: three sections
// (drift, orphans, errors) each with a header, a list body, and
// a blank-line separator. Empty sections are emitted with an "ok"
// placeholder so operators reading the log can tell "no findings"
// apart from "tool exited before this section".
func renderHuman(w io.Writer, r Report) {
	fmt.Fprintf(w, "cisco-vk-config-lint  —  %s\n\n", r.Summary())

	fmt.Fprintln(w, "== Managed drift ==")
	if len(r.ManagedDrift) == 0 {
		fmt.Fprintln(w, "  ok — no claimed family has diverged from its intent.")
	} else {
		// Stable order: sort by family name.
		sort.Slice(r.ManagedDrift, func(i, j int) bool {
			return r.ManagedDrift[i].Family < r.ManagedDrift[j].Family
		})
		for _, d := range r.ManagedDrift {
			verbs := renderVerbs(d.Verbs)
			fmt.Fprintf(w, "  - %s  (%d ops: %s)  claimed by: %s\n",
				d.Family, d.OpCount, verbs, strings.Join(d.Claimers, ", "))
		}
	}
	fmt.Fprintln(w)

	fmt.Fprintln(w, "== Device orphans (config outside any IOSXEConfig claim) ==")
	if len(r.Orphans) == 0 {
		fmt.Fprintln(w, "  ok — every non-empty family on the device is claimed by a CR.")
	} else {
		sort.Slice(r.Orphans, func(i, j int) bool {
			return r.Orphans[i].Family < r.Orphans[j].Family
		})
		for _, o := range r.Orphans {
			fmt.Fprintf(w, "  - %s\n", o.Family)
			for _, p := range o.YANGPaths {
				fmt.Fprintf(w, "      %s\n", p)
			}
		}
	}
	fmt.Fprintln(w)

	if len(r.Errors) > 0 {
		fmt.Fprintln(w, "== Errors (families excluded from the findings above) ==")
		sort.Slice(r.Errors, func(i, j int) bool {
			return r.Errors[i].Family < r.Errors[j].Family
		})
		for _, e := range r.Errors {
			fmt.Fprintf(w, "  - %s (%s): %s\n", e.Family, e.Stage, e.Err)
		}
		fmt.Fprintln(w)
	}
}

func renderVerbs(verbs map[string]int) string {
	if len(verbs) == 0 {
		return "?"
	}
	keys := make([]string, 0, len(verbs))
	for k := range verbs {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	parts := make([]string, 0, len(keys))
	for _, k := range keys {
		parts = append(parts, fmt.Sprintf("%s=%d", k, verbs[k]))
	}
	return strings.Join(parts, " ")
}

// renderJSON writes the report as indented JSON. Suitable for CI
// consumption, dashboards, and piping to jq for per-family filters
// ("fail only on DELETE ops": `jq '.managedDrift[] | select(.verbs.DELETE)'`).
func renderJSON(w io.Writer, r Report) error {
	enc := json.NewEncoder(w)
	enc.SetIndent("", "  ")
	return enc.Encode(r)
}
