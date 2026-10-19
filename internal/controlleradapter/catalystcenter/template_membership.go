// Copyright © 2026 Cisco Systems Inc.
// Licensed under the Apache License, Version 2.0 (the "License");
package catalystcenter

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	configapi "github.com/cisco/virtual-kubelet-cisco/api/config/v1alpha1"
	ciskov1 "github.com/cisco/virtual-kubelet-cisco/api/v1alpha1"
	"net/http"
	"net/url"
	"reflect"
	"strings"
	"sync/atomic"
)

const templateMembershipBindingPrefix = "template-tag-association:v2:"

var errTemplateMembershipProjectBinding = errors.New("template association requires its original journaled project binding")

// The operation already retains template/tag UUIDs in target/parent. Bind the
// third identity, the project UUID, into its immutable resource key instead of
// widening the physical-deployment version graph or introducing another CRD.
func templateMembershipBoundResource(m tagMembership) (string, error) {
	if !validAPIID(m.projectID) || m.resource == "" {
		return "", errTemplateMembershipProjectBinding
	}
	raw, _ := json.Marshal([]string{m.resource, m.projectID})
	return fmt.Sprintf("%s%x", templateMembershipBindingPrefix, sha256.Sum256(raw)), nil
}

func validTemplateMembershipBoundResource(resource string) bool {
	if !strings.HasPrefix(resource, templateMembershipBindingPrefix) {
		return false
	}
	hash := strings.TrimPrefix(resource, templateMembershipBindingPrefix)
	return len(hash) == 64 && strings.IndexFunc(hash, func(r rune) bool { return !(r >= '0' && r <= '9' || r >= 'a' && r <= 'f') }) < 0
}

// The public template compiler nests associations under members/, with one
// additional templates/ group when combined with discoveries. Old Verified
// operations without a project binding must not bypass recovery by releasing.
func templateMembershipJournalBinding(section, resource string) (selected, valid bool) {
	if section != "templates" {
		return false, false
	}
	resource = strings.TrimPrefix(resource, "templates/")
	if strings.HasPrefix(resource, "members/") {
		return true, validTemplateMembershipBoundResource(strings.TrimPrefix(resource, "members/"))
	}
	return strings.HasPrefix(resource, templateMembershipBindingPrefix), validTemplateMembershipBoundResource(resource)
}

func tagMembershipIdentity(template, tag string) string {
	raw, _ := json.Marshal([]string{template, tag})
	return fmt.Sprintf("%s/%x", tag, sha256.Sum256(raw))
}
func (c *client) templateTagMembers(ctx context.Context, id string) (map[string]bool, error) {
	return c.tagMembers(ctx, id, "template")
}
func (c *client) tagMembers(ctx context.Context, id, memberType string) (map[string]bool, error) {
	return c.tagMembersByAssociation(ctx, id, memberType, "")
}

// Keep unfiltered membership reads unchanged. Explicit association filters are
// evidence about provenance; MIXED is its own class, not a union selector.
func (c *client) tagMembersByAssociation(ctx context.Context, id, memberType, association string) (map[string]bool, error) {
	if association != "" && association != "DYNAMIC" && association != "STATIC" && association != "MIXED" {
		return nil, errConfigurationTransition
	}
	if memberType != "template" && memberType != "networkdevice" {
		return nil, errConfigurationTransition
	}
	if !validAPIID(id) {
		return nil, errInvalidResponse
	}
	type member struct {
		ID string `json:"instanceUuid"`
	}
	get := func(ctx context.Context, path string, q url.Values) ([]byte, error) { return c.get(ctx, path, q, 200) }
	query := url.Values{"memberType": {memberType}}
	if association != "" {
		query.Set("memberAssociationType", association)
	}
	members, err := listInventoryQueryWithGet(ctx, get, tagsPath+"/"+id+"/member", query, func(m member) string { return m.ID })
	if err != nil {
		return nil, err
	}
	result := map[string]bool{}
	for _, m := range members {
		if !validAPIID(m.ID) {
			return nil, errInvalidResponse
		}
		result[m.ID] = true
	}
	return result, nil
}

