// Copyright © 2026 Cisco Systems Inc.
// Licensed under the Apache License, Version 2.0 (the "License");
package catalystcenter

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	ciskov1 "github.com/cisco/virtual-kubelet-cisco/api/v1alpha1"
	"net/http"
	"reflect"
	"sort"
	"strings"
)

type templateExecutor struct {
	submissionGuard func(context.Context, string, string, configurationResourceIdentity) error
	versionComment  func(context.Context, string, string, string, string) (string, error)
	client          *client
	intent          templateIntent
	settle          func(context.Context, string, map[string]templateObservation) error
}
type templateObservation struct {
	project          templateProject
	summary          templateSummary
	draft, committed map[string]any
	version          *templateVersion
}

func (e *templateExecutor) count() int { return len(e.intent.projects) + 2*len(e.intent.definitions) }
func (e *templateExecutor) projects() *templateProjectExecutor {
	return &templateProjectExecutor{client: e.client, desired: e.intent.projects, submissionGuard: e.submissionGuard}
}
func (e *templateExecutor) observe(ctx context.Context) (map[string]templateObservation, error) {
	projects, err := e.client.listTemplateProjects(ctx)
	if err != nil {
		return nil, err
	}
	definitions, err := e.client.listTemplateDefinitions(ctx)
	if err != nil {
		return nil, err
	}
	result := map[string]templateObservation{}
	for _, w := range e.intent.definitions {
		observed := templateObservation{project: projects[w.identity.Project]}
		if observed.project.ID != "" {
			if s, exists := definitions[observed.project.ID+"#"+w.identity.Name]; exists {
				observed.summary = s
				observed.draft, err = e.client.readTemplateDefinition(ctx, s)
				if err != nil {
					return nil, err
				}
				if observed.draft["composite"] != w.identity.Composite {
					return nil, errors.New("template kind replacement is not supported")
				}
				version, err := e.client.latestTemplateVersion(ctx, s.ID)
				if err != nil {
					return nil, err
				}
				observed.version = version
				if version != nil {
					observed.committed, err = e.client.readCommittedTemplate(ctx, s, *version)
					if err != nil {
						return nil, err
					}
				}
			}
		}
		result[w.identity.Key()] = observed
	}
	return result, nil
}
func (e *templateExecutor) mutation(resource, action, target, parent, method, path string, payload map[string]any) (*configurationWrite, error) {
	body, err := json.Marshal(payload)
	if err != nil {
		return nil, err
	}
	submittedBody := body
	write := &configurationWrite{resource: resource, action: action, identity: configurationResourceIdentity{target: target, parent: parent}, submit: func(ctx context.Context) (Task, error) {
		raw, err := e.client.mutate(ctx, method, path, submittedBody)
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
	}}
	if path == templateVersionsPath && method == http.MethodPost && e.versionComment != nil {
		guardConfigurationPreflight(write, func(ctx context.Context) error {
			original, ok := payload["comments"].(string)
			if !ok {
				return errConfigurationTransition
			}
			comment, err := e.versionComment(ctx, resource, target, parent, original)
			if err != nil {
				return err
			}
			copy := map[string]any{}
			for k, v := range payload {
				copy[k] = v
			}
			copy["comments"] = comment
			submittedBody, err = json.Marshal(copy)
			return err
		})
	}
	return write, nil
}

func (e *templateExecutor) compositeMutation(ctx context.Context, w desiredTemplate, observed map[string]templateObservation, marker, resource, action, target, parent, method, path string, payload map[string]any) (*configurationWrite, error) {
	intent, err := e.dispatchIntent()
	if err != nil {
		return nil, err
	}
	if w.identity.Composite && e.settle != nil {
		if err := e.settle(ctx, resource, observed); err != nil {
			return nil, err
		}
	}
	write, err := e.mutation(resource, action, target, parent, method, path, payload)
	if err != nil {
		return nil, err
	}
	write.identity.preparationWait = w.identity.Composite && e.settle != nil
	guardConfigurationPreflight(write, func(ctx context.Context) error {
		current, err := e.dispatchIntent()
		if err != nil || !bytes.Equal(intent, current) {
			return errConfigurationStale
		}
		// Settle may read external task/version state. Check the complete native
		// selection after it, immediately before releasing the original write.
		if write.identity.preparationWait {
			if err := e.settle(ctx, resource, observed); err != nil {
				return err
			}
		}
		fresh, err := e.observe(ctx)
		if err != nil {
			return err
		}
		_, currentMarker, ready, err := e.resolvedPayload(w, fresh)
		if err != nil {
			return err
		}
		if !ready || currentMarker != marker || !reflect.DeepEqual(observed, fresh) || fresh[w.identity.Key()].project.ID != parent || fresh[w.identity.Key()].summary.ID != target {
			return errConfigurationStale
		}
		if e.submissionGuard != nil {
			return e.submissionGuard(ctx, resource, action, write.identity)
		}
		return nil
	})
	return write, nil
}

