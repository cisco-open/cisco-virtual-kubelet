// Copyright 2026 Cisco Systems Inc.
// SPDX-License-Identifier: Apache-2.0
//go:build live

package catalystcenter

import (
	"context"
	"encoding/json"
	"errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"net/http"
	"os"
	"testing"
	"time"
)

type templateDuplicateStatusTrace struct {
	base   http.RoundTripper
	active bool
	path   string
	status int
}

func (g *templateDuplicateStatusTrace) RoundTrip(req *http.Request) (*http.Response, error) {
	resp, err := g.base.RoundTrip(req)
	if g.active && req.Method == http.MethodPost && req.URL.Path == g.path && resp != nil {
		g.status = resp.StatusCode
	}
	return resp, err
}

// A successful Create must not be an upsert of an already-existing name. Repeat
// exact successful request bodies only on generated fixtures and qualify that
// each duplicate is refused rather than returning successful ownership evidence.
func TestLiveCatalystCenterTemplateDuplicateCreate(t *testing.T) {
	if os.Getenv("CATC_TEMPLATE_DUPLICATES") != "isolated-fixtures" {
		t.Skip("isolated duplicate creation qualification not enabled")
	}
	c := liveClient(t)
	ctx, cancel := context.WithTimeout(context.Background(), 6*time.Minute)
	defer cancel()
	name := "CVK-Q-Duplicate-" + time.Now().UTC().Format("20060102-150405")
	t.Log("generated duplicate qualification fixture", name)
	projects, err := c.listTemplateProjects(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if _, exists := projects[name]; exists {
		t.Fatal("generated name already exists")
	}
	rest, _, err := c.session(ctx)
	if err != nil {
		t.Fatal(err)
	}
	transport := rest.HTTPClient.Transport
	if transport == nil {
		transport = http.DefaultTransport
	}
	trace := &templateDuplicateStatusTrace{base: transport}
	rest.HTTPClient.Transport = trace
	defer func() { rest.HTTPClient.Transport = transport }()
	projectID := ""
	ids := map[string]string{}
	settled := false
	wait := func(ctx context.Context, id string, submitted time.Time) error {
		for {
			done, err := c.configurationTaskTreeComplete(ctx, id, submitted, time.Now())
			if err != nil || done {
				return err
			}
			select {
			case <-ctx.Done():
				return ctx.Err()
			case <-time.After(time.Second):
			}
		}
	}
	receipt := func(raw []byte) Task {
		var body struct {
			Response Task `json:"response"`
		}
		if json.Unmarshal(raw, &body) != nil || !validAPIID(body.Response.ID) {
			t.Fatal("native receipt missing; no replay")
		}
		return body.Response
	}
	mutate := func(ctx context.Context, method, path string, payload map[string]any, kind, parent string) string {
		settled = false
		body, _ := json.Marshal(payload)
		submitted := time.Now()
		raw, err := c.mutate(ctx, method, path, body)
		if err != nil {
			t.Fatal("native fixture mutation failed; no replay")
		}
		task := receipt(raw)
		if err := wait(ctx, task.ID, submitted); err != nil {
			t.Fatal("native fixture task did not complete")
		}
		if kind == "" {
			settled = true
			return ""
		}
		fixture := templateCreatorReceiptFixture(kind)
		op := fixture.Operation.DeepCopy()
		project := name
		if kind == "p" {
			project = ""
		}
		op.Resource = templateCreatorResource(kind, project, payload["name"].(string)) + ":i"
		op.SourceUID = ""
		op.SourceVersion = ""
		op.ParentID = parent
		op.Phase = "Verifying"
		op.TaskID = task.ID
		op.SubmittedAt = nil
		// This is only a read-only task reference; no Kubernetes owner/receipt is
		// fabricated. The public ownership lifecycle is qualified separately.
		stamp := metav1.NewTime(submitted)
		op.SubmittedAt = &stamp
		id, done, err := c.templateCreateTaskResult(ctx, op, time.Now())
		if err != nil || !done {
			t.Fatal("fixture task did not return canonical NCTP identity")
		}
		settled = true
		return id
	}
	t.Cleanup(func() {
		if !settled || projectID == "" {
			t.Log("retained unsettled duplicate fixture", name)
			return
		}
		cleanup, stop := context.WithTimeout(context.Background(), 2*time.Minute)
		defer stop()
		remove := func(path string) bool {
			submitted := time.Now()
			raw, err := c.mutate(cleanup, http.MethodDelete, path, nil)
			if err != nil {
				t.Error("exact cleanup request failed")
				return false
			}
			task := receipt(raw)
			if err := wait(cleanup, task.ID, submitted); err != nil {
				t.Error("exact cleanup task failed")
				return false
			}
			return true
		}
		for _, child := range []string{"combined", "base"} {
			defs, err := c.listTemplateDefinitions(cleanup)
			if err != nil {
				t.Error(err)
				return
			}
			got, exists := defs[projectID+"#"+child]
			if !exists {
				continue
			}
			if got.ID != ids[child] {
				t.Error("cleanup refused changed UUID")
				return
			}
			if !remove(templatesPath + "/" + got.ID) {
				return
			}
		}
		raw, err := c.get(cleanup, templateProjectsPath+"/"+projectID, nil, http.StatusOK)
		var p struct {
			ID, Name  string
			Templates []json.RawMessage `json:"templates"`
		}
		if err != nil || json.Unmarshal(raw, &p) != nil || p.ID != projectID || p.Name != name || len(p.Templates) != 0 {
			t.Error("cleanup refused changed/populated project")
			return
		}
		if !remove(templateProjectsPath + "/" + projectID) {
			return
		}
		remaining, err := c.listTemplateProjects(cleanup)
		if err != nil {
			t.Error(err)
			return
		}
		if _, exists := remaining[name]; exists {
			t.Error("project cleanup not verified")
			return
		}
		t.Log("duplicate qualification exact template/project cleanup verified", name)
	})
	projectPayload := map[string]any{"name": name}
	projectID = mutate(ctx, http.MethodPost, templateProjectsPath, projectPayload, "p", "")
	settled = false
	projects, err = c.listTemplateProjects(ctx)
	if err != nil || projects[name].ID != projectID {
		t.Fatal("project task UUID did not match generated name")
	}
	settled = true
	common := func(name string) map[string]any {
		return map[string]any{"name": name, "language": "JINJA", "software_type": "IOS-XE", "device_types": []any{map[string]any{"product_family": "Switches and Hubs"}}}
	}
	base, combined := common("base"), common("combined")
	combined["composite"] = true
	combined["containing_templates"] = []string{"base"}
	raw, _ := json.Marshal(map[string]any{"catalyst_center": map[string]any{"templates": map[string]any{"projects": []any{map[string]any{"name": name, "dayn_templates": []any{base, combined}}}}}})
	intent, err := parseTemplateIntent(raw, map[string]string{"base.j2": "! isolated duplicate qualification\n"})
	if err != nil {
		t.Fatal(err)
	}
	baseDesired, combinedDesired := intent.definitions[0], intent.definitions[1]
	path := templateProjectsPath + "/" + projectID + "/template"
	ids["base"] = mutate(ctx, http.MethodPost, path, baseDesired.payload, "r", projectID)
	settled = false
	defs, err := c.listTemplateDefinitions(ctx)
	if err != nil || defs[projectID+"#base"].ID != ids["base"] {
		t.Fatal("regular task UUID did not match generated project/name")
	}
	settled = true
	mutate(ctx, http.MethodPost, templateVersionsPath, map[string]any{"templateId": ids["base"], "comments": "CVK isolated duplicate qualification"}, "", "")
	e := &templateExecutor{client: c, intent: intent}
	observed, err := e.observe(ctx)
	if err != nil {
		t.Fatal(err)
	}
	combinedPayload, _, ready, err := e.resolvedPayload(combinedDesired, observed)
	if err != nil || !ready {
		t.Fatal("committed member unavailable")
	}
	ids["combined"] = mutate(ctx, http.MethodPost, path, combinedPayload, "c", projectID)
	settled = false
	defs, err = c.listTemplateDefinitions(ctx)
	if err != nil || defs[projectID+"#combined"].ID != ids["combined"] {
		t.Fatal("composite task UUID did not match generated project/name")
	}
	settled = true
	for _, probe := range []struct {
		kind, path string
		payload    map[string]any
	}{{"project", templateProjectsPath, projectPayload}, {"regular", path, baseDesired.payload}, {"composite", path, combinedPayload}} {
		settled = false
		body, _ := json.Marshal(probe.payload)
		trace.active = true
		trace.path = probe.path
		trace.status = 0
		submitted := time.Now()
		raw, err := c.mutate(ctx, http.MethodPost, probe.path, body)
		trace.active = false
		if err != nil {
			if trace.status != http.StatusBadRequest && trace.status != http.StatusConflict && trace.status != http.StatusUnprocessableEntity && trace.status != http.StatusInternalServerError {
				t.Fatal("duplicate refusal was not a qualified controller rejection", probe.kind, trace.status)
			}
			t.Log("duplicate Create refused before task", probe.kind, "HTTP", trace.status)
		} else {
			task := receipt(raw)
			err = wait(ctx, task.ID, submitted)
			var failure *configurationTaskFailure
			if !errors.As(err, &failure) {
				t.Fatal("duplicate Create did not fail its original task; ownership semantics require investigation", probe.kind)
			}
			t.Log("duplicate Create refused by original task", probe.kind, task.ID)
		}
		// A rejected task must not leave a same-name second object or replace an ID.
		projects, err := c.listTemplateProjects(ctx)
		if err != nil || projects[name].ID != projectID {
			t.Fatal("duplicate changed project identity")
		}
		defs, err := c.listTemplateDefinitions(ctx)
		if err != nil {
			t.Fatal("duplicate created ambiguous native names")
		}
		for _, child := range []string{"base", "combined"} {
			if defs[projectID+"#"+child].ID != ids[child] {
				t.Fatal("duplicate changed template identity")
			}
		}
		observed, err = e.observe(ctx)
		if err != nil || !templateDefinitionMatches(baseDesired.payload, observed[baseDesired.identity.Key()].draft) || !templateDefinitionMatches(combinedPayload, observed[combinedDesired.identity.Key()].draft) {
			t.Fatal("duplicate changed declared template state")
		}
		settled = true
	}
	t.Log("project/regular/composite duplicate Creates refused; no successful task UUID could authorize adoption")
}
