// Copyright 2026 Cisco Systems Inc.
// SPDX-License-Identifier: Apache-2.0
package catalystcenter

import (
	"context"
	configapi "github.com/cisco/virtual-kubelet-cisco/api/config/v1alpha1"
	cvk "github.com/cisco/virtual-kubelet-cisco/api/v1alpha1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"reflect"
	"strings"
	"time"
)

type tagOwnedBinding struct {
	legacy, name, key, create, update string
	leaf                              *tagExecutor
}
type tagOwnedExecutor struct {
	base                                   configurationExecutor
	client                                 *client
	bindings                               map[string]tagOwnedBinding
	receipts                               []cvk.NetworkControllerConfigurationActionApplication
	owner, name                            string
	prune                                  bool
	cleanupEnrolled                        bool
	finalizeResource                       func(tagPruneCandidate) string
	section, sourceMode                    string
	pruneGuard                             func(context.Context, *configurationWrite) error
	configGeneration, controllerGeneration int64
	sourceUID, sourceVersion               string
}

func (e *tagOwnedExecutor) count() int { return e.base.count() }
func (e *tagOwnedExecutor) binding(op *cvk.NetworkControllerConfigurationOperation) (tagOwnedBinding, bool) {
	if !validTagOwnedOperation(op) || op.ConfigUID != e.owner || op.ConfigName != e.name {
		return tagOwnedBinding{}, false
	}
	for _, b := range e.bindings {
		if tagOwnedCanonical(op.Resource) == b.create || op.Resource == b.update {
			if tagSuccessorSelected(op.Resource) && tagSuccessorAuthority(&cvk.NetworkControllerConfigurationStatus{ActionApplications: e.receipts}, op) != nil {
				return tagOwnedBinding{}, false
			}
			if tagRecreateSelected(op.Resource) && tagRecreateAuthority(&cvk.NetworkControllerConfigurationStatus{ActionApplications: e.receipts}, op) != nil {
				return tagOwnedBinding{}, false
			}
			return b, true
		}
	}
	return tagOwnedBinding{}, false
}
func (e *tagOwnedExecutor) verifyState(ctx context.Context, op *cvk.NetworkControllerConfigurationOperation) (string, string, bool, error) {
	b, ok := e.binding(op)
	if !ok {
		return "", "", false, errConfigurationTransition
	}
	local := op.DeepCopy()
	local.Resource = b.name
	done, err := b.leaf.verify(ctx, local)
	if err != nil || !done {
		return "", "", false, err
	}
	rows, err := e.client.listTags(ctx)
	if err != nil {
		return "", "", false, err
	}
	got, exists := rows[b.name]
	if !exists || !canonicalTemplateUUID(got.ID) || got.SystemTag == nil || *got.SystemTag {
		return "", "", false, errConfigurationOwned
	}
	// Recheck the desired effect on the same bytes used for the retained state.
	for _, want := range b.leaf.desired {
		if want.name == b.name && !tagPayloadMatches(want.payload, got) {
			return "", "", false, errConfigurationStale
		}
	}
	if op.Action == "Create" {
		id, done, err := e.client.tagCreateTaskResult(ctx, op, time.Now())
		if err != nil || !done {
			return "", "", false, err
		}
		{
			if err := tagRecreatedIDUnused(&cvk.NetworkControllerConfigurationStatus{ActionApplications: e.receipts}, id); err != nil {
				return "", "", false, err
			}
		}
		if id != got.ID {
			return "", "", false, errConfigurationStale
		}
	} else if op.TargetID != got.ID {
		return "", "", false, errConfigurationStale
	}
	return got.ID, featureIdentityDigest(tagPayload(got)), true, nil
}
func (e *tagOwnedExecutor) verify(ctx context.Context, op *cvk.NetworkControllerConfigurationOperation) (bool, error) {
	if op != nil && tagDeleteSelected(op.Resource) {
		return e.verifyDelete(ctx, op)
	}
	if op != nil && tagOwnedSelected(op.Resource) {
		_, _, done, err := e.verifyState(ctx, op)
		return done, err
	}
	return e.base.verify(ctx, op)
}
func (e *tagOwnedExecutor) next(ctx context.Context) (*configurationWrite, error) {
	if e.prune {
		write, err := e.pruneNext(ctx)
		if err != nil || write != nil {
			return write, err
		}
	}
	records, err := tagOwnedRecords(&cvk.NetworkControllerConfigurationStatus{ActionApplications: e.receipts})
	if err != nil {
		return nil, err
	}
	if _, err := tagRetiredCohorts(&cvk.NetworkControllerConfigurationStatus{ActionApplications: e.receipts}); err != nil {
		return nil, err
	}
	deleted, err := tagDeletedRecords(&cvk.NetworkControllerConfigurationStatus{ActionApplications: e.receipts})
	if err != nil {
		return nil, err
	}
	rows, err := e.client.listTags(ctx)
	if err != nil {
		return nil, err
	}
	if err := tagOwnerArchiveNativeAbsent(&cvk.NetworkControllerConfigurationStatus{ActionApplications: e.receipts}, rows); err != nil {
		return nil, err
	}
	missing := 0
	for _, b := range e.bindings {
		got, present := rows[b.name]
		if creator, owned := records.created[b.key]; owned {
			if _, wasDeleted := deleted[b.key]; wasDeleted {
				if creator.Operation.ConfigUID != e.owner || creator.Operation.ConfigName != e.name || present {
					return nil, errConfigurationOwned
				}
				if err := e.recreationAbsent(ctx, b); err != nil {
					return nil, err
				}
				missing++
				continue
			}
			_, id, _, err := tagReceiptIdentity(creator)
			if err != nil || creator.Operation.ConfigUID != e.owner || creator.Operation.ConfigName != e.name || !present || got.ID != id || got.SystemTag == nil || *got.SystemTag {
				return nil, errConfigurationOwned
			}
			// A settled latest-state record is required for a subsequent mutation.
			// Verification of a retained in-flight operation bypasses next().
			latest := creator
			if applied, exists := records.applied[b.key]; exists {
				latest = applied
			}
			_, _, fingerprint, err := tagReceiptIdentity(latest)
			if err != nil || fingerprint != featureIdentityDigest(tagPayload(got)) {
				return nil, errConfigurationOwned
			}
		} else if !present {
			missing++
		}
	}
	reservation := 2
	if e.prune || e.cleanupEnrolled {
		reservation = 3
	}
	if len(e.receipts)+reservation*missing > 256 {
		return nil, errConfigurationBusy
	}
	write, err := e.base.next(ctx)
	if err != nil || write == nil {
		return write, err
	}
	b, selected := e.bindings[write.resource]
	if !selected {
		return write, nil
	}
	copy := *write
	if write.action == "Create" {
		copy.resource = tagRecreateIntent(b, &cvk.NetworkControllerConfigurationStatus{ActionApplications: e.receipts})
	} else if write.action == "Update" {
		if _, owned := records.created[b.key]; owned {
			copy.resource = b.update
		}
	}
	if tagOwnedSelected(copy.resource) {
		guardConfigurationPreflight(&copy, func(ctx context.Context) error {
			return b.leaf.submissionGuard(ctx, b.name, write.action, write.identity)
		})
	}
	return &copy, nil
}