// Keep template content private in memory; never put bodies or variable values
// into journal identities, diagnostics or unkeyed hashes. Explicit fields avoid
// silently omitting the compiler's private intent fields during JSON encoding.
func (e *templateExecutor) dispatchIntent() ([]byte, error) {
	definitions := make([]any, 0, len(e.intent.definitions))
	for _, d := range e.intent.definitions {
		definitions = append(definitions, []any{d.identity, d.tags, d.payload})
	}
	return json.Marshal([]any{e.intent.projects, definitions})
}
func (e *templateExecutor) next(ctx context.Context) (*configurationWrite, error) {
	observed, err := e.observe(ctx)
	if err != nil {
		return nil, err
	}
	// Complete all template/version reads before releasing a parent-project write.
	project, err := e.projects().next(ctx)
	if err != nil {
		return project, err
	}
	if project != nil {
		intent, err := e.dispatchIntent()
		if err != nil {
			return nil, err
		}
		guardConfigurationPreflight(project, func(ctx context.Context) error {
			current, err := e.dispatchIntent()
			if err != nil || !bytes.Equal(intent, current) {
				return errConfigurationStale
			}
			fresh, err := e.observe(ctx)
			if err != nil {
				return err
			}
			if !reflect.DeepEqual(observed, fresh) {
				return errConfigurationStale
			}
			return nil
		})
		return project, nil
	}
	definitions := append([]desiredTemplate(nil), e.intent.definitions...)
	sort.SliceStable(definitions, func(i, j int) bool { return !definitions[i].identity.Composite && definitions[j].identity.Composite })
	for _, w := range definitions {
		got := observed[w.identity.Key()]
		desired, marker, ready, err := e.resolvedPayload(w, observed)
		if err != nil {
			return nil, err
		}
		if !ready {
			continue
		}
		if !validAPIID(got.project.ID) {
			return nil, errors.New("template parent project unavailable")
		}
		if got.summary.ID == "" || !templateDefinitionMatches(desired, got.draft) {
			payload := map[string]any{}
			for k, v := range desired {
				payload[k] = v
			}
			method, action, path := http.MethodPost, "Create", templateProjectsPath+"/"+got.project.ID+"/template"
			if got.summary.ID != "" {
				method, action, path = http.MethodPut, "Update", templatesPath
				payload["id"] = got.summary.ID
				// Optional attributes omitted in the model are retained during updates.
				for _, key := range []string{"description", "softwareVersion"} {
					if _, exists := payload[key]; !exists {
						if value, exists := got.draft[key]; exists && value != nil {
							payload[key] = value
						}
					}
				}
			}
			return e.compositeMutation(ctx, w, observed, marker, "draft:"+w.identity.Key(), action, got.summary.ID, got.project.ID, method, path, payload)
		}
		if got.committed == nil || !templateDefinitionMatches(desired, got.committed) || (w.identity.Composite && (got.version == nil || !templateCommitCommentMatches(got.version.Comment, marker))) {
			return e.compositeMutation(ctx, w, observed, marker, "commit:"+w.identity.Key(), "Update", got.summary.ID, got.project.ID, http.MethodPost, templateVersionsPath, map[string]any{"templateId": got.summary.ID, "comments": marker})
		}
	}
	return nil, nil
}
func (e *templateExecutor) verify(ctx context.Context, op *ciskov1.NetworkControllerConfigurationOperation) (bool, error) {
	if op.ParentID == "" {
		return e.projects().verify(ctx, op)
	}
	if !validAPIID(op.ParentID) || (op.Action != "Create" && op.Action != "Update") || (op.Action == "Update" && !validAPIID(op.TargetID)) {
		return false, errConfigurationTransition
	}
	observed, err := e.observe(ctx)
	if err != nil {
		return false, err
	}
	for _, w := range e.intent.definitions {
		if op.Resource != "draft:"+w.identity.Key() && op.Resource != "commit:"+w.identity.Key() {
			continue
		}
		got := observed[w.identity.Key()]
		desired, marker, ready, err := e.resolvedPayload(w, observed)
		if err != nil || !ready {
			return false, err
		}
		if got.project.ID != op.ParentID {
			return false, errors.New("template parent identity changed")
		}
		if got.summary.ID == "" {
			return false, nil
		}
		if op.TargetID != "" && got.summary.ID != op.TargetID {
			return false, errors.New("template identity changed")
		}
		if strings.HasPrefix(op.Resource, "commit:") {
			if op.Action != "Update" {
				return false, errConfigurationTransition
			}
			return got.committed != nil && templateDefinitionMatches(desired, got.committed) && (!w.identity.Composite || (got.version != nil && templateCommitCommentMatches(got.version.Comment, marker))), nil
		}
		return templateDefinitionMatches(desired, got.draft), nil
	}
	return false, errConfigurationTransition
}

// Composite APIs retain base member IDs, so body equality alone cannot detect
// a newly committed member. The marker contains only hashes of nonsecret UUIDs;
// both member bodies and the composite body remain independently verified.
func (e *templateExecutor) resolvedPayload(w desiredTemplate, observed map[string]templateObservation) (map[string]any, string, bool, error) {
	payload := map[string]any{}
	for k, v := range w.payload {
		payload[k] = v
	}
	if !w.identity.Composite {
		return payload, "CVK NetAsCode configuration", true, nil
	}
	members := []any{}
	versions := []string{}
	for _, key := range w.identity.Members {
		var member *desiredTemplate
		for i := range e.intent.definitions {
			if e.intent.definitions[i].identity.Key() == key {
				member = &e.intent.definitions[i]
				break
			}
		}
		if member == nil || member.identity.Composite {
			return nil, "", false, errors.New("invalid composite dependency")
		}
		got := observed[key]
		if got.summary.ID == "" || got.version == nil || !templateDefinitionMatches(member.payload, got.draft) || !templateDefinitionMatches(member.payload, got.committed) {
			return nil, "", false, nil
		}
		members = append(members, map[string]any{"id": got.summary.ID, "name": member.identity.Name, "language": member.payload["language"], "projectName": member.identity.Project})
		versions = append(versions, got.summary.ID, got.version.ID)
	}
	payload["containingTemplates"] = members
	encoded, _ := json.Marshal(versions)
	return payload, fmt.Sprintf("CVK member versions %x", sha256.Sum256(encoded)), true, nil
}
