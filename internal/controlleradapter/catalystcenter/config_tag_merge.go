// Copyright © 2026 Cisco Systems Inc.
// Licensed under the Apache License, Version 2.0 (the "License");
package catalystcenter

// normalizeReportTags implements the pinned model's name-keyed tag union.
// Inventory overrides template definitions. Report each final definition once,
// attributing an overridden tag to inventory. Both raw lists are validated first
// so an overridden malformed entry does not hide unsupported intent.
func normalizeReportTags(root map[string]any) (bool, error) {
	templates, tok := root["templates"].(map[string]any)
	inventory, iok := root["inventory"].(map[string]any)
	if !tok || !iok {
		return false, nil
	}
	tv, tp := templates["tags"]
	iv, ip := inventory["tags"]
	if !tp || !ip {
		return false, nil
	}
	tt, err := parseTemplateTags(tv)
	if err != nil {
		return false, err
	}
	it, err := parseTemplateTags(iv)
	if err != nil {
		return false, err
	}
	names := map[string]bool{}
	for _, t := range it {
		names[t.name] = true
	}
	values := tv.([]any)
	keep := []any{}
	for i, t := range tt {
		if !names[t.name] {
			keep = append(keep, values[i])
		}
	}
	if len(keep) == len(values) {
		return false, nil
	}
	if len(keep) > 0 {
		templates["tags"] = keep
		return false, nil
	}
	delete(templates, "tags")
	return len(templates) == 0, nil
}
