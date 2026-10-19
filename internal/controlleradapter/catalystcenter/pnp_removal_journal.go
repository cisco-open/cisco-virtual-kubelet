// Copyright 2026 Cisco Systems Inc.
// SPDX-License-Identifier: Apache-2.0
package catalystcenter

import (
	"context"
	"crypto/sha256"
	"fmt"
	"reflect"
	"strings"
	"time"

	configapi "github.com/cisco/virtual-kubelet-cisco/api/config/v1alpha1"
	cvk "github.com/cisco/virtual-kubelet-cisco/api/v1alpha1"
	"github.com/cisco/virtual-kubelet-cisco/internal/controlleradapter/catalystcenter/nacmodel"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
)

const pnpRemovalJournalPrefix = "pnp-remove:v2:"
const pnpRemovedPrefix = "pnp-removed:v1:"

func pnpRemovalSelected(s string) bool { return strings.HasPrefix(s, "pnp-remove:") }
func pnpRemovalParts(s string) (string, string, bool) {
	p := strings.Split(strings.TrimPrefix(s, pnpRemovalJournalPrefix), ":")
	if !strings.HasPrefix(s, pnpRemovalJournalPrefix) || len(p) != 2 || !validAPIID(p[0]) || !validTemplateComparisonDigest(p[1]) {
		return "", "", false
	}
	return p[0], p[1], true
}
func validPnPRemovalOperation(op *cvk.NetworkControllerConfigurationOperation) bool {
	if op == nil {
		return false
	}
	id, _, ok := pnpRemovalParts(op.Resource)
	if op.ConfigUID == "" || op.ConfigName == "" || op.ConfigGeneration < 1 || op.ControllerGeneration < 1 || (op.SourceUID == "") != (op.SourceVersion == "") {
		return false
	}
	if !ok || !configurationOperationID.MatchString(op.ID) || op.Section != "inventory" || op.Action != "Delete" || op.TargetID != id || op.TargetSerial != "" || op.ParentID != "" || op.TaskID != "" || op.CreatedResourceID != "" || op.PhysicalGroup != nil || op.DependencyVersions != nil || op.CancellationRequestedAt != nil || len(op.CredentialVersions) != 0 || len(op.SSIDCredentialHistory) != 0 {
		return false
	}
	switch op.Phase {
	case "Prepared":
		return op.SubmittedAt == nil
	case "Submitting", "Unknown", "Verifying", "Verified":
		return op.SubmittedAt != nil && !op.SubmittedAt.IsZero() && !op.SubmittedAt.After(time.Now())
	}
	return false
}