// Bind only actual metadata-tag leaves. All task execution, source validation,
// status CAS and ownership exclusion remain in the shared configuration journal.
func (a *adapter) bindTagOwnership(nc *cvk.NetworkController, c *configapi.NetworkControllerConfig, source *configSourceSnapshot, plan *configurationApplyPlan) error {
	if plan.section != "inventory" && plan.section != "templates" {
		return nil
	}
	if err := tagOwnerCustodyValid(nc, c); err != nil {
		return err
	}
	e := &tagOwnedExecutor{base: plan.executor, client: a.client, owner: string(c.UID), name: c.Name, bindings: map[string]tagOwnedBinding{}}
	e.configGeneration, e.controllerGeneration = c.Generation, nc.Generation
	e.sourceUID, e.sourceVersion = string(source.uid), source.version
	if _, err := tagRetiredCohorts(nc.Status.Configuration); err != nil {
		return err
	}
	if nc.Status.Configuration != nil {
		e.receipts = nc.Status.Configuration.DeepCopy().ActionApplications
	}
	records, err := tagOwnedRecords(nc.Status.Configuration)
	if err != nil {
		return err
	}
	mode := "i"
	if c.Spec.Source.ConfigMapRef != nil {
		mode = "c"
	}
	var visit func(configurationExecutor, string)
	visit = func(executor configurationExecutor, prefix string) {
		switch child := executor.(type) {
		case *tagExecutor:
			for _, want := range child.desired {
				key := featureIdentityDigest(want.name)
				tail := key + ":" + featureIdentityDigest(want.payload) + ":" + mode
				b := tagOwnedBinding{legacy: prefix + want.name, name: want.name, key: key, create: tagOwnedPrefix + "c:" + tail, update: tagOwnedPrefix + "u:" + tail, leaf: child}
				e.bindings[b.legacy] = b
			}
		case *configurationGroup:
			for _, part := range child.parts {
				visit(part.executor, prefix+part.name+"/")
			}
		case *templateOwnedExecutor:
			visit(child.base, prefix)
		}
	}
	visit(plan.executor, "")
	e.cleanupEnrolled = c.Spec.DeletionPolicy == configapi.NetworkControllerRetentionPolicyDelete && hasTagCleanupFinalizer(c) && tagPruneSource(source.raw, c.Spec.ManagedSections)
	e.prune = (c.Spec.PrunePolicy == configapi.NetworkControllerRetentionPolicyDelete || tagDeletionCleanup(source)) && tagPruneSource(source.raw, c.Spec.ManagedSections)
	if tagDeletionCleanup(source) {
		e.finalizeResource = func(candidate tagPruneCandidate) string {
			return tagFinalizePrefix + candidate.Key + ":" + candidate.Full + ":i:" + source.tagDeletion.DeletionToken + ":" + source.tagDeletion.SpecDigest
		}
	}
	if _, ordinaryDiscovery := plan.executor.(*ownedDiscoveryExecutor); ordinaryDiscovery {
		e.prune = false
	}
	e.section, e.sourceMode = plan.section, mode
	if len(e.bindings) == 0 && !e.prune {
		return nil
	}
	if e.prune || e.cleanupEnrolled {
		if err := e.validatePruneScope(nc, c); err != nil {
			return err
		}
	}
	// Existing ordinary operations keep their old protocol; matching native state
	// must not retroactively manufacture a creator receipt.
	if nc.Status.Configuration != nil && nc.Status.Configuration.Operation != nil {
		op := nc.Status.Configuration.Operation
		if op.ConfigUID == e.owner {
			if _, legacy := e.bindings[op.Resource]; legacy {
				return nil
			}
		}
	}
	for _, b := range e.bindings {
		b.leaf.submissionGuard = func(ctx context.Context, local, action string, identity configurationResourceIdentity) error {
			var selected tagOwnedBinding
			found := false
			for _, candidate := range e.bindings {
				if candidate.leaf == b.leaf && candidate.name == local {
					selected = candidate
					found = true
					break
				}
			}
			if !found {
				return errConfigurationTransition
			}
			resource := tagRecreateIntent(selected, &cvk.NetworkControllerConfigurationStatus{ActionApplications: e.receipts})
			if action == "Create" && tagRecreateSelected(resource) {
				if err := e.recreationAbsent(ctx, selected); err != nil {
					return err
				}
			}
			if action == "Update" {
				if _, owned := records.created[selected.key]; !owned {
					return nil
				}
				resource = selected.update
			}
			if err := e.client.tagOwnerHistoryAbsent(ctx, &cvk.NetworkControllerConfigurationStatus{ActionApplications: e.receipts}); err != nil {
				return err
			}
			return a.checkOwnedConfigurationDispatch(ctx, nc, c, source, plan.section, resource, action, identity, validTagOwnedOperation, func(state *cvk.NetworkControllerConfigurationStatus, op *cvk.NetworkControllerConfigurationOperation) error {
				fresh, err := tagOwnedRecords(state)
				if err != nil {
					return err
				}
				if !reflect.DeepEqual(records.created[selected.key], fresh.created[selected.key]) || !reflect.DeepEqual(records.applied[selected.key], fresh.applied[selected.key]) {
					return errConfigurationOwned
				}
				return tagOwnedCapacity(state, op)
			})
		}
	}
	e.pruneGuard = func(ctx context.Context, write *configurationWrite) error {
		return a.checkOwnedConfigurationDispatch(ctx, nc, c, source, plan.section, write.resource, write.action, write.identity, validTagDeleteOperation, func(state *cvk.NetworkControllerConfigurationStatus, op *cvk.NetworkControllerConfigurationOperation) error {
			if !reflect.DeepEqual(e.receipts, state.ActionApplications) {
				return errConfigurationOwned
			}
			return tagDeleteCapacity(state, op)
		})
	}
	prior := *plan
	plan.executor = e
	ours := func(op *cvk.NetworkControllerConfigurationOperation) bool {
		return op != nil && (tagOwnedSelected(op.Resource) || tagDeleteSelected(op.Resource))
	}
	plan.receiptOperation = func(op *cvk.NetworkControllerConfigurationOperation) bool {
		return ours(op) || prior.recordCompletion != nil && (prior.receiptOperation == nil || prior.receiptOperation(op))
	}
	plan.recoverKnownTask = func(op *cvk.NetworkControllerConfigurationOperation) bool {
		if !ours(op) {
			return prior.canObserveUnknownTask(op)
		}
		if tagDeleteSelected(op.Resource) {
			return e.prune && op.Phase == "Unknown" && validAPIID(op.TaskID) && op.SubmittedAt != nil && !op.SubmittedAt.IsZero() && tagDeleteAuthority(&cvk.NetworkControllerConfigurationStatus{ActionApplications: e.receipts}, op) == nil && !e.wantsDeleteKey(op)
		}
		_, ok := e.binding(op)
		return ok && op.Phase == "Unknown" && validAPIID(op.TaskID) && op.SubmittedAt != nil && !op.SubmittedAt.IsZero() && !op.SubmittedAt.After(time.Now())
	}
	plan.observeCompletion = func(ctx context.Context, op *cvk.NetworkControllerConfigurationOperation, now time.Time) (bool, error) {
		if !ours(op) {
			if prior.observeCompletion != nil {
				return prior.observeCompletion(ctx, op, now)
			}
			if op == nil || op.SubmittedAt == nil {
				return false, errConfigurationTransition
			}
			return a.client.configurationTaskComplete(ctx, op.TaskID, op.SubmittedAt.Time, now)
		}
		if tagDeleteSelected(op.Resource) {
			if !e.prune || tagDeleteAuthority(&cvk.NetworkControllerConfigurationStatus{ActionApplications: e.receipts}, op) != nil || op.SubmittedAt == nil {
				return false, errConfigurationOwned
			}
			return a.client.configurationTaskComplete(ctx, op.TaskID, op.SubmittedAt.Time, now)
		}
		if _, ok := e.binding(op); !ok || op.SubmittedAt == nil {
			return false, errConfigurationTransition
		}
		if op.Action == "Create" {
			_, done, err := a.client.tagCreateTaskResult(ctx, op, now)
			return done, err
		}
		return a.client.configurationTaskComplete(ctx, op.TaskID, op.SubmittedAt.Time, now)
	}
	plan.recordCompletion = func(ctx context.Context, op *cvk.NetworkControllerConfigurationOperation) error {
		if !ours(op) {
			if prior.recordCompletion == nil {
				return errConfigurationTransition
			}
			return prior.recordCompletion(ctx, op)
		}
		_, bound := e.binding(op)
		if tagDeleteSelected(op.Resource) {
			bound = e.prune && tagDeleteAuthority(&cvk.NetworkControllerConfigurationStatus{ActionApplications: e.receipts}, op) == nil && !e.wantsDeleteKey(op)
		}
		if tagFinalizeSelected(op.Resource) && !tagFinalizationMutationAllowed(c, source, op.Resource, op.Action) {
			return errConfigurationStale
		}
		if !bound || op.Phase != "Verifying" || !tagOperationGenerationMatches(c, source, op) || op.ControllerGeneration != nc.Generation || op.SourceUID != string(source.uid) || op.SourceVersion != source.version {
			return errConfigurationStale
		}
		if err := a.checkConfigurationSnapshot(ctx, c, source); err != nil {
			return err
		}
		current, err := a.currentController(ctx)
		if err != nil {
			return err
		}
		if current.UID != nc.UID || current.Generation != nc.Generation || current.Spec.Paused || !current.DeletionTimestamp.IsZero() || current.Status.Configuration == nil || !reflect.DeepEqual(current.Status.Configuration.Operation, op) {
			return errConfigurationStale
		}
		if !reflect.DeepEqual(e.receipts, current.Status.Configuration.ActionApplications) {
			return errConfigurationOwned
		}
		if err := configurationOwnersMatch(current.Status.Configuration, c); err != nil {
			return err
		}
		capacity := tagOwnedCapacity
		if tagDeleteSelected(op.Resource) {
			capacity = tagDeleteCapacity
		}
		if err := capacity(current.Status.Configuration, op); err != nil {
			return err
		}
		id, fingerprint, done, err := "", "", false, error(nil)
		fullState := ""
		if tagDeleteSelected(op.Resource) {
			done, err = e.verifyDelete(ctx, op)
			id = op.TargetID
		} else {
			id, fingerprint, done, err = e.verifyState(ctx, op)
			if err == nil && done && (e.prune || e.cleanupEnrolled) {
				var row observedTag
				row, fullState, err = e.client.tagNativeState(ctx, id)
				if err == nil && featureIdentityDigest(tagPayload(row)) != fingerprint {
					err = errConfigurationStale
				}
			}
		}
		if err != nil {
			return err
		}
		if !done {
			return errConfigurationBusy
		}
		if err := a.checkConfigurationSnapshot(ctx, c, source); err != nil {
			return err
		}
		key, create, _ := tagOwnedParts(op)
		deletion := tagDeleteSelected(op.Resource)
		if deletion {
			key, _ = tagDeleteKey(op)
		}
		prefix := tagAppliedPrefix
		if create {
			prefix = tagCreatedPrefix
		}
		if !create && !deletion {
			retained := current.Status.Configuration.ActionApplications[:0]
			for _, r := range current.Status.Configuration.ActionApplications {
				if !strings.HasPrefix(r.Resource, tagAppliedPrefix+key+":") {
					retained = append(retained, r)
				}
			}
			current.Status.Configuration.ActionApplications = retained
		}
		if tagRecreateSelected(op.Resource) {
			if err := archiveTagCohort(current.Status.Configuration, op); err != nil {
				return err
			}
		}
		current.Status.Configuration.Operation.Phase = "Verified"
		now := metav1.NewTime(time.Now().UTC().Truncate(time.Second))
		if now.Before(op.SubmittedAt) {
			now = *op.SubmittedAt.DeepCopy()
		}
		resource := prefix + key + ":" + id + ":" + fingerprint
		if fullState != "" {
			resource += ":s:" + fullState
		}
		if deletion {
			resource = tagDeletedPrefix + key + ":" + id
		}
		current.Status.Configuration.ActionApplications = append(current.Status.Configuration.ActionApplications, cvk.NetworkControllerConfigurationActionApplication{Section: op.Section, Resource: resource, Operation: *current.Status.Configuration.Operation.DeepCopy(), VerifiedAt: &now})
		return a.persistConfigurationActionReceipt(ctx, current)
	}
	return nil
}
