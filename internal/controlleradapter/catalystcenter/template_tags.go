// Copyright © 2026 Cisco Systems Inc.
// Licensed under the Apache License, Version 2.0 (the "License");
package catalystcenter

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	configapi "github.com/cisco/virtual-kubelet-cisco/api/config/v1alpha1"
	ciskov1 "github.com/cisco/virtual-kubelet-cisco/api/v1alpha1"
	"net/http"
	"net/url"
	"reflect"
	"strings"
	"sync/atomic"
	"time"
)

const tagsPath = "/dna/intent/api/v1/tag"

func validTemplateTagName(name string) bool {
	return name != "" && strings.TrimSpace(name) == name && len(name) <= 256 && !strings.ContainsAny(name, "\x00\r\n")
}

type desiredTag struct {
	name    string
	payload map[string]any
}
type observedTag struct {
	ID           string  `json:"id"`
	Name         string  `json:"name"`
	Description  *string `json:"description"`
	SystemTag    *bool   `json:"systemTag"`
	DynamicRules []any   `json:"dynamicRules"`
}

func parseTemplateTags(value any) ([]desiredTag, error) {
	list, ok := value.([]any)
	if !ok || len(list) > 1000 {
		return nil, errors.New("invalid tag list")
	}
	out := []desiredTag{}
	seen := map[string]bool{}
	for _, entry := range list {
		m, ok := entry.(map[string]any)
		if !ok {
			return nil, errors.New("invalid tag")
		}
		name, ok := m["name"].(string)
		if !ok || !validTemplateTagName(name) || seen[name] {
			return nil, errors.New("invalid or duplicate tag name")
		}
		seen[name] = true
		p := map[string]any{"name": name}
		for key, value := range m {
			switch key {
			case "name":
			case "description":
				s, ok := value.(string)
				if !ok {
					return nil, errors.New("invalid tag description")
				}
				p[key] = s
			case "system_tag":
				b, ok := value.(bool)
				if !ok {
					return nil, errors.New("invalid system tag flag")
				}
				p["systemTag"] = b
			case "dynamic_rules":
				rules, ok := value.([]any)
				if !ok || len(rules) > 1000 {
					return nil, errors.New("invalid dynamic tag rules")
				}
				translated := []any{}
				for _, rule := range rules {
					r, ok := rule.(map[string]any)
					if !ok {
						return nil, errors.New("invalid dynamic rule")
					}
					member, ok := r["member_type"].(string)
					if !ok || member == "" {
						return nil, errors.New("dynamic rule member type required")
					}
					conditions := map[string]any{}
					for field, value := range r {
						switch field {
						case "member_type":
						case "operation", "name", "value":
							s, ok := value.(string)
							if !ok {
								return nil, errors.New("invalid rule string")
							}
							conditions[field] = s
						case "values":
							list, ok := value.([]any)
							if !ok {
								return nil, errors.New("invalid rule values")
							}
							for _, v := range list {
								if _, ok := v.(string); !ok {
									return nil, errors.New("invalid rule value")
								}
							}
							conditions[field] = list
						case "items":
							list, ok := value.([]any)
							if !ok {
								return nil, errors.New("invalid rule items")
							}
							items := []any{}
							for _, v := range list {
								object, ok := v.(map[string]any)
								if !ok {
									return nil, errors.New("invalid rule item")
								}
								item, err := templateStringFields(object, map[string]string{"operation": "operation", "name": "name", "value": "value"})
								if err != nil {
									return nil, err
								}
								items = append(items, item)
							}
							conditions[field] = items
						default:
							return nil, errors.New("unsupported dynamic rule attribute")
						}
					}
					_, singleValue := conditions["value"]
					_, multipleValues := conditions["values"]
					if singleValue && multipleValues {
						return nil, errors.New("dynamic tag rule must select value or values, not both")
					}
					if conditions["operation"] == "AND" || conditions["operation"] == "OR" {
						items, ok := conditions["items"].([]any)
						if !ok || len(items) == 0 {
							return nil, errors.New("dynamic logical rules require at least one condition")
						}
						if len(items) == 1 {
							// A unary conjunction/disjunction has its leaf's exact
							// meaning. Emit that native form: the appliance rejects
							// a one-item AND with an internal indexing error.
							leaf := items[0].(map[string]any)
							operation, _ := leaf["operation"].(string)
							name, _ := leaf["name"].(string)
							_, value := leaf["value"].(string)
							if len(conditions) != 2 || len(leaf) != 3 || name == "" || !value || (operation != "EQ" && operation != "LIKE" && operation != "ILIKE" && operation != "IN") {
								return nil, errors.New("single-condition logical rule requires an unambiguous atomic name/value condition")
							}
							conditions = leaf
						}
					}
					if len(conditions) == 0 {
						return nil, errors.New("empty dynamic rule")
					}
					translated = append(translated, map[string]any{"memberType": member, "rules": conditions})
				}
				p["dynamicRules"] = translated
			default:
				return nil, errors.New("unsupported tag attribute")
			}
		}
		out = append(out, desiredTag{name: name, payload: p})
	}
	return out, nil
}
func (c *client) listTags(ctx context.Context) (map[string]observedTag, error) {
	get := func(ctx context.Context, path string, q url.Values) ([]byte, error) { return c.get(ctx, path, q, 200) }
	tags, err := listInventoryQueryWithGet(ctx, get, tagsPath, url.Values{}, func(t observedTag) string { return t.ID })
	if err != nil {
		return nil, err
	}
	out := map[string]observedTag{}
	for _, tag := range tags {
		if tag.Name == "" {
			return nil, errInvalidResponse
		}
		if _, exists := out[tag.Name]; exists {
			return nil, errInvalidResponse
		}
		out[tag.Name] = tag
	}
	return out, nil
}
func tagPayload(tag observedTag) map[string]any {
	out := map[string]any{"name": tag.Name}
	if tag.Description != nil {
		out["description"] = *tag.Description
	}
	if tag.SystemTag != nil {
		out["systemTag"] = *tag.SystemTag
	}
	if tag.DynamicRules != nil {
		out["dynamicRules"] = tag.DynamicRules
	}
	return out
}

