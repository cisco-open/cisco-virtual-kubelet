// Copyright © 2026 Cisco Systems Inc.
// Licensed under the Apache License, Version 2.0 (the "License");
package nacmodel

import "testing"

func TestTemplateContents(t *testing.T) {
	index, err := IndexTemplates([]byte(referenceModel))
	if err != nil {
		t.Fatal(err)
	}
	files := map[string]string{"shared.j2": "default\r\n", "Two#shared.vtl": "override\r\n", "unique.vlt": "unique"}
	got, err := index.ResolveTemplateContents(files)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 3 || got["One#shared"] != "default\n" || got["Two#shared"] != "override\n" || got["One#unique"] != "unique" {
		t.Fatal("content resolution differs from upstream")
	}
	for _, extra := range []string{"../shared.j2", "shared.vtl", "bad.txt", ".j2", "dir\\file.j2"} {
		invalid := map[string]string{}
		for k, v := range files {
			invalid[k] = v
		}
		invalid[extra] = ""
		if _, err := index.ResolveTemplateContents(invalid); err == nil {
			t.Fatal("invalid asset accepted")
		}
	}
	delete(files, "unique.vlt")
	if _, err := index.ResolveTemplateContents(files); err == nil {
		t.Fatal("missing content accepted")
	}
}

func TestTemplateAssetJSON(t *testing.T) {
	files, err := ParseTemplateAssets([]byte(`{"Project#body.j2":"line\r\n"}`))
	if err != nil || files["Project#body.j2"] != "line\r\n" {
		t.Fatal("asset parsing failed", err)
	}
	for _, raw := range []string{`null`, `[]`, `{"body.j2":null}`, `{"body.j2":1}`, `{"body.j2":"first","body.j2":"second"}`, `{"../body.j2":"content"}`, `{"body.j2":"content","body.vtl":"other"}`} {
		if _, err := ParseTemplateAssets([]byte(raw)); err == nil {
			t.Fatal("invalid asset map accepted")
		}
	}
}
