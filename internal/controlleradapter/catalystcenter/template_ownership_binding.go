// Copyright 2026 Cisco Systems Inc.
// SPDX-License-Identifier: Apache-2.0
package catalystcenter

import (
	"context"
	"reflect"
	"strings"
	"time"

	configapi "github.com/cisco/virtual-kubelet-cisco/api/config/v1alpha1"
	cvk "github.com/cisco/virtual-kubelet-cisco/api/v1alpha1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

func (a *adapter) bindTemplateOwnership(nc *cvk.NetworkController, config *configapi.NetworkControllerConfig, source *configSourceSnapshot, plan *configurationApplyPlan) error {
	if plan.section != "templates" {
		return nil
	}
	e := &templateOwnedExecutor{base: plan.executor, client: a.client, owner: string(config.UID), ownerName: config.Name, configGeneration: config.Generation, controllerGeneration: nc.Generation, bindings: map[string]templateCreateBinding{}}
	sourceMode := "i"
	if config.Spec.Source.ConfigMapRef != nil {
		sourceMode = "c"
	}
	if source != nil && source.templateComparison != nil {
		if !templateNativeComparisonConfig(config) || source.secrets == nil {
			return errConfigurationSecret
		}
		e.comparison = source.templateComparison
	}
	intentResource := func(kind, project, name string) string {
		resource := templateCreatorResource(kind, project, name) + ":" + sourceMode
		if e.comparison != nil {
			resource += ":" + templateComparisonTag + ":" + e.comparison.keyID
		}
		return resource
	}
	if nc.Status.Configuration != nil {
		e.receipts = nc.Status.Configuration.DeepCopy().ActionApplications
	}
	if _, err := templateAppliedRecords(&cvk.NetworkControllerConfigurationStatus{ActionApplications: e.receipts}); err != nil {
		return err
	}
	type childBinding struct {
		executor configurationExecutor
		prefix   string
	}
	children := []childBinding{}
	if group, ok := plan.executor.(*configurationGroup); ok {
		for _, part := range group.parts {
			if part.name == "objects" {
				children = append(children, childBinding{part.executor, "objects/"})
			}
		}
	} else {
		children = append(children, childBinding{plan.executor, ""})
	}
	for _, child := range children {
		projects := []templateProject{}
		switch ex := child.executor.(type) {
		case *templateProjectExecutor:
			projects = ex.desired
		case *templateExecutor:
			projects = ex.intent.projects
			for _, d := range ex.intent.definitions {
				kind := "r"
				if d.identity.Composite {
					kind = "c"
				}
				resource := intentResource(kind, d.identity.Project, d.identity.Name)
				binding := templateCreateBinding{resource: resource, legacy: child.prefix + "draft:" + d.identity.Key(), kind: kind, project: d.identity.Project, name: d.identity.Name}
				binding.snapshotMatches = func(ctx context.Context, body map[string]any) error {
					desired := d.payload
					if d.identity.Composite {
						observed, err := ex.observe(ctx)
						if err != nil {
							return err
						}
						resolved, _, ready, err := ex.resolvedPayload(d, observed)
						if err != nil {
							return err
						}
						if !ready {
							return errConfigurationBusy
						}
						desired = resolved
					}
					if !templateDefinitionMatches(desired, body) {
						return errConfigurationOwned
					}
					return nil
				}
				e.bindings[resource] = binding
			}
		default:
			continue
		}
		for _, p := range projects {
			if p.Name != "Onboarding Configuration" {
				resource := intentResource("p", "", p.Name)
				e.bindings[resource] = templateCreateBinding{resource: resource, legacy: child.prefix + p.Name, kind: "p", name: p.Name, snapshotMatches: func(_ context.Context, body map[string]any) error {
					if p.Description != nil && body["description"] != *p.Description {
						return errConfigurationOwned
					}
					return nil
				}}
			}
		}
	}
	if len(e.bindings) == 0 {
		return nil
	}
	// A legacy retained Create cannot retroactively acquire the new protocol.
	if nc.Status.Configuration != nil && nc.Status.Configuration.Operation != nil {
		op := nc.Status.Configuration.Operation
		if op.Section == "templates" && op.Action == "Create" {
			for _, b := range e.bindings {
				if op.Resource == b.legacy {
					return nil
				}
			}
		}
	}
	if source == nil {
		source = &configSourceSnapshot{}
	}
	e.sourceUID, e.sourceVersion = string(source.uid), source.version
	for _, child := range children {
		guard := func(ctx context.Context, local, action string, identity configurationResourceIdentity) error {
			legacy := child.prefix + local
			resource := legacy
			valid := func(op *cvk.NetworkControllerConfigurationOperation) bool {
				return op != nil && op.Section == "templates" && op.Action == action && op.Resource == legacy && op.TargetSerial == "" && op.PhysicalGroup == nil && op.CancellationRequestedAt == nil && op.DependencyVersions == nil && op.CreatedResourceID == "" && len(op.CredentialVersions) == 0 && len(op.SSIDCredentialHistory) == 0
			}
			if action == "Create" {
				found := false
				for _, b := range e.bindings {
					if b.legacy == legacy {
						resource = b.resource
						found = true
						break
					}
				}
				if !found {
					return errConfigurationTransition
				}
				valid = validTemplateCreateOperation
			}
			if action == "Update" {
				typed, err := e.applyResource(legacy)
				if err != nil {
					return err
				}
				if typed != "" {
					resource = typed
					valid = func(op *cvk.NetworkControllerConfigurationOperation) bool {
						_, _, _, ok := templateApplyIdentity(op)
						return ok
					}
				}
			}
			return a.checkOwnedConfigurationDispatch(ctx, nc, config, source, "templates", resource, action, identity, valid, func(state *cvk.NetworkControllerConfigurationStatus, op *cvk.NetworkControllerConfigurationOperation) error {
				if err := e.retainedStateMatches(state); err != nil {
					return err
				}
				if action == "Create" {
					return templateCreateCapacity(state, op)
				}
				if strings.HasPrefix(resource, templateApplyPrefix) {
					return templateApplyCapacity(state, op)
				}
				return nil
			})
		}
		switch ex := child.executor.(type) {
		case *templateProjectExecutor:
			ex.submissionGuard = guard
		case *templateExecutor:
			ex.submissionGuard = guard
			if e.comparison != nil {
				ex.versionComment = func(ctx context.Context, local, target, parent, original string) (string, error) {
					current, err := a.currentController(ctx)
					if err != nil {
						return "", err
					}
					if current.Status.Configuration == nil || current.Status.Configuration.Operation == nil {
						return "", errConfigurationStale
					}
					op := current.Status.Configuration.Operation
					if !templateVersionOperation(op) {
						return original, nil
					}
					legacy, ok := e.appliedLegacy(op)
					if !ok || legacy.Resource != child.prefix+local || op.TargetID != target || op.ParentID != parent || op.Phase != "Submitting" {
						return "", errConfigurationStale
					}
					if err := guard(ctx, local, "Update", configurationResourceIdentity{target: target, parent: parent}); err != nil {
						return "", err
					}
					return templateCommitComment(original, op.ID), nil
				}
			}
		}
	}
	prior := *plan
	plan.executor = e
	plan.recoverKnownTask = func(op *cvk.NetworkControllerConfigurationOperation) bool {
		if op != nil && strings.HasPrefix(op.Resource, templateApplyPrefix) {
			_, _, ok := e.findApplied(op)
			return ok && op.Phase == "Unknown" && validAPIID(op.TaskID) && op.SubmittedAt != nil && !op.SubmittedAt.IsZero() && configurationOperationID.MatchString(op.ID) && op.ConfigUID == e.owner && op.ConfigName == e.ownerName && op.ConfigGeneration == e.configGeneration && op.ControllerGeneration == e.controllerGeneration
		}
		if op != nil && strings.HasPrefix(op.Resource, templateCreatePrefix) {
			_, ok := e.find(op)
			return ok && op.Phase == "Unknown" && validAPIID(op.TaskID) && op.SubmittedAt != nil && !op.SubmittedAt.IsZero() && configurationOperationID.MatchString(op.ID) && op.ConfigUID == e.owner && op.ConfigName == e.ownerName && op.ConfigGeneration == e.configGeneration && op.ControllerGeneration == e.controllerGeneration
		}
		return prior.canObserveUnknownTask(op)
	}
	plan.observeCompletion = func(ctx context.Context, op *cvk.NetworkControllerConfigurationOperation, now time.Time) (bool, error) {
		if op != nil && strings.HasPrefix(op.Resource, templateApplyPrefix) {
			if _, _, ok := e.findApplied(op); !ok || op.SubmittedAt == nil || !validAPIID(op.TaskID) {
				return false, errConfigurationTransition
			}
			return a.client.configurationTaskTreeComplete(ctx, op.TaskID, op.SubmittedAt.Time, now)
		}
		if op != nil && strings.HasPrefix(op.Resource, templateCreatePrefix) {
			if _, ok := e.find(op); !ok || op.SubmittedAt == nil || !validAPIID(op.TaskID) {
				return false, errConfigurationTransition
			}
			return a.client.configurationTaskTreeComplete(ctx, op.TaskID, op.SubmittedAt.Time, now)
		}
		return prior.taskComplete(ctx, a.client, op, now)
	}
	plan.receiptOperation = func(op *cvk.NetworkControllerConfigurationOperation) bool {
		return op != nil && (strings.HasPrefix(op.Resource, templateCreatePrefix) || strings.HasPrefix(op.Resource, templateApplyPrefix)) || prior.recordCompletion != nil && (prior.receiptOperation == nil || prior.receiptOperation(op))
	}
	plan.recordCompletion = func(ctx context.Context, op *cvk.NetworkControllerConfigurationOperation) error {
		if strings.HasPrefix(op.Resource, templateApplyPrefix) {
			return a.recordTemplateApplied(ctx, nc, config, source, e, op)
		}
		if !strings.HasPrefix(op.Resource, templateCreatePrefix) {
			if prior.recordCompletion == nil {
				return errConfigurationTransition
			}
			return prior.recordCompletion(ctx, op)
		}
		if _, ok := e.find(op); !ok || op.Phase != "Verifying" || op.ConfigUID != e.owner || op.ConfigName != config.Name || op.ConfigGeneration != config.Generation || op.ControllerGeneration != nc.Generation || op.SourceUID != string(source.uid) || op.SourceVersion != source.version {
			return errConfigurationStale
		}
		if err := a.checkConfigurationSnapshot(ctx, config, source); err != nil {
			return err
		}
		current, err := a.currentController(ctx)
		if err != nil {
			return err
		}
		if current.UID != nc.UID || current.Generation != nc.Generation || current.Spec.Paused || !current.DeletionTimestamp.IsZero() || current.Status.Configuration == nil || !reflect.DeepEqual(current.Status.Configuration.Operation, op) {
			return errConfigurationStale
		}
		if err := configurationOwnersMatch(current.Status.Configuration, config); err != nil {
			return err
		}
		if err := e.retainedStateMatches(current.Status.Configuration); err != nil {
			return err
		}
		if err := templateCreateCapacity(current.Status.Configuration, op); err != nil {
			return err
		}
		id, done, err := e.verifyCreated(ctx, op)
		if err != nil {
			return err
		}
		if !done {
			return errConfigurationBusy
		}
		baseline, err := e.captureCreatorBaseline(ctx, op, id)
		if err != nil {
			return err
		}
		if templateCreatorComparisonKey(op.Resource) != "" {
			again, complete, err := e.verifyCreated(ctx, op)
			if err != nil {
				return err
			}
			if !complete || again != id {
				return errConfigurationStale
			}
		}
		if err := a.checkConfigurationSnapshot(ctx, config, source); err != nil {
			return err
		}
		current.Status.Configuration.Operation.Phase = "Verified"
		now := metav1.NewTime(time.Now().UTC().Truncate(time.Second))
		if op.SubmittedAt.After(now.Time) {
			now = *op.SubmittedAt.DeepCopy()
		}
		result := templateCreatorResultResource(op.Resource, id, baseline)
		if result == "" {
			return errConfigurationTransition
		}
		current.Status.Configuration.ActionApplications = append(current.Status.Configuration.ActionApplications, cvk.NetworkControllerConfigurationActionApplication{Section: "templates", Resource: result, Operation: *current.Status.Configuration.Operation.DeepCopy(), VerifiedAt: &now})
		return a.persistConfigurationActionReceipt(ctx, current)
	}
	return nil
}
