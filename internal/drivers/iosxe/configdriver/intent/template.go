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
	"bytes"
	"fmt"
	"net"
	"strconv"
	"text/template"

	"sigs.k8s.io/yaml"

	configv1alpha1 "github.com/cisco/virtual-kubelet-cisco/api/config/v1alpha1"
)

// ExpandTemplate renders an IOSXETemplate with the supplied values and
// returns the netascode fragment the result represents. The rendering is
// restricted to parameter substitution via the Go text/template engine —
// no function calls, no nested template includes, no file access. Every
// declared parameter is validated against its type before substitution;
// undeclared values in the caller-supplied map are an error so a typo
// does not silently produce an empty interpolation.
func ExpandTemplate(tpl *configv1alpha1.IOSXETemplate, values map[string]string) (map[string]any, error) {
	if tpl == nil {
		return nil, fmt.Errorf("ExpandTemplate: nil template")
	}

	resolved, err := resolveParameters(tpl.Spec.Parameters, values)
	if err != nil {
		return nil, fmt.Errorf("template %s: %w", tpl.Name, err)
	}

	var body map[string]any
	if err := yaml.Unmarshal(tpl.Spec.Configuration.Raw, &body); err != nil {
		return nil, fmt.Errorf("template %s: parse configuration: %w", tpl.Name, err)
	}

	rendered, err := renderTree(body, resolved)
	if err != nil {
		return nil, fmt.Errorf("template %s: render: %w", tpl.Name, err)
	}
	out, ok := rendered.(map[string]any)
	if !ok {
		return nil, fmt.Errorf("template %s: expansion produced non-object root", tpl.Name)
	}
	return out, nil
}

func resolveParameters(declared []configv1alpha1.TemplateParameter, supplied map[string]string) (map[string]string, error) {
	known := map[string]configv1alpha1.TemplateParameter{}
	out := map[string]string{}

	for _, p := range declared {
		known[p.Name] = p
		if v, ok := supplied[p.Name]; ok {
			if err := validateParameter(p, v); err != nil {
				return nil, err
			}
			out[p.Name] = v
			continue
		}
		if p.Required {
			return nil, fmt.Errorf("required parameter %q not supplied", p.Name)
		}
		if p.Default != "" {
			if err := validateParameter(p, p.Default); err != nil {
				return nil, fmt.Errorf("default for %q: %w", p.Name, err)
			}
			out[p.Name] = p.Default
		}
	}

	for name := range supplied {
		if _, declared := known[name]; !declared {
			return nil, fmt.Errorf("value supplied for undeclared parameter %q", name)
		}
	}
	return out, nil
}

func validateParameter(p configv1alpha1.TemplateParameter, v string) error {
	switch p.Type {
	case configv1alpha1.TemplateParameterString:
		// Every string is valid; an empty value is allowed unless the
		// parameter was required (already checked).
		return nil
	case configv1alpha1.TemplateParameterInt:
		if _, err := strconv.ParseInt(v, 10, 64); err != nil {
			return fmt.Errorf("parameter %q: not an integer: %w", p.Name, err)
		}
	case configv1alpha1.TemplateParameterBool:
		if _, err := strconv.ParseBool(v); err != nil {
			return fmt.Errorf("parameter %q: not a boolean: %w", p.Name, err)
		}
	case configv1alpha1.TemplateParameterIPv4:
		ip := net.ParseIP(v)
		if ip == nil || ip.To4() == nil {
			return fmt.Errorf("parameter %q: not an IPv4 address", p.Name)
		}
	case configv1alpha1.TemplateParameterIPv6:
		ip := net.ParseIP(v)
		if ip == nil || ip.To4() != nil {
			return fmt.Errorf("parameter %q: not an IPv6 address", p.Name)
		}
	case configv1alpha1.TemplateParameterCIDR:
		if _, _, err := net.ParseCIDR(v); err != nil {
			return fmt.Errorf("parameter %q: not a CIDR: %w", p.Name, err)
		}
	default:
		return fmt.Errorf("parameter %q: unknown type %q", p.Name, p.Type)
	}
	return nil
}

// renderTree walks the decoded YAML tree and interpolates {{ .Name }}
// references in any string leaf. Non-string leaves pass through. Errors
// from a single leaf abort the whole expansion so partial renders never
// reach the device.
func renderTree(node any, values map[string]string) (any, error) {
	switch n := node.(type) {
	case map[string]any:
		out := make(map[string]any, len(n))
		for k, v := range n {
			rendered, err := renderTree(v, values)
			if err != nil {
				return nil, fmt.Errorf("key %q: %w", k, err)
			}
			out[k] = rendered
		}
		return out, nil
	case []any:
		out := make([]any, len(n))
		for i, v := range n {
			rendered, err := renderTree(v, values)
			if err != nil {
				return nil, fmt.Errorf("index %d: %w", i, err)
			}
			out[i] = rendered
		}
		return out, nil
	case string:
		return renderLeaf(n, values)
	default:
		return n, nil
	}
}

func renderLeaf(s string, values map[string]string) (string, error) {
	// Cheap path: no template delimiters, no work to do.
	if !bytes.Contains([]byte(s), []byte("{{")) {
		return s, nil
	}
	// Option("missingkey=error") means a {{ .Unknown }} reference fails
	// the expansion rather than rendering as "<no value>". text/template
	// has no function whitelist concept, but leaving no FuncMap registered
	// gives us built-ins only (html/js escapers, eq/ne/lt — harmless).
	tpl, err := template.New("iosxe-template").
		Option("missingkey=error").
		Parse(s)
	if err != nil {
		return "", fmt.Errorf("parse leaf: %w", err)
	}
	var buf bytes.Buffer
	if err := tpl.Execute(&buf, values); err != nil {
		return "", fmt.Errorf("execute leaf: %w", err)
	}
	return buf.String(), nil
}