func pnpRemovalCreator(state *cvk.NetworkControllerConfigurationStatus, uid, name, id string, generation int64) (*cvk.NetworkControllerConfigurationActionApplication, pnpRemovalBaseline, error) {
	var b pnpRemovalBaseline
	var found *cvk.NetworkControllerConfigurationActionApplication
	if state == nil {
		return nil, b, errConfigurationOwned
	}
	owner := &configapi.NetworkControllerConfig{ObjectMeta: metav1.ObjectMeta{Name: name, UID: types.UID(uid)}, Spec: configapi.NetworkControllerConfigSpec{ManagedSections: []string{"inventory"}}}
	if err := configurationOwnersMatch(state, owner); err != nil {
		return nil, b, err
	}
	for _, r := range state.ActionApplications {
		if r.Operation.CreatedResourceID != id {
			continue
		}
		if found != nil || !pnpRegistrationReceiptPresent(state, &r.Operation) || r.Operation.ConfigUID != uid || r.Operation.ConfigName != name || r.Operation.ControllerGeneration != generation {
			return nil, b, errConfigurationOwned
		}
		decoded, err := decodePnPRecord(r.Resource)
		if err != nil || decoded.Serial == "" {
			return nil, b, errConfigurationOwned
		}
		b = decoded
		found = r.DeepCopy()
	}
	if found == nil {
		return nil, b, errConfigurationOwned
	}
	return found, b, nil
}
func pnpRemovedKey(resource string) string {
	if !pnpRemovalSelected(resource) {
		return ""
	}
	return pnpRemovedPrefix + strings.TrimPrefix(resource, pnpRemovalJournalPrefix)
}
func pnpRemovedReceiptPresent(state *cvk.NetworkControllerConfigurationStatus, op *cvk.NetworkControllerConfigurationOperation) bool {
	if !validPnPRemovalOperation(op) || op.Phase != "Verified" || state == nil {
		return false
	}
	creator, _, err := pnpRemovalCreator(state, op.ConfigUID, op.ConfigName, op.TargetID, op.ControllerGeneration)
	_, digest, _ := pnpRemovalParts(op.Resource)
	if err != nil || digest != featureIdentityDigest(*creator) || op.ConfigGeneration < creator.Operation.ConfigGeneration {
		return false
	}
	count := 0
	for _, r := range state.ActionApplications {
		if r.Operation.ID != op.ID && r.Resource != pnpRemovedKey(op.Resource) {
			continue
		}
		if r.Section != "inventory" || r.Resource != pnpRemovedKey(op.Resource) || !reflect.DeepEqual(r.Operation, *op) || r.VerifiedAt == nil || r.VerifiedAt.IsZero() || r.VerifiedAt.Before(op.SubmittedAt) || r.VerifiedAt.After(time.Now()) {
			return false
		}
		count++
	}
	return count == 1
}
func pnpRemovalCapacity(state *cvk.NetworkControllerConfigurationStatus, op *cvk.NetworkControllerConfigurationOperation) error {
	if !validPnPRemovalOperation(op) {
		return errConfigurationTransition
	}
	creator, _, err := pnpRemovalCreator(state, op.ConfigUID, op.ConfigName, op.TargetID, op.ControllerGeneration)
	if err != nil {
		return err
	}
	_, digest, _ := pnpRemovalParts(op.Resource)
	if digest != featureIdentityDigest(*creator) || op.ConfigGeneration < creator.Operation.ConfigGeneration {
		return errConfigurationOwned
	}
	future := op.DeepCopy()
	future.Phase = "Verified"
	now := metav1.NewTime(time.Now().UTC().Truncate(time.Second))
	if future.SubmittedAt == nil {
		future.SubmittedAt = &now
	}
	rows := append([]cvk.NetworkControllerConfigurationActionApplication(nil), state.ActionApplications...)
	for _, r := range rows {
		if r.Operation.ID == op.ID || r.Resource == pnpRemovedKey(op.Resource) {
			return errConfigurationOwned
		}
	}
	rows = append(rows, cvk.NetworkControllerConfigurationActionApplication{Section: "inventory", Resource: pnpRemovedKey(op.Resource), Operation: *future, VerifiedAt: &now})
	return pnpRegistrationReceiptBytesFit(rows)
}

// The same product admission protects preparation and the shared start permit.
func pnpRemovalAdmission(state *cvk.NetworkControllerConfigurationStatus, c *configapi.NetworkControllerConfig, s *configSourceSnapshot, op *cvk.NetworkControllerConfigurationOperation) error {
	if err := pnpRemovalCapacity(state, op); err != nil {
		return err
	}
	_, baseline, err := pnpRemovalCreator(state, op.ConfigUID, op.ConfigName, op.TargetID, op.ControllerGeneration)
	if err != nil {
		return err
	}
	return pnpRemovalIntent(c, s, baseline)
}

// Private source-prune stage only. Public routing/CR finalization remain gated.
// Complete PnP-only intent must omit this serial, with explicit Prune Delete.
func pnpRemovalIntent(c *configapi.NetworkControllerConfig, s *configSourceSnapshot, b pnpRemovalBaseline) error {
	serials, err := pnpRemovalDeclaredSerials(c, s)
	if err != nil {
		return err
	}
	if serials[b.Serial] {
		return errConfigurationOwned
	}
	return nil
}

// Consume the complete pinned PnP-only fragment, including explicit empty intent.
func pnpRemovalDeclaredSerials(c *configapi.NetworkControllerConfig, s *configSourceSnapshot) (map[string]bool, error) {
	if c == nil || s == nil || !supportedConfigurationApply(c) || c.Spec.PrunePolicy != configapi.NetworkControllerRetentionPolicyDelete || c.Spec.DeletionPolicy == configapi.NetworkControllerRetentionPolicyDelete || !c.DeletionTimestamp.IsZero() || len(c.Spec.ManagedSections) != 1 || c.Spec.ManagedSections[0] != "inventory" || len(c.Spec.SecretRefs) != 0 || !c.Spec.ModelSource.Resolved || c.Spec.ModelSource.Format != nacmodel.Format || c.Spec.ModelSource.ModelVersion != nacmodel.Version {
		return nil, errConfigurationTransition
	}
	node, err := nacmodel.ParseDocument(s.raw)
	if err != nil {
		return nil, err
	}
	var root map[string]any
	if node.Decode(&root) != nil || len(root) != 1 {
		return nil, errConfigurationTransition
	}
	inventory, ok := root["inventory"].(map[string]any)
	if !ok || len(inventory) != 1 {
		return nil, errConfigurationTransition
	}
	devices, ok := inventory["devices"].([]any)
	if !ok {
		return nil, errConfigurationTransition
	}
	if len(devices) == 0 {
		return map[string]bool{}, nil
	}
	wants, err := parsePnPInventory(s.raw)
	if err != nil {
		return nil, err
	}
	out := map[string]bool{}
	for _, want := range wants {
		serials, err := validatePnPDeviceIntent(want)
		if err != nil {
			return nil, err
		}
		for _, serial := range serials {
			if out[serial] {
				return nil, errConfigurationOwned
			}
			out[serial] = true
		}
	}
	return out, nil
}