// Explicit declarations require a manual association. Rule-derived presence
// alone cannot satisfy intent that must survive a later rule change.
func (c *client) tagExplicitDeviceMembers(ctx context.Context, id string) (map[string]bool, error) {
	return c.tagExplicitMembers(ctx, id, "networkdevice")
}
func (c *client) tagExplicitTemplateMembers(ctx context.Context, id string) (map[string]bool, error) {
	return c.tagExplicitMembers(ctx, id, "template")
}
func (c *client) tagExplicitMembers(ctx context.Context, id, memberType string) (map[string]bool, error) {
	result := map[string]bool{}
	for _, association := range []string{"STATIC", "MIXED"} {
		members, err := c.tagMembersByAssociation(ctx, id, memberType, association)
		if err != nil {
			return nil, err
		}
		for member := range members {
			result[member] = true
		}
	}
	return result, nil
}

type tagMembership struct {
	resource, tagID, memberID    string
	projectID                    string
	declared, present, protected bool
}
type templateMembershipExecutor struct {
	client *client
	intent templateIntent
}

func (e *templateMembershipExecutor) count() int {
	n := 0
	for _, d := range e.intent.definitions {
		n += len(d.tags)
	}
	return n
}
func (e *templateMembershipExecutor) observe(ctx context.Context) ([]tagMembership, error) {
	tags, err := e.client.listTags(ctx)
	if err != nil {
		return nil, err
	}
	projects, err := e.client.listTemplateProjects(ctx)
	if err != nil {
		return nil, err
	}
	definitions, err := e.client.listTemplateDefinitions(ctx)
	if err != nil {
		return nil, err
	}
	declared := map[string]bool{}
	for _, t := range e.intent.tags {
		declared[t.name] = true
	}
	members := map[string]map[string]bool{}
	out := []tagMembership{}
	for _, d := range e.intent.definitions {
		for _, name := range d.tags {
			tag, exists := tags[name]
			template := definitions[projects[d.identity.Project].ID+"#"+d.identity.Name]
			if exists {
				if _, read := members[tag.ID]; !read {
					members[tag.ID], err = e.client.tagExplicitTemplateMembers(ctx, tag.ID)
					if err != nil {
						return nil, err
					}
				}
			}
			out = append(out, tagMembership{resource: tagMembershipIdentity(d.identity.Key(), name), tagID: tag.ID, memberID: template.ID, projectID: projects[d.identity.Project].ID, declared: declared[name], present: exists && template.ID != "" && members[tag.ID][template.ID], protected: exists && (tag.SystemTag == nil || *tag.SystemTag)})
		}
	}
	return out, nil
}
func (e *templateMembershipExecutor) next(ctx context.Context) (*configurationWrite, error) {
	// Only identity/tag selectors are used by this observer. Do not retain a
	// mutable definition slice, tag list, or unrelated template content.
	intent := templateIntent{tags: make([]desiredTag, len(e.intent.tags)), definitions: make([]desiredTemplate, len(e.intent.definitions))}
	for i, tag := range e.intent.tags {
		intent.tags[i].name = tag.name
	}
	for i, definition := range e.intent.definitions {
		identity := definition.identity
		identity.Members = nil // Composite content is not consumed by this observer.
		intent.definitions[i] = desiredTemplate{identity: identity, tags: append([]string(nil), definition.tags...)}
	}
	frozen := &templateMembershipExecutor{client: e.client, intent: intent}
	observed, err := frozen.observe(ctx)
	if err != nil {
		return nil, err
	}
	return nextTagMembership(frozen.client, "template", observed, frozen.observe)
}
func nextTagMembership(c *client, memberType string, observed []tagMembership, reobserve func(context.Context) ([]tagMembership, error)) (*configurationWrite, error) {
	if reobserve == nil || (memberType != "template" && memberType != "networkdevice") {
		return nil, errConfigurationTransition
	}
	for _, m := range observed {
		if m.tagID != "" && !validAPIID(m.tagID) || m.memberID != "" && !validAPIID(m.memberID) {
			return nil, errInvalidResponse
		}
		if memberType == "template" && (m.projectID != "" && !validAPIID(m.projectID) || m.memberID != "" && m.projectID == "") {
			return nil, errTemplateMembershipProjectBinding
		}
		if m.tagID == "" && !m.declared {
			return nil, errors.New("external tag is missing")
		}
		if m.protected && !m.present {
			return nil, errors.New("association with protected tag is not qualified")
		}
	}
	for _, m := range observed {
		if m.present || m.tagID == "" || m.memberID == "" {
			continue
		}
		body, _ := json.Marshal(map[string]any{memberType: []string{m.memberID}})
		resource := m.resource
		if memberType == "template" {
			var err error
			resource, err = templateMembershipBoundResource(m)
			if err != nil {
				return nil, err
			}
		}
		var used atomic.Bool
		return &configurationWrite{resource: resource, action: "Update", identity: configurationResourceIdentity{target: m.memberID, parent: m.tagID}, submit: func(ctx context.Context) (Task, error) {
			if !used.CompareAndSwap(false, true) {
				return Task{}, errConfigurationTransition
			}
			// A full observation spans several native readers. Repeat it so
			// changes during collection cannot hide behind its earlier tag or
			// project observation. Neither pass grants dispatch authority.
			for range 2 {
				fresh, err := reobserve(ctx)
				if err != nil {
					return Task{}, err
				}
				if !reflect.DeepEqual(observed, fresh) {
					return Task{}, errConfigurationStale
				}
			}
			raw, err := c.mutate(ctx, http.MethodPost, tagsPath+"/"+m.tagID+"/member", body)
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
	return nil, nil
}
func (e *templateMembershipExecutor) verify(ctx context.Context, op *ciskov1.NetworkControllerConfigurationOperation) (bool, error) {
	if op == nil || op.Action != "Update" || !validAPIID(op.TargetID) || !validAPIID(op.ParentID) || op.TargetSerial != "" || op.CreatedResourceID != "" || op.PhysicalGroup != nil || op.DependencyVersions != nil || len(op.CredentialVersions) != 0 {
		return false, errConfigurationTransition
	}
	if !validTemplateMembershipBoundResource(op.Resource) {
		return false, errTemplateMembershipProjectBinding
	}
	// Recovery verifies one acknowledged association, not every future source
	// member. Select it using the persisted binding and current project catalog;
	// reuse the normal observer with that bounded intent. Submission still
	// preflights the complete declared selection.
	projects, err := e.client.listTemplateProjects(ctx)
	if err != nil {
		return false, err
	}
	var selected *templateMembershipExecutor
	for _, definition := range e.intent.definitions {
		projectID := projects[definition.identity.Project].ID
		if projectID == "" {
			continue
		}
		for _, tag := range definition.tags {
			resource, err := templateMembershipBoundResource(tagMembership{resource: tagMembershipIdentity(definition.identity.Key(), tag), projectID: projectID})
			if err != nil {
				return false, err
			}
			if resource == op.Resource {
				if selected != nil {
					return false, errConfigurationTransition
				}
				identity := definition.identity
				identity.Members = nil
				selected = &templateMembershipExecutor{client: e.client, intent: templateIntent{definitions: []desiredTemplate{{identity: identity, tags: []string{tag}}}}}
			}
		}
	}
	if selected == nil {
		return false, errTemplateMembershipProjectBinding
	}
	for range 2 {
		observed, err := selected.observe(ctx)
		if err != nil {
			return false, err
		}
		for i := range observed {
			if observed[i].projectID == "" && observed[i].memberID == "" {
				continue
			}
			resource, err := templateMembershipBoundResource(observed[i])
			if err != nil {
				return false, err
			}
			observed[i].resource = resource
		}
		matched, err := verifyTagMembership(observed, op)
		if err != nil || !matched {
			return false, err
		}
	}
	return true, nil
}
func verifyTagMembership(observed []tagMembership, op *ciskov1.NetworkControllerConfigurationOperation) (bool, error) {
	if op.Action != "Update" || !validAPIID(op.TargetID) || !validAPIID(op.ParentID) {
		return false, errConfigurationTransition
	}
	for _, m := range observed {
		if m.resource == op.Resource {
			if m.tagID != op.ParentID || m.memberID != op.TargetID {
				return false, errors.New("tag membership identity changed")
			}
			return m.present, nil
		}
	}
	return false, errConfigurationTransition
}
func (e *templateMembershipExecutor) report(ctx context.Context) ([]configapi.NetworkControllerDriftEntry, error) {
	observed, err := e.observe(ctx)
	if err != nil {
		return nil, err
	}
	out := []configapi.NetworkControllerDriftEntry{}
	for _, m := range observed {
		if !m.present {
			out = append(out, configapi.NetworkControllerDriftEntry{Section: "templates", Resource: m.resource, Path: "/tag_membership", Change: configapi.NetworkControllerDriftChangeAdd})
		}
	}
	return out, nil
}
