// Copyright 2026 Cisco Systems Inc.
// SPDX-License-Identifier: Apache-2.0
package catalystcenter

import (
	"context"
	"encoding/json"
	"reflect"
	"strings"
	"time"

	cvk "github.com/cisco/virtual-kubelet-cisco/api/v1alpha1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

type templateCreateBinding struct {
	resource, legacy, kind, project, name string
	snapshotMatches                       func(context.Context, map[string]any) error
}
type templateOwnedExecutor struct {
	base                                   configurationExecutor
	client                                 *client
	owner, ownerName                       string
	sourceUID, sourceVersion               string
	configGeneration, controllerGeneration int64
	bindings                               map[string]templateCreateBinding
	receipts                               []cvk.NetworkControllerConfigurationActionApplication
	comparison                             *templateInputComparison
}

func (e *templateOwnedExecutor) count() int { return e.base.count() }
func (e *templateOwnedExecutor) find(op *cvk.NetworkControllerConfigurationOperation) (templateCreateBinding, bool) {
	if !validTemplateCreateOperation(op) {
		return templateCreateBinding{}, false
	}
	b, ok := e.bindings[op.Resource]
	return b, ok
}
func (e *templateOwnedExecutor) verify(ctx context.Context, op *cvk.NetworkControllerConfigurationOperation) (bool, error) {
	if op != nil && strings.HasPrefix(op.Resource, templateCreatePrefix) {
		_, done, err := e.verifyCreated(ctx, op)
		return done, err
	}
	if op != nil && strings.HasPrefix(op.Resource, templateApplyPrefix) {
		return e.verifyApplied(ctx, op)
	}
	if err := e.checkIdentities(ctx); err != nil {
		return false, err
	}
	return e.base.verify(ctx, op)
}
func (e *templateOwnedExecutor) verifyCreated(ctx context.Context, op *cvk.NetworkControllerConfigurationOperation) (string, bool, error) {
	b, ok := e.find(op)
	if !ok || op.SubmittedAt == nil || op.SubmittedAt.IsZero() || !validAPIID(op.TaskID) || op.ConfigUID != e.owner || op.ConfigName != e.ownerName || op.ConfigGeneration != e.configGeneration || op.ControllerGeneration != e.controllerGeneration {
		return "", false, errConfigurationStale
	}
	done, err := e.client.configurationTaskTreeComplete(ctx, op.TaskID, op.SubmittedAt.Time, time.Now())
	if err != nil || !done {
		return "", false, err
	}
	id, done, err := e.client.templateCreateTaskResult(ctx, op, time.Now())
	if err != nil || !done {
		return "", false, err
	}
	if err := e.checkIdentities(ctx); err != nil {
		return "", false, err
	}
	legacy := op.DeepCopy()
	legacy.Resource = b.legacy
	legacy.TargetID = id
	done, err = e.base.verify(ctx, legacy)
	if err != nil || !done {
		return "", false, err
	}
	// Bracket native identity/content verification with unchanged authoritative task
	// identity and complete child-task success. Progress prose is never interpreted.
	again, done, err := e.client.templateCreateTaskResult(ctx, op, time.Now())
	if err != nil || !done {
		return "", false, err
	}
	if again != id {
		return "", false, errConfigurationOwned
	}
	done, err = e.client.configurationTaskTreeComplete(ctx, op.TaskID, op.SubmittedAt.Time, time.Now())
	return id, done, err
}
func (e *templateOwnedExecutor) checkIdentities(ctx context.Context) error {
	records, err := templateCreatedRecords(&cvk.NetworkControllerConfigurationStatus{ActionApplications: e.receipts})
	if err != nil {
		return err
	}
	if len(records) == 0 {
		return nil
	}
	projects, err := e.client.listTemplateProjects(ctx)
	if err != nil {
		return err
	}
	var definitions map[string]templateSummary
	for resource, b := range e.bindings {
		key, _ := templateCreateKey(resource)
		r, owned := records[key]
		if !owned {
			continue
		}
		id, err := templateCreatedTarget(r)
		if err != nil || r.Operation.ConfigUID != e.owner || r.Operation.ConfigName != e.ownerName || r.Operation.ConfigGeneration > e.configGeneration || r.Operation.ControllerGeneration > e.controllerGeneration {
			return errConfigurationOwned
		}
		if b.kind == "p" {
			if projects[b.name].ID != id {
				return errConfigurationOwned
			}
			continue
		}
		project := projects[b.project]
		if project.ID != r.Operation.ParentID {
			return errConfigurationOwned
		}
		if definitions == nil {
			definitions, err = e.client.listTemplateDefinitions(ctx)
			if err != nil {
				return err
			}
		}
		native, ok := definitions[project.ID+"#"+b.name]
		if !ok || native.ID != id {
			return errConfigurationOwned
		}
		body, err := e.client.readTemplateDefinition(ctx, native)
		if err != nil {
			return err
		}
		if body["composite"] != (b.kind == "c") {
			return errConfigurationOwned
		}
	}
	return nil
}
func (e *templateOwnedExecutor) next(ctx context.Context) (*configurationWrite, error) {
	if err := e.checkIdentities(ctx); err != nil {
		return nil, err
	}
	if err := e.reserveCreateCapacity(ctx); err != nil {
		return nil, err
	}
	write, err := e.base.next(ctx)
	if err != nil {
		return nil, err
	}
	// A replacement between the ownership observation and base planning must also
	// fence a no-op or an Update; matching metadata alone cannot re-adopt its UUID.
	if err := e.checkIdentities(ctx); err != nil {
		return nil, err
	}
	if write == nil {
		return nil, nil
	}
	if write.action == "Update" {
		resource, err := e.applyResource(write.resource)
		if err != nil {
			return nil, err
		}
		if resource != "" {
			copy := *write
			copy.resource = resource
			op := &cvk.NetworkControllerConfigurationOperation{ID: strings.Repeat("a", 32), Resource: resource, Section: "templates", Action: "Update", TargetID: write.identity.target, ParentID: write.identity.parent, ConfigUID: e.owner, ConfigName: e.ownerName, ConfigGeneration: e.configGeneration, ControllerGeneration: e.controllerGeneration, SourceUID: e.sourceUID, SourceVersion: e.sourceVersion}
			if write.identity.preparationWait {
				now := metav1.Now()
				op.PreparedAt = &now
			}
			if err := templateApplyCapacity(&cvk.NetworkControllerConfigurationStatus{ActionApplications: e.receipts}, op); err != nil {
				return nil, err
			}
			return &copy, nil
		}
	}
	if write.action != "Create" {
		return write, nil
	}
	for resource, b := range e.bindings {
		if b.legacy != write.resource {
			continue
		}
		copy := *write
		copy.resource = resource
		return &copy, nil
	}
	return write, nil
}
func (e *templateOwnedExecutor) retainedStateMatches(state *cvk.NetworkControllerConfigurationStatus) error {
	before, err := templateCreatedRecords(&cvk.NetworkControllerConfigurationStatus{ActionApplications: e.receipts})
	if err != nil {
		return err
	}
	current, err := templateCreatedRecords(state)
	if err != nil {
		return err
	}
	if !reflect.DeepEqual(before, current) {
		return errConfigurationOwned
	}
	beforeApplied, err := templateAppliedRecords(&cvk.NetworkControllerConfigurationStatus{ActionApplications: e.receipts})
	if err != nil {
		return err
	}
	currentApplied, err := templateAppliedRecords(state)
	if err != nil {
		return err
	}
	if !reflect.DeepEqual(beforeApplied, currentApplied) {
		return errConfigurationOwned
	}
	beforeHistory, err := templateAppliedHistoryRecords(&cvk.NetworkControllerConfigurationStatus{ActionApplications: e.receipts})
	if err != nil {
		return err
	}
	currentHistory, err := templateAppliedHistoryRecords(state)
	if err != nil {
		return err
	}
	if !reflect.DeepEqual(beforeHistory, currentHistory) {
		return errConfigurationOwned
	}
	return nil
}

// Reserve count/byte room for the whole missing creator set and its commits
// before the first effect, using only public identities and worst-case task IDs.
func (e *templateOwnedExecutor) reserveCreateCapacity(ctx context.Context) error {
	projects, err := e.client.listTemplateProjects(ctx)
	if err != nil {
		return err
	}
	var definitions map[string]templateSummary
	receipts := append([]cvk.NetworkControllerConfigurationActionApplication(nil), e.receipts...)
	existing := map[string]bool{}
	for _, r := range receipts {
		existing[r.Resource] = true
	}
	now := metav1.Now()
	added := 0
	for resource, b := range e.bindings {
		absent := projects[b.name].ID == ""
		if b.kind != "p" {
			project := projects[b.project]
			absent = project.ID == ""
			if !absent {
				if definitions == nil {
					definitions, err = e.client.listTemplateDefinitions(ctx)
					if err != nil {
						return err
					}
				}
				_, present := definitions[project.ID+"#"+b.name]
				absent = !present
			}
		}
		if !absent {
			continue
		}
		op := cvk.NetworkControllerConfigurationOperation{ID: strings.Repeat("x", 32), ConfigName: e.ownerName, ConfigUID: e.owner, ConfigGeneration: e.configGeneration, ControllerGeneration: e.controllerGeneration, SourceUID: e.sourceUID, SourceVersion: e.sourceVersion, Section: "templates", Resource: resource, Action: "Create", Phase: "Verified", TaskID: strings.Repeat("x", 128), SubmittedAt: &now}
		if b.kind != "p" {
			op.ParentID = strings.Repeat("x", 36)
		}
		if b.kind == "c" {
			op.PreparedAt = &now
		}
		key, _ := templateCreateKey(resource)
		result := templateCreatedPrefix + key + ":" + strings.Repeat("x", 36)
		if comparisonKey := templateCreatorComparisonKey(resource); comparisonKey != "" {
			result += ":" + templateComparisonTag + ":" + comparisonKey + ":" + strings.Repeat("x", 64)
		}
		receipts = append(receipts, cvk.NetworkControllerConfigurationActionApplication{Section: "templates", Resource: result, Operation: op, VerifiedAt: &now})
		added++
		if b.kind != "p" {
			commit := strings.Replace(b.legacy, "draft:", "commit:", 1)
			if !existing[commit] {
				op.Resource = commit
				op.Action = "Update"
				op.TargetID = strings.Repeat("x", 36)
				result := commit
				if comparisonKey := templateCreatorComparisonKey(resource); comparisonKey != "" {
					op.Resource = templateApplyResource(resource, "commit") + ":" + templateVersionOwnershipTag
					result = templateAppliedPrefix + key + ":" + op.TargetID + ":" + templateComparisonTag + ":" + comparisonKey + ":" + strings.Repeat("x", 64) + ":" + strings.Repeat("x", 64) + ":" + templateVersionOwnershipTag + ":" + strings.Repeat("x", 36) + ":" + strings.Repeat("x", 64)
				}
				receipts = append(receipts, cvk.NetworkControllerConfigurationActionApplication{Section: "templates", Resource: result, Operation: op, VerifiedAt: &now})
				existing[commit] = true
			}
		}
	}
	if added == 0 {
		return nil
	}
	raw, err := json.Marshal(receipts)
	if err != nil || len(receipts) > 256 || len(raw) > 256<<10 {
		return errConfigurationBusy
	}
	return nil
}