func (a *adapter) pnpRemovalContext(ctx context.Context, c *configapi.NetworkControllerConfig, s *configSourceSnapshot, id string) (*cvk.NetworkController, *cvk.NetworkControllerConfigurationActionApplication, pnpRemovalBaseline, error) {
	var zero pnpRemovalBaseline
	if err := a.checkConfigurationSnapshot(ctx, c, s); err != nil {
		return nil, nil, zero, err
	}
	nc, err := a.currentController(ctx)
	if err != nil {
		return nil, nil, zero, err
	}
	if nc.Status.Configuration == nil {
		return nil, nil, zero, errConfigurationOwned
	}
	if err := configurationOwnersMatch(nc.Status.Configuration, c); err != nil {
		return nil, nil, zero, err
	}
	creator, b, err := pnpRemovalCreator(nc.Status.Configuration, string(c.UID), c.Name, id, nc.Generation)
	if err != nil {
		return nil, nil, zero, err
	}
	if err := pnpRemovalIntent(c, s, b); err != nil {
		return nil, nil, zero, err
	}
	return nc, creator, b, nil
}

func (a *adapter) planOwnedPnPRemoval(ctx context.Context, c *configapi.NetworkControllerConfig, s *configSourceSnapshot, id string) (*configurationWrite, error) {
	return a.planPnPRemoval(ctx, c, s, id, nil)
}

