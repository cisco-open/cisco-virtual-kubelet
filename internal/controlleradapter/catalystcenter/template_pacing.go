// Copyright © 2026 Cisco Systems Inc.
// Licensed under the Apache License, Version 2.0 (the "License");
package catalystcenter

import (
	"context"
	"strings"
	"time"

	configapi "github.com/cisco/virtual-kubelet-cisco/api/config/v1alpha1"
	cvk "github.com/cisco/virtual-kubelet-cisco/api/v1alpha1"
)

const templateRegularSettleDelay = 10 * time.Second

func validTemplateCommitReceipt(op *cvk.NetworkControllerConfigurationOperation) bool {
	if op == nil || op.Section != "templates" || op.Action != "Update" || op.TargetSerial != "" || op.PhysicalGroup != nil || op.CreatedResourceID != "" || op.CancellationRequestedAt != nil || op.DependencyVersions != nil || len(op.CredentialVersions) != 0 || !validAPIID(op.TargetID) || !validAPIID(op.ParentID) || !validAPIID(op.TaskID) {
		return false
	}
	for _, prefix := range []string{"commit:", "objects/commit:", "templates/objects/commit:", "templates/commit:"} {
		if key, ok := strings.CutPrefix(op.Resource, prefix); ok && key != "" {
			return true
		}
	}
	return false
}

// Bind pacing only in public admitted reconciliation. Native clients remain
// independent of Kubernetes, and nested group resource prefixes stay exact.
func (a *adapter) bindTemplateSettling(executor configurationExecutor, config *configapi.NetworkControllerConfig, source *configSourceSnapshot, prefix string) {
	switch e := executor.(type) {
	case *templateOwnedExecutor:
		a.bindTemplateSettling(e.base, config, source, prefix)
	case *configurationGroup:
		for _, part := range e.parts {
			a.bindTemplateSettling(part.executor, config, source, prefix+part.name+"/")
		}
	case *templateExecutor:
		e.settle = func(ctx context.Context, resource string, observed map[string]templateObservation) error {
			if err := a.checkConfigurationSnapshot(ctx, config, source); err != nil {
				return err
			}
			nc, err := a.currentController(ctx)
			if err != nil {
				return err
			}
			if err := templateRegularDependenciesSettled(nc, config, source, e.intent, prefix, resource, observed, time.Now()); err != nil {
				return err
			}
			if err := a.checkConfigurationSnapshot(ctx, config, source); err != nil {
				return err
			}
			fresh, err := a.currentController(ctx)
			if err != nil {
				return err
			}
			if fresh.UID != nc.UID || fresh.ResourceVersion != nc.ResourceVersion {
				return errConfigurationStale
			}
			return nil
		}
	}
}