// Normalize empty native rule representations only when the manifest explicitly
// selects an empty rule list. Never add a clearing field to an update that leaves
// dynamic rules unmanaged.
func tagPayloadMatches(want map[string]any, got observedTag) bool {
	actual := tagPayload(got)
	if rules, selected := want["dynamicRules"].([]any); selected && len(rules) == 0 && got.DynamicRules == nil {
		actual["dynamicRules"] = []any{}
	}
	return templateValueMatches(want, actual)
}

func (c *client) reportTags(ctx context.Context, want []desiredTag) ([]configapi.NetworkControllerDriftEntry, error) {
	observed, err := c.listTags(ctx)
	if err != nil {
		return nil, err
	}
	out := []configapi.NetworkControllerDriftEntry{}
	for _, w := range want {
		got, exists := observed[w.name]
		change := configapi.NetworkControllerDriftChangeAdd
		if exists {
			if tagPayloadMatches(w.payload, got) {
				continue
			}
			change = configapi.NetworkControllerDriftChangeUpdate
		}
		out = append(out, configapi.NetworkControllerDriftEntry{Section: "templates", Resource: w.name, Path: "/tags", Change: change})
	}
	return out, nil
}

type tagExecutor struct {
	submissionGuard func(context.Context, string, string, configurationResourceIdentity) error
	client          *client
	desired         []desiredTag
}

func (e *tagExecutor) count() int { return len(e.desired) }

type tagMutation struct {
	resource, action, target, method string
	body                             []byte
}

func planTagMutation(desired []desiredTag, observed map[string]observedTag) (*tagMutation, error) {
	for _, w := range desired {
		if got, exists := observed[w.name]; exists && !tagPayloadMatches(w.payload, got) && (got.SystemTag == nil || *got.SystemTag) {
			return nil, errors.New("tag protection state is missing or system-owned")
		}
	}
	for _, w := range desired {
		got, exists := observed[w.name]
		if exists && tagPayloadMatches(w.payload, got) {
			continue
		}
		if exists && got.SystemTag != nil && *got.SystemTag {
			return nil, errors.New("system tag mutation requires separate qualification")
		}
		payload := map[string]any{}
		if exists {
			payload = tagPayload(got)
			payload["id"] = got.ID
		}
		for k, v := range w.payload {
			payload[k] = v
		}
		body, err := json.Marshal(payload)
		if err != nil {
			return nil, err
		}
		method, action := http.MethodPost, "Create"
		if exists {
			method, action = http.MethodPut, "Update"
		}
		return &tagMutation{resource: w.name, action: action, target: got.ID, method: method, body: body}, nil
	}
	return nil, nil
}

