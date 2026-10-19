// Copyright © 2026 Cisco Systems Inc.
// Licensed under the Apache License, Version 2.0 (the "License");
package catalystcenter

import (
	"context"
	"fmt"
	"net/http"
	"strings"
	"testing"
)

const combinedTagsModel = `{"catalyst_center":{"templates":{"tags":[{"name":"shared","description":"template"}]},"inventory":{"tags":[{"name":"shared","description":"inventory"}]}}}`

func TestCombinedTagsUseInventoryPrecedence(t *testing.T) {
	for _, sections := range [][]string{{"templates", "inventory"}, {"inventory", "templates"}} {
		actual := "inventory"
		reads := 0
		c := testClient(t, func(w http.ResponseWriter, r *http.Request) {
			if r.URL.Path == authPath {
				fmt.Fprint(w, `{"Token":"test"}`)
				return
			}
			if r.Method != "GET" || r.URL.Path != tagsPath {
				t.Error("unexpected API")
				w.WriteHeader(400)
				return
			}
			reads++
			fmt.Fprintf(w, `{"response":[{"id":"tag","name":"shared","description":%q,"systemTag":false}]}`, actual)
		})
		plans, err := c.compileConfigReports([]byte(combinedTagsModel), sections)
		if err != nil || len(plans) != 2 || reads != 0 {
			t.Fatal("combined compile", err)
		}
		count := 0
		for _, p := range plans {
			count += p.count
			d, err := p.observe(context.Background())
			if err != nil || len(d) != 0 {
				t.Fatal("shadowed template produced false drift", err)
			}
		}
		if count != 1 || reads != 1 {
			t.Fatal("shared tag was counted or read twice", count, reads)
		}
		actual = "template"
		entries := 0
		for _, p := range plans {
			d, err := p.observe(context.Background())
			if err != nil {
				t.Fatal(err)
			}
			for _, entry := range d {
				entries++
				if entry.Section != "inventory" {
					t.Fatal("incorrect merge ownership")
				}
			}
		}
		if entries != 1 {
			t.Fatal("expected exactly one effective tag drift", entries)
		}
	}
}
func TestCombinedTagsRetainUnshadowedAndValidateBothLists(t *testing.T) {
	raw := strings.Replace(combinedTagsModel, `"description":"template"}`, `"description":"template"},{"name":"template-only"}`, 1)
	plans, err := (&client{}).compileConfigReports([]byte(raw), []string{"templates", "inventory"})
	if err != nil || plans[0].count != 1 || plans[1].count != 1 {
		t.Fatal("unshadowed tag lost", err)
	}
	invalid := strings.Replace(combinedTagsModel, `"description":"template"`, `"unsupported":true`, 1)
	if _, err := (&client{}).compileConfigReports([]byte(invalid), []string{"templates", "inventory"}); err == nil {
		t.Fatal("shadowed unsupported field ignored")
	}
	if _, err := (&client{}).compileConfigReports([]byte(combinedTagsModel), []string{"templates"}); err == nil {
		t.Fatal("unselected inventory silently merged")
	}
}