// The wait is measured from persisted receipt verification, not process uptime.
// Already-existing regular templates have no fabricated mutation receipt: the
// exact composite Prepared journal supplies a restart-stable observation floor.
// Missing legacy timestamps cannot authorize submission.
func templateRegularDependenciesSettled(nc *cvk.NetworkController, config *configapi.NetworkControllerConfig, source *configSourceSnapshot, intent templateIntent, prefix, resource string, observed map[string]templateObservation, now time.Time) error {
	if nc == nil || config == nil || nc.Status.Configuration == nil || now.IsZero() {
		return errConfigurationStale
	}
	state := nc.Status.Configuration
	owners := 0
	for _, owner := range state.Owners {
		if owner.Section == "templates" {
			if owner.ConfigName != config.Name || owner.ConfigUID != string(config.UID) {
				return errConfigurationOwned
			}
			owners++
		}
	}
	if owners != 1 {
		return errConfigurationOwned
	}
	uid, version := "", ""
	if source != nil {
		uid, version = string(source.uid), source.version
	}
	revision := func(op *cvk.NetworkControllerConfigurationOperation) bool {
		return op.ConfigUID == string(config.UID) && op.ConfigName == config.Name && op.ConfigGeneration == config.Generation && op.ControllerGeneration == nc.Generation && op.SourceUID == uid && op.SourceVersion == version && op.Section == "templates"
	}
	applied, err := templateAppliedRecords(state)
	if err != nil {
		return err
	}
	pending, missing := false, false
	for _, definition := range intent.definitions {
		if definition.identity.Composite {
			continue
		}
		got := observed[definition.identity.Key()]
		if !validAPIID(got.summary.ID) || !validAPIID(got.project.ID) || got.version == nil || !templateDefinitionMatches(definition.payload, got.draft) || !templateDefinitionMatches(definition.payload, got.committed) {
			return errConfigurationBusy
		}
		key := prefix + "commit:" + definition.identity.Key()
		var receipt *cvk.NetworkControllerConfigurationActionApplication
		for i := range state.ActionApplications {
			r := &state.ActionApplications[i]
			if r.Section == "templates" && r.Resource == key {
				if receipt != nil {
					return errConfigurationOwned
				}
				receipt = r
			}
		}
		identity := strings.TrimPrefix(templateCreatorResource("r", definition.identity.Project, definition.identity.Name), templateCreatePrefix)
		if r, exists := applied[identity]; exists {
			_, _, stage, _ := templateApplyIdentity(&r.Operation)
			if stage == "commit" {
				receipt = &r
				key = r.Operation.Resource
			} else {
				receipt = nil
			}
		}
		if receipt == nil {
			missing = true
			continue
		}
		op := &receipt.Operation
		if op.ConfigUID != string(config.UID) || op.ConfigName != config.Name {
			return errConfigurationOwned
		}
		if !revision(op) {
			missing = true
			continue
		}
		if op.Resource != key || op.Phase != "Verified" || op.Action != "Update" || !configurationOperationID.MatchString(op.ID) || op.TargetID != got.summary.ID || op.ParentID != got.project.ID || op.TargetSerial != "" || op.PhysicalGroup != nil || op.CancellationRequestedAt != nil || len(op.CredentialVersions) != 0 || op.DependencyVersions != nil || !validAPIID(op.TaskID) || op.SubmittedAt == nil || op.SubmittedAt.IsZero() || op.SubmittedAt.After(now) {
			return errConfigurationStale
		}
		if receipt.VerifiedAt == nil {
			missing = true
			continue
		}
		if receipt.VerifiedAt.IsZero() || receipt.VerifiedAt.Before(op.SubmittedAt) || receipt.VerifiedAt.After(now) {
			return errConfigurationStale
		}
		// metav1.Time is persisted to whole seconds. Avoid an early release after
		// serialization by retaining a one-second margin, as in AP pacing.
		if now.Before(receipt.VerifiedAt.Add(templateRegularSettleDelay + time.Second)) {
			pending = true
		}
	}
	op := state.Operation
	if op == nil {
		return nil
	} // read-only planning may prepare a durable journal
	resourceMatches := op.Resource == prefix+resource
	if !resourceMatches && validTemplateCreateOperation(op) {
		key, _ := templateCreateKey(op.Resource)
		for _, definition := range intent.definitions {
			if definition.identity.Composite && resource == "draft:"+definition.identity.Key() && templateCreatePrefix+key == templateCreatorResource("c", definition.identity.Project, definition.identity.Name) {
				resourceMatches = true
			}
		}
	}
	if !resourceMatches {
		key, _, stage, ok := templateApplyIdentity(op)
		if ok {
			for _, definition := range intent.definitions {
				if definition.identity.Composite && (stage == "draft" || stage == "commit") && resource == stage+":"+definition.identity.Key() && templateCreatePrefix+key == templateCreatorResource("c", definition.identity.Project, definition.identity.Name) {
					resourceMatches = true
				}
			}
		}
	}
	if !revision(op) || !resourceMatches || !configurationOperationID.MatchString(op.ID) || (op.Phase != "Prepared" && op.Phase != "Submitting") || op.CancellationRequestedAt != nil || op.PhysicalGroup != nil || op.TargetSerial != "" {
		return errConfigurationStale
	}
	if missing {
		if op.PreparedAt == nil || op.PreparedAt.IsZero() || op.PreparedAt.After(now) || op.SubmittedAt != nil && op.PreparedAt.After(op.SubmittedAt.Time) {
			return errConfigurationBusy
		}
		if now.Before(op.PreparedAt.Add(templateRegularSettleDelay + time.Second)) {
			pending = true
		}
	}
	if pending {
		return errConfigurationBusy
	}
	return nil
}
