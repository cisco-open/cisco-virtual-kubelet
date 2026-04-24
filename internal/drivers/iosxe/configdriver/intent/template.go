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
	"crypto/sha256"
	"fmt"
	"net"
	"strconv"
	"text/template"

	gonja "github.com/nikolalohinski/gonja/v2"
	gonjaconfig "github.com/nikolalohinski/gonja/v2/config"
	"github.com/nikolalohinski/gonja/v2/exec"
	"github.com/nikolalohinski/gonja/v2/loaders"
	"sigs.k8s.io/yaml"

	configv1alpha1 "github.com/cisco/virtual-kubelet-cisco/api/config/v1alpha1"
)

// ExpandTemplate renders a data-model IOSXETemplate with the
// supplied values and returns the netascode fragment the result
// represents. CLI-type templates go through ExpandCLITemplate
// instead — a separate entry point because the output shape is a
// CLI text block rather than a merged netascode map.
//
// Both entry points:
//   - validate every declared parameter against its type before
//     substitution;
//   - reject values supplied for parameters not in the template's
//     declared set, so a typo produces an error rather than a
//     silent empty interpolation;
//   - use text/template with missingkey=error so a stray
//     {{ .unknown }} fails loudly.
func ExpandTemplate(tpl *configv1alpha1.IOSXETemplate, values map[string]string) (map[string]any, error) {
	if tpl == nil {
		return nil, fmt.Errorf("ExpandTemplate: nil template")
	}

	// Type is an optional field defaulted to "data-model" at the API
	// server (kubebuilder default); defaulting here too covers the
	// in-process path where the defaulter has not been applied (unit
	// tests, fake clients).
	kind := tpl.Spec.Type
	if kind == "" {
		kind = configv1alpha1.DataModelTemplate
	}
	if kind == configv1alpha1.CLITemplate {
		return nil, fmt.Errorf(
			"template %s: spec.type=cli — use ExpandCLITemplate for CLI templates",
			tpl.Name)
	}
	if kind != configv1alpha1.DataModelTemplate {
		return nil, fmt.Errorf(
			"template %s: unknown spec.type %q", tpl.Name, kind)
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

// ExpandCLITemplate renders a CLI-type IOSXETemplate to a CLI text
// block suitable for push via the Cisco-IA cli-config-data RPC.
// The template body is treated as a whole-file Jinja template:
// parameters substitute into a multi-line CLI string; each
// non-empty line becomes one <cmd> at transport time. Callers
// hold the returned string on ResolvedIntent.CLIBlocks; the
// engine builds a single transport.Op with VerbCLI per block
// and hands it to the transport.
//
// Why Jinja for CLI (and not for data-model): NAC operators author
// CLI templates in Jinja today (Ansible/Nornir/Nautobot ecosystem).
// The engine is pongo2 (pure-Go Jinja2-compatible). Data-model
// templates stay on Go text/template because they render into
// structured YAML and don't benefit from Jinja's text-oriented
// control flow.
//
// Why a separate entry point from ExpandTemplate:
//   - The output is a string, not a map — CLI blocks do not
//     merge into the netascode intent tree.
//   - The render engines differ. Parameter resolution and type
//     validation are still shared via resolveParameters.
func ExpandCLITemplate(tpl *configv1alpha1.IOSXETemplate, values map[string]string) (string, error) {
	if tpl == nil {
		return "", fmt.Errorf("ExpandCLITemplate: nil template")
	}
	kind := tpl.Spec.Type
	if kind != configv1alpha1.CLITemplate {
		return "", fmt.Errorf(
			"template %s: spec.type=%q — expected cli", tpl.Name, kind)
	}

	resolved, err := resolveParameters(tpl.Spec.Parameters, values)
	if err != nil {
		return "", fmt.Errorf("template %s: %w", tpl.Name, err)
	}

	// CLI template bodies are stored on the RawExtension as JSON.
	// We accept two shapes to keep operator ergonomics sensible:
	//
	//   1. A JSON string: "interface Loopback0\n ip address ..."
	//      — the raw CLI as a quoted YAML string.
	//   2. A mapping with a "cli" key: {"cli": "interface ..."}
	//      — useful when the operator wants structured metadata
	//      alongside the CLI (the mapping shape lets future
	//      fields grow without a breaking change).
	body, err := decodeCLIBody(tpl.Spec.Configuration.Raw, tpl.Name)
	if err != nil {
		return "", err
	}

	rendered, err := renderJinjaCLI(body, coerceForJinja(tpl.Spec.Parameters, resolved))
	if err != nil {
		return "", fmt.Errorf("template %s: render cli: %w", tpl.Name, err)
	}
	return rendered, nil
}

// cliJinjaConfig is a per-package gonja config with StrictUndefined
// turned on so a stray `{{ unknown }}` in a CLI body errors out
// instead of silently rendering as the empty string. We deliberately
// don't mutate gonja.DefaultConfig — that's a process-global and we
// have no business reaching across packages to flip it.
var cliJinjaConfig = func() *gonjaconfig.Config {
	c := gonjaconfig.New()
	c.StrictUndefined = true
	return c
}()

// coerceForJinja upgrades the string-valued parameter map into a
// typed map so `{% if enabled %}` and arithmetic on int parameters
// behave as operators expect when the parameter is declared bool or
// int. Strings (the default) and network-address types pass through
// untouched — gonja treats them as regular strings.
func coerceForJinja(declared []configv1alpha1.TemplateParameter, values map[string]string) map[string]any {
	typed := make(map[string]configv1alpha1.TemplateParameterType, len(declared))
	for _, p := range declared {
		typed[p.Name] = p.Type
	}
	ctx := make(map[string]any, len(values))
	for k, v := range values {
		switch typed[k] {
		case configv1alpha1.TemplateParameterInt:
			if n, err := strconv.ParseInt(v, 10, 64); err == nil {
				ctx[k] = n
				continue
			}
		case configv1alpha1.TemplateParameterBool:
			if b, err := strconv.ParseBool(v); err == nil {
				ctx[k] = b
				continue
			}
		}
		ctx[k] = v
	}
	return ctx
}

// renderJinjaCLI is the gonja entry point for CLI templates.
// We construct the template against cliJinjaConfig (strict-undefined)
// rather than calling gonja.FromString, which would pick up
// gonja.DefaultConfig and lose the strictness guarantee.
func renderJinjaCLI(body string, values map[string]any) (string, error) {
	rootID := fmt.Sprintf("cli-template-%x",
		sha256.Sum256([]byte(body)))
	fsLoader, err := loaders.NewFileSystemLoader("")
	if err != nil {
		return "", fmt.Errorf("jinja loader: %w", err)
	}
	loader, err := loaders.NewShiftedLoader(rootID, bytes.NewReader([]byte(body)), fsLoader)
	if err != nil {
		return "", fmt.Errorf("jinja loader: %w", err)
	}
	tpl, err := exec.NewTemplate(rootID, cliJinjaConfig, loader, gonja.DefaultEnvironment)
	if err != nil {
		return "", fmt.Errorf("parse jinja: %w", err)
	}
	out, err := tpl.ExecuteToString(exec.NewContext(values))
	if err != nil {
		return "", fmt.Errorf("execute jinja: %w", err)
	}
	return out, nil
}

// decodeCLIBody extracts the CLI text from either a JSON string or
// a {"cli": "..."} mapping. Anything else fails with a clear
// diagnostic — we don't silently stringify arbitrary structures.
func decodeCLIBody(raw []byte, tplName string) (string, error) {
	if len(raw) == 0 {
		return "", fmt.Errorf("template %s: empty configuration body", tplName)
	}
	var asString string
	if err := yaml.Unmarshal(raw, &asString); err == nil && asString != "" {
		return asString, nil
	}
	var asMap map[string]any
	if err := yaml.Unmarshal(raw, &asMap); err == nil {
		if cli, ok := asMap["cli"].(string); ok && cli != "" {
			return cli, nil
		}
	}
	return "", fmt.Errorf(
		"template %s: CLI body must be a string or {\"cli\": \"...\"} mapping",
		tplName)
}
