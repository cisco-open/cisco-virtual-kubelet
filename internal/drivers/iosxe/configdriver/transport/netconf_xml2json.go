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

package transport

import (
	"encoding/json"
	"encoding/xml"
	"fmt"
	"io"
	"strings"
)

// xmlToYangJSON converts a NETCONF XML subtree (the bytes held in
// rpcReply.Data) into a yang-data+json-style byte slice that the
// family writers can decode via their existing JSON decoders.
//
// The conversion is the RFC 7951 mapping, narrowed to what Cisco
// YANG outputs in practice:
//
//   - Each element becomes an object entry keyed by
//     "<module>:<local>" where module is derived from the XML
//     namespace; when the namespace is absent or unknown we emit
//     just "<local>" (writers tolerate both via their
//     unwrapYANGEnvelope helper).
//   - Sibling elements with the same name become an array.
//   - Text-only elements become JSON strings. Numeric text is
//     kept as a string because we can't tell "integer leaf" from
//     "string leaf" without a YANG schema; writers coerce
//     scalars via fmt.Sprintf when comparing.
//   - Mixed-content elements (rare in Cisco YANG) are emitted
//     object-only, with text nodes dropped. No round-trip
//     fidelity is promised here — the writers are the shape
//     source of truth.
//
// The converter is best-effort: families whose YANG uses types
// that depend on schema context (empty leaves, typed unions) may
// need writer-level coercion. The Phase-1/2/3 writers all model
// leaves as either strings, bools, numbers, or opaque nested
// slices, so their Diff paths tolerate the string-ification here.
func xmlToYangJSON(raw []byte, nsToPrefix map[string]string) ([]byte, error) {
	if len(raw) == 0 {
		return []byte(`{}`), nil
	}
	dec := xml.NewDecoder(strings.NewReader("<_root_>" + string(raw) + "</_root_>"))
	root, err := decodeElement(dec, nsToPrefix)
	if err != nil {
		return nil, fmt.Errorf("xml -> json: %w", err)
	}
	return json.Marshal(root)
}

// decodeElement reads tokens until it has consumed one complete
// element and returns the value. Children are collected into a
// map[string]any; when a child key recurs, the entry is promoted
// to []any. Leaf text becomes a string.
func decodeElement(dec *xml.Decoder, nsToPrefix map[string]string) (any, error) {
	var obj map[string]any
	var textBuf strings.Builder

	for {
		tok, err := dec.Token()
		if err == io.EOF {
			break
		}
		if err != nil {
			return nil, err
		}
		switch t := tok.(type) {
		case xml.StartElement:
			childVal, err := decodeElement(dec, nsToPrefix)
			if err != nil {
				return nil, err
			}
			key := yangKey(t.Name, nsToPrefix)
			if obj == nil {
				obj = map[string]any{}
			}
			if existing, present := obj[key]; present {
				switch e := existing.(type) {
				case []any:
					obj[key] = append(e, childVal)
				default:
					obj[key] = []any{existing, childVal}
				}
			} else {
				obj[key] = childVal
			}
		case xml.CharData:
			textBuf.Write(t)
		case xml.EndElement:
			if obj != nil {
				return obj, nil
			}
			text := strings.TrimSpace(textBuf.String())
			if text == "" {
				// Empty element (e.g. <shutdown/>) → empty string;
				// writers expect JSON booleans but null/empty-string
				// for absence. Pick empty-string for maximum
				// compatibility; a future per-family type table
				// could promote known-bool leaves.
				return "", nil
			}
			return text, nil
		case xml.ProcInst, xml.Directive, xml.Comment:
			// Ignored — these never carry YANG data.
		}
	}

	// Hit EOF — return what we have. Only happens for the wrapper
	// <_root_> element injected by xmlToYangJSON.
	if obj != nil {
		return obj, nil
	}
	text := strings.TrimSpace(textBuf.String())
	if text == "" {
		return map[string]any{}, nil
	}
	return text, nil
}

// yangKey produces the "<module>:<local>" key per RFC 7951. When
// the namespace isn't in nsToPrefix we emit just the local name —
// writers tolerate that via unwrapYANGEnvelope's best-effort
// fallback.
func yangKey(name xml.Name, nsToPrefix map[string]string) string {
	if name.Space == "" {
		return name.Local
	}
	if prefix, ok := nsToPrefix[name.Space]; ok {
		return prefix + ":" + name.Local
	}
	// Best effort: derive a prefix from the namespace URI when it
	// has the conventional Cisco shape
	// http://cisco.com/ns/yang/<module>.
	if idx := strings.LastIndex(name.Space, "/"); idx != -1 {
		return name.Space[idx+1:] + ":" + name.Local
	}
	return name.Local
}

// ciscoYANGPrefixes is a starter namespace->prefix table covering
// the Cisco-IOS-XE modules the Phase-1/2/3 writers fetch. Not
// exhaustive — a writer that hits a path with a namespace we
// haven't listed here still works; the key just falls back to
// just the local name.
var ciscoYANGPrefixes = map[string]string{
	"http://cisco.com/ns/yang/Cisco-IOS-XE-native":        "Cisco-IOS-XE-native",
	"http://cisco.com/ns/yang/Cisco-IOS-XE-vlan":          "Cisco-IOS-XE-vlan",
	"http://cisco.com/ns/yang/Cisco-IOS-XE-acl":           "Cisco-IOS-XE-acl",
	"http://cisco.com/ns/yang/Cisco-IOS-XE-bgp":           "Cisco-IOS-XE-bgp",
	"http://cisco.com/ns/yang/Cisco-IOS-XE-ospf":          "Cisco-IOS-XE-ospf",
	"http://cisco.com/ns/yang/Cisco-IOS-XE-eigrp":         "Cisco-IOS-XE-eigrp",
	"http://cisco.com/ns/yang/Cisco-IOS-XE-isis":          "Cisco-IOS-XE-isis",
	"http://cisco.com/ns/yang/Cisco-IOS-XE-aaa":           "Cisco-IOS-XE-aaa",
	"http://cisco.com/ns/yang/Cisco-IOS-XE-crypto":        "Cisco-IOS-XE-crypto",
	"http://cisco.com/ns/yang/Cisco-IOS-XE-http":          "Cisco-IOS-XE-http",
	"http://cisco.com/ns/yang/Cisco-IOS-XE-lldp":          "Cisco-IOS-XE-lldp",
	"http://cisco.com/ns/yang/Cisco-IOS-XE-nat":           "Cisco-IOS-XE-nat",
	"http://cisco.com/ns/yang/Cisco-IOS-XE-ntp":           "Cisco-IOS-XE-ntp",
	"http://cisco.com/ns/yang/Cisco-IOS-XE-policy":        "Cisco-IOS-XE-policy",
	"http://cisco.com/ns/yang/Cisco-IOS-XE-snmp":          "Cisco-IOS-XE-snmp",
	"http://cisco.com/ns/yang/Cisco-IOS-XE-spanning-tree": "Cisco-IOS-XE-spanning-tree",
	"http://cisco.com/ns/yang/Cisco-IOS-XE-track":         "Cisco-IOS-XE-track",
	"http://cisco.com/ns/yang/Cisco-IOS-XE-eem":           "Cisco-IOS-XE-eem",
	"http://cisco.com/yang/cisco-ia":                      "cisco-ia",
}