func (a *adapter) planPnPRemoval(ctx context.Context, c *configapi.NetworkControllerConfig, s *configSourceSnapshot, id string, resuming *cvk.NetworkControllerConfigurationOperation) (*configurationWrite, error) {
	if a == nil || c == nil || !validAPIID(id) {
		return nil, errConfigurationTransition
	}
	c = c.DeepCopy()
	if s == nil {
		var err error
		s, err = loadControllerSource(ctx, a.statusReader, c)
		if err != nil {
			return nil, err
		}
	} else {
		copy := *s
		copy.raw = append([]byte(nil), s.raw...)
		s = &copy
	}
	nc, creator, b, err := a.pnpRemovalContext(ctx, c, s, id)
	if err != nil {
		return nil, err
	}
	var retainedHandoff *cvk.NetworkControllerConfigurationMetadataHandoff
	if resuming == nil {
		if nc.Status.Configuration.Operation != nil || nc.Status.Configuration.MetadataHandoff != nil {
			return nil, errConfigurationBusy
		}
	} else {
		resuming = resuming.DeepCopy()
		if !a.metadataAdmissionReady() || !validPnPRemovalOperation(resuming) || resuming.Phase != "Prepared" || resuming.TargetID != id || nc.Status.Configuration.MetadataHandoff == nil || !reflect.DeepEqual(nc.Status.Configuration.Operation, resuming) || !reflect.DeepEqual(nc.Status.Configuration.MetadataHandoff.Operation, *resuming) {
			return nil, errConfigurationBusy
		}
		retainedHandoff = nc.Status.Configuration.MetadataHandoff.DeepCopy()
	}
	cohort, err := ownedPnPRemovalCohort(nc, c, s)
	if err != nil {
		return nil, err
	}
	if len(cohort) == 0 || cohort[0].Baseline.ID != id {
		return nil, errConfigurationOwned
	}
	resource := pnpRemovalJournalPrefix + id + ":" + featureIdentityDigest(*creator)
	identity := configurationResourceIdentity{target: id}
	if resuming != nil {
		if resuming.Resource != resource {
			return nil, errConfigurationStale
		}
		identity.metadata = &metadataClaimSlot{}
	}
	synthetic := &cvk.NetworkControllerConfigurationOperation{ID: strings.Repeat("f", 32), Section: "inventory", Resource: resource, Action: "Delete", Phase: "Prepared", TargetID: id, ConfigUID: string(c.UID), ConfigName: c.Name, ConfigGeneration: c.Generation, ControllerGeneration: nc.Generation}
	if err := pnpRemovalCapacity(nc.Status.Configuration, synthetic); err != nil {
		return nil, err
	}
	authority := func(ctx context.Context) error {
		current, retained, _, err := a.pnpRemovalContext(ctx, c, s, id)
		if err != nil {
			return err
		}
		if !reflect.DeepEqual(creator, retained) {
			return errConfigurationOwned
		}
		if resuming != nil {
			if !reflect.DeepEqual(current.Status.Configuration.MetadataHandoff, retainedHandoff) {
				return errConfigurationStale
			}
			if reflect.DeepEqual(current.Status.Configuration.Operation, resuming) {
				return a.readMetadataPreparation(ctx, current)
			}
			if identity.metadata.permit == nil {
				return errConfigurationBusy
			}
		}
		if current.Status.Configuration.Operation == nil {
			if resuming != nil {
				return errConfigurationStale
			}
			if current.ResourceVersion != nc.ResourceVersion {
				return errConfigurationStale
			}
			return nil
		}
		return a.checkOwnedConfigurationDispatch(ctx, current, c, s, "inventory", resource, "Delete", identity, validPnPRemovalOperation, pnpRemovalCapacity)
	}
	cohortUnchanged := func(ctx context.Context) error {
		if err := authority(ctx); err != nil {
			return err
		}
		current, err := a.currentController(ctx)
		if err != nil {
			return err
		}
		fresh, err := ownedPnPRemovalCohort(current, c, s)
		if err != nil {
			return err
		}
		if !reflect.DeepEqual(cohort, fresh) {
			return errConfigurationStale
		}
		if err := a.client.observePnPRemovalCohort(ctx, cohort); err != nil {
			return err
		}
		return authority(ctx)
	}
	if err := cohortUnchanged(ctx); err != nil {
		return nil, err
	}
	rows, err := a.client.pnpRemovalDevices(ctx)
	if err != nil {
		return nil, err
	}
	var selected *pnpRemovalDevice
	for _, row := range rows {
		if row.Device.ID == id {
			copy := row
			selected = &copy
		}
	}
	if selected == nil || featureIdentityDigest(*selected) != b.NativeDigest {
		return nil, errConfigurationOwned
	}
	// Derive only current native identity and compare its canonical payload to
	// the retained proof. No old source/serial casing or artificial site is invented.
	payload, err := pnpRegistrationIdentityPayload(b.Serial, selected.Device.Info.Hostname, selected.Device.Info.PID, false)
	if err != nil {
		return nil, err
	}
	if fmt.Sprintf("%x", sha256.Sum256(payload)) != b.CanonicalDigest {
		return nil, errConfigurationOwned
	}
	expected := pnpRegistrationReceipt{resource: creator.Operation.Resource, deviceID: id, serial: b.Serial, pid: selected.Device.Info.PID, hostname: selected.Device.Info.Hostname}
	plan, err := a.client.planPnPRemovalIdentity(ctx, expected, b.NativeDigest, cohortUnchanged)
	if err != nil {
		return nil, err
	}
	write := &configurationWrite{resource: resource, action: "Delete", identity: identity, submitCompleted: plan.submit, noDispatch: plan.noDispatch}
	guardConfigurationPreflight(write, func(ctx context.Context) error {
		if err := cohortUnchanged(ctx); err != nil {
			return err
		}
		current, _, _, err := a.pnpRemovalContext(ctx, c, s, id)
		if err != nil {
			return err
		}
		return a.checkOwnedConfigurationDispatch(ctx, current, c, s, "inventory", resource, "Delete", identity, validPnPRemovalOperation, pnpRemovalCapacity)
	})
	return write, nil
}

func (a *adapter) recordOwnedPnPRemoval(ctx context.Context, c *configapi.NetworkControllerConfig, s *configSourceSnapshot, operationID string) error {
	return a.recordOwnedInventoryAction(ctx, c, s, operationID, func(ctx context.Context, op *cvk.NetworkControllerConfigurationOperation) (string, error) {
		if !validPnPRemovalOperation(op) {
			return "", errConfigurationTransition
		}
		_, creator, b, err := a.pnpRemovalContext(ctx, c, s, op.TargetID)
		if err != nil {
			return "", err
		}
		_, digest, _ := pnpRemovalParts(op.Resource)
		if digest != featureIdentityDigest(*creator) {
			return "", errConfigurationOwned
		}
		done, err := a.observeOwnedInventoryDeployment(ctx, c, s, op, func(ctx context.Context) (bool, error) {
			for range 2 {
				if _, err := a.client.readPnPRemovalState(ctx, pnpRegistrationReceipt{deviceID: b.ID, serial: b.Serial}, true); err != nil {
					return false, err
				}
			}
			return true, nil
		})
		if err != nil || !done {
			return "", err
		}
		current, err := a.currentController(ctx)
		if err != nil {
			return "", err
		}
		if err := pnpRemovalCapacity(current.Status.Configuration, op); err != nil {
			return "", err
		}
		return pnpRemovedKey(op.Resource), nil
	})
}
