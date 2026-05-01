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

package intent

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"sort"
)

// CanonicalHash returns a stable SHA-256 hex digest of a ResolvedIntent
// suitable for storing in status.lastAppliedHash. Two resolved intents
// that produce the same hash can be treated as equivalent by the
// reconcile short-circuit — map-key order and YAML whitespace do not
// affect the result.
//
// The hash excludes SourceCR (which includes resourceVersion/generation
// that the reconciler tracks separately) so a no-op re-list of the CR
// does not invalidate the cached state.
func CanonicalHash(intent *ResolvedIntent) (string, error) {
	if intent == nil {
		return "", fmt.Errorf("CanonicalHash: nil intent")
	}

	// Build a stable representation as a sorted JSON-compatible tree.
	// CLIBlocks are included so a change to a cli template invalidates
	// the reconciler's hash short-circuit even when the structured
	// configuration map is unchanged.
	cliBlocksPayload := make([]map[string]any, 0, len(intent.CLIBlocks))
	for _, b := range intent.CLIBlocks {
		cliBlocksPayload = append(cliBlocksPayload, map[string]any{
			"name": b.TemplateName,
			"cli":  b.CLI,
		})
	}
	payload := map[string]any{
		"deviceName":      intent.DeviceName,
		"managedFamilies": append([]string(nil), intent.ManagedFamilies...),
		"transactional":   intent.Transactional,
		"writeStartup":    intent.WriteStartup,
		"driftPolicy":     string(intent.DriftPolicy),
		"configuration":   intent.Configuration,
		"cliBlocks":       cliBlocksPayload,
	}
	canonical, err := canonicalJSON(payload)
	if err != nil {
		return "", fmt.Errorf("serialise: %w", err)
	}
	sum := sha256.Sum256(canonical)
	return "sha256:" + hex.EncodeToString(sum[:]), nil
}

// canonicalJSON produces a JSON encoding whose output is determined only
// by the structural value — map keys are sorted, slices are preserved in
// their natural order, and numbers are rendered as JSON numbers. The
// encoding is NOT intended to be reversible; it is a fingerprinting
// serialisation only.
func canonicalJSON(v any) ([]byte, error) {
	normalised, err := normalise(v)
	if err != nil {
		return nil, err
	}
	return json.Marshal(normalised)
}

// normalise walks a value produced by decoded YAML and converts it into
// a form encoding/json can marshal in a deterministic order:
//
//   - map[string]any → json.RawMessage of keys-sorted JSON.
//   - []any → recurse into elements in order.
//   - primitives → left as-is.
//
// Using json.RawMessage for maps means the outer Marshal preserves the
// sorted key order we encoded; json.Marshal on a map[string]any
// otherwise produces sorted output already in Go, but relying on that
// unwritten property would be brittle.
func normalise(v any) (any, error) {
	switch tv := v.(type) {
	case map[string]any:
		keys := make([]string, 0, len(tv))
		for k := range tv {
			keys = append(keys, k)
		}
		sort.Strings(keys)

		buf := []byte("{")
		for i, k := range keys {
			if i > 0 {
				buf = append(buf, ',')
			}
			keyJSON, err := json.Marshal(k)
			if err != nil {
				return nil, err
			}
			buf = append(buf, keyJSON...)
			buf = append(buf, ':')
			valNorm, err := normalise(tv[k])
			if err != nil {
				return nil, err
			}
			valJSON, err := json.Marshal(valNorm)
			if err != nil {
				return nil, err
			}
			buf = append(buf, valJSON...)
		}
		buf = append(buf, '}')
		return json.RawMessage(buf), nil
	case []any:
		out := make([]any, len(tv))
		for i, el := range tv {
			n, err := normalise(el)
			if err != nil {
				return nil, err
			}
			out[i] = n
		}
		return out, nil
	default:
		return tv, nil
	}
}