func (e *tagExecutor) next(ctx context.Context) (*configurationWrite, error) {
	// A compiled executor's mutable maps must not change an already planned
	// request or its later whole-selection revalidation.
	desired := make([]desiredTag, len(e.desired))
	for i, tag := range e.desired {
		raw, err := json.Marshal(tag.payload)
		if err != nil {
			return nil, err
		}
		var payload map[string]any
		if json.Unmarshal(raw, &payload) != nil || payload == nil {
			return nil, errInvalidResponse
		}
		desired[i] = desiredTag{name: tag.name, payload: payload}
	}
	observed, err := e.client.listTags(ctx)
	if err != nil {
		return nil, err
	}
	mutation, err := planTagMutation(desired, observed)
	if err != nil || mutation == nil {
		return nil, err
	}
	var used atomic.Bool
	return &configurationWrite{resource: mutation.resource, action: mutation.action, identity: configurationResourceIdentity{target: mutation.target}, submit: func(ctx context.Context) (Task, error) {
		if !used.CompareAndSwap(false, true) {
			return Task{}, errConfigurationTransition
		}
		// Pagination does not provide an atomic catalog snapshot. Repeat the
		// complete read so changes to an earlier page during the first fresh
		// read cannot authorize a stale write. Only declared rows are compared;
		// unrelated catalog activity does not invalidate this operation.
		for observation := 0; observation < 2; observation++ {
			fresh, err := e.client.listTags(ctx)
			if err != nil {
				return Task{}, err
			}
			for _, want := range desired {
				before, existed := observed[want.name]
				now, exists := fresh[want.name]
				if existed != exists || !reflect.DeepEqual(before, now) {
					return Task{}, errConfigurationStale
				}
			}
			planned, err := planTagMutation(desired, fresh)
			if err != nil {
				return Task{}, err
			}
			if planned == nil || planned.resource != mutation.resource || planned.action != mutation.action || planned.target != mutation.target || planned.method != mutation.method || !bytes.Equal(planned.body, mutation.body) {
				return Task{}, errConfigurationStale
			}
		}
		if e.submissionGuard != nil {
			if err := e.submissionGuard(ctx, mutation.resource, mutation.action, configurationResourceIdentity{target: mutation.target}); err != nil {
				return Task{}, err
			}
		}
		raw, err := e.client.mutate(ctx, mutation.method, tagsPath, mutation.body)
		if err != nil {
			return Task{}, err
		}
		var receipt struct {
			Response Task `json:"response"`
		}
		if json.Unmarshal(raw, &receipt) != nil || !validAPIID(receipt.Response.ID) {
			return Task{}, errInvalidResponse
		}
		return Task{ID: receipt.Response.ID}, nil
	}}, nil
}
func (e *tagExecutor) verify(ctx context.Context, op *ciskov1.NetworkControllerConfigurationOperation) (bool, error) {
	if op == nil {
		return false, errConfigurationTransition
	}
	if op.ParentID != "" || (op.Action != "Create" && op.Action != "Update") || (op.Action == "Update" && !validAPIID(op.TargetID)) {
		return false, errConfigurationTransition
	}
	createdID := ""
	if op.Action == "Create" {
		var complete bool
		var err error
		createdID, complete, err = e.client.tagCreateTaskResult(ctx, op, time.Now())
		if err != nil || !complete {
			return false, err
		}
	}
	observed, err := e.client.listTags(ctx)
	if err != nil {
		return false, err
	}
	for _, w := range e.desired {
		if w.name == op.Resource {
			got, exists := observed[w.name]
			if !exists {
				return false, nil
			}
			if createdID != "" && got.ID != createdID {
				return false, errConfigurationStale
			}
			if op.TargetID != "" && op.TargetID != got.ID {
				return false, errors.New("tag identity changed")
			}
			return tagPayloadMatches(w.payload, got), nil
		}
	}
	return false, errConfigurationTransition
}

// Bind a newly created tag to the original task result. A matching name or a
// completed generic task alone cannot attest which native object was created.
// This observation does not publish ownership or authorize deletion/adoption.
func (c *client) tagCreateTaskResult(ctx context.Context, op *ciskov1.NetworkControllerConfigurationOperation, now time.Time) (string, bool, error) {
	if op == nil || op.Action != "Create" || op.TargetID != "" || op.ParentID != "" || !validAPIID(op.TaskID) || op.SubmittedAt == nil || op.SubmittedAt.IsZero() {
		return "", false, errConfigurationTransition
	}
	raw, err := c.get(ctx, taskPath+op.TaskID, nil, http.StatusOK)
	if err != nil {
		return "", false, err
	}
	done, err := decodeConfigurationTaskComplete(raw, op.TaskID, op.SubmittedAt.Time, now)
	if err != nil || !done {
		return "", false, err
	}
	var body struct {
		Response struct {
			Service string `json:"serviceType"`
			Data    string `json:"data"`
		} `json:"response"`
	}
	if json.Unmarshal(raw, &body) != nil || body.Response.Service != "Grouping Service" || !canonicalTemplateUUID(body.Response.Data) {
		return "", false, errInvalidResponse
	}
	return body.Response.Data, true, nil
}
