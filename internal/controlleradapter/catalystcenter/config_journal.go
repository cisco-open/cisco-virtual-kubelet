// Copyright © 2026 Cisco Systems Inc.
// Licensed under the Apache License, Version 2.0 (the "License");
package catalystcenter

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"reflect"
	"regexp"
	"slices"
	"sort"
	"strings"
	"time"

	configapi "github.com/cisco/virtual-kubelet-cisco/api/config/v1alpha1"
	ciskov1 "github.com/cisco/virtual-kubelet-cisco/api/v1alpha1"
	contract "github.com/cisco/virtual-kubelet-cisco/internal/controllerconfiguration"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/util/retry"
	ctrlclient "sigs.k8s.io/controller-runtime/pkg/client"
)

var (
	errConfigurationOwned      = errors.New("controller section is owned by another config identity")
	errConfigurationBusy       = errors.New("controller configuration operation requires recovery")
	errConfigurationStale      = errors.New("controller configuration input has changed")
	errConfigurationTransition = errors.New("invalid controller configuration journal transition")
	configurationOperationID   = regexp.MustCompile(`^[a-f0-9]{32}$`)
)

type configurationResourceIdentity struct {
	metadata *metadataClaimSlot
	// Request a durable preparation boundary for adapter dependency pacing.
	preparationWait        bool
	groupTargets           []contract.GroupTargetIdentity
	target, parent, serial string
	dependencies           *ciskov1.NetworkControllerConfigurationDependencyVersions
	credentials            []ciskov1.NetworkControllerConfigurationCredentialVersion
	ssidCredentialHistory  []ciskov1.NetworkControllerConfigurationSSIDCredentialHistory
}

// Tags declared by inventory and templates share one Catalyst Center namespace.
// Conservatively reserve both sections to one config identity until resource-level
// ownership is available. Existing template owners are honored without migration.
func configurationSectionsOverlap(a, b string) bool {
	return a == b || ((a == "inventory" || a == "templates") && (b == "inventory" || b == "templates"))
}

// claimConfigurationSection also covers already-converged intent, which must
// own its section before publishing InSync. A no-op never needs a write permit.
func (a *adapter) claimConfigurationSection(ctx context.Context, config *configapi.NetworkControllerConfig, source *configSourceSnapshot) error {
	if !supportedConfigurationApply(config) {
		return errConfigurationTransition
	}
	nc, err := a.currentController(ctx)
	if err != nil {
		return err
	}
	if err := a.checkConfigurationSnapshot(ctx, config, source); err != nil {
		return err
	}
	if nc.Status.Configuration == nil {
		nc.Status.Configuration = &ciskov1.NetworkControllerConfigurationStatus{}
	}
	found := map[string]bool{}
	for _, owner := range nc.Status.Configuration.Owners {
		for _, section := range config.Spec.ManagedSections {
			if configurationSectionsOverlap(owner.Section, section) {
				if owner.ConfigName != config.Name || owner.ConfigUID != string(config.UID) {
					return errConfigurationOwned
				}
				if owner.Section == section {
					found[section] = true
				}
			}
		}
	}
	if len(found) == len(config.Spec.ManagedSections) {
		return nil
	}
	if len(nc.Status.Configuration.Owners)+len(config.Spec.ManagedSections)-len(found) > 64 {
		return errConfigurationTransition
	}
	for _, section := range config.Spec.ManagedSections {
		if !found[section] {
			nc.Status.Configuration.Owners = append(nc.Status.Configuration.Owners, ciskov1.NetworkControllerSectionOwner{Section: section, ConfigName: config.Name, ConfigUID: string(config.UID)})
		}
	}
	sort.Slice(nc.Status.Configuration.Owners, func(i, j int) bool {
		return nc.Status.Configuration.Owners[i].Section < nc.Status.Configuration.Owners[j].Section
	})
	return a.statusWriter.Status().Update(ctx, nc)
}

// prepareConfiguration reserves all selected global sections and one operation
// in a single status CAS. It does not authorize HTTP submission. The journal is
// deliberately on the controller: deleting the config cannot erase uncertainty.
// Callers must compile and validate the complete intent before invoking this.
func (a *adapter) prepareConfiguration(ctx context.Context, config *configapi.NetworkControllerConfig, source *configSourceSnapshot, section, resource, action string, identity ...configurationResourceIdentity) (*ciskov1.NetworkControllerConfigurationOperation, error) {
	groupResource := isReservedConfigurationGroupResource(section, resource)
	hasGroup := len(identity) == 1 && identity[0].groupTargets != nil
	anchorsValid := hasGroup && validAPIID(identity[0].target) && validAPIID(identity[0].parent)
	if hasGroup && section == "fabric" && (strings.HasPrefix(resource, "extranet-create:") || strings.HasPrefix(resource, "extranet-update-v2:")) {
		target, err := extranetOperationTarget(resource, action)
		anchorsValid = err == nil && identity[0].target == target && identity[0].parent == ""
	}
	if hasGroup && section == "fabric" && strings.HasPrefix(resource, sdaTransitCreateFamily) {
		target, err := sdaTransitOperationTarget(resource, action)
		anchorsValid = err == nil && identity[0].target == target && identity[0].parent == ""
	}
	if groupResource != hasGroup || hasGroup && (!validConfigurationGroupAction(section, resource, action) || !anchorsValid) {
		return nil, errConfigurationDeviceAdmission
	}

	if len(identity) > 1 || (len(identity) == 1 && ((identity[0].target != "" && !validAPIID(identity[0].target)) || (identity[0].parent != "" && !validAPIID(identity[0].parent)))) {
		return nil, errConfigurationTransition
	}
	if len(identity) == 1 && identity[0].serial != "" {
		serial := identity[0].serial
		if identity[0].target == "" || len(serial) > 128 || strings.TrimSpace(serial) != serial || strings.ContainsAny(serial, "\x00\r\n") {
			return nil, errConfigurationTransition
		}
	}
	if len(identity) == 1 {
		if err := validateConfigurationDependencyVersions(section, resource, action, identity[0]); err != nil {
			return nil, err
		}
		if err := validateConfigurationCredentialVersions(source, section, identity[0].credentials); err != nil {
			return nil, err
		}
	}
	if len(identity) == 0 {
		if err := validateSSIDCredentialHistory(section, resource, nil, nil); err != nil {
			return nil, err
		}
	}
	if config.Spec.Mode != configapi.NetworkControllerApplyModeApply || (config.Spec.Scope != "" && config.Spec.Scope != "global") ||
		config.UID == "" || config.Generation < 1 || len(resource) == 0 || len(resource) > 512 ||
		!slices.Contains([]string{"Create", "Update", "Delete"}, action) || !slices.Contains(config.Spec.ManagedSections, section) {
		return nil, errConfigurationTransition
	}
	selected := map[string]bool{}
	for _, s := range config.Spec.ManagedSections {
		// Execution support remains a separate gate from the model catalog.
		if (!reportSectionSupported(s) && !(s == "lan_automation" && supportedLANLinkAddConfig(config) && validLANLinkMutationResource(resource, action))) || selected[s] {
			return nil, errConfigurationTransition
		}
		selected[s] = true
	}
	if source != nil && source.discoveryCleanup != nil && !discoveryCleanupPreparationAllowed(config, source, section, resource, action, identity) {
		return nil, errConfigurationTransition
	}
	// Finalization identities can never be submitted through ordinary apply/prune.
	if tagFinalizeSelected(resource) && !tagFinalizationMutationAllowed(config, source, resource, action) {
		return nil, errConfigurationTransition
	}
	if !config.DeletionTimestamp.IsZero() && !featureFinalizationMutationAllowed(config, source, resource, action) && !tagFinalizationMutationAllowed(config, source, resource, action) && !discoveryCleanupPreparationAllowed(config, source, section, resource, action, identity) {
		return nil, errConfigurationTransition
	}
	nc, err := a.currentController(ctx)
	if err != nil {
		return nil, err
	}
	if err := a.checkConfigurationSnapshot(ctx, config, source); err != nil {
		return nil, err
	}
	if nc.Status.Configuration == nil {
		nc.Status.Configuration = &ciskov1.NetworkControllerConfigurationStatus{}
	}
	state := nc.Status.Configuration
	if state.MetadataHandoff != nil {
		return nil, errConfigurationBusy
	}
	if source != nil && source.discoveryCleanup != nil {
		o := source.discoveryCleanup.observation
		if string(nc.UID) != o.ControllerUID || nc.Generation != o.ControllerGeneration || nc.ResourceVersion != o.ControllerVersion || featureIdentityDigest(state) != o.EvidenceDigest {
			return nil, errConfigurationStale
		}
	}
	if state.Operation != nil {
		return nil, errConfigurationBusy
	}
	if len(identity) == 1 {
		if err := configurationCredentialCapacity(state, string(config.UID), section, resource, identity[0].credentials, identity[0].ssidCredentialHistory); err != nil {
			return nil, err
		}
	}
	for _, owner := range state.Owners {
		for section := range selected {
			if configurationSectionsOverlap(section, owner.Section) && (owner.ConfigUID != string(config.UID) || owner.ConfigName != config.Name) {
				return nil, errConfigurationOwned
			}
		}
	}
	for section := range selected {
		found := false
		for _, owner := range state.Owners {
			if owner.Section == section {
				found = true
				break
			}
		}
		if !found {
			state.Owners = append(state.Owners, ciskov1.NetworkControllerSectionOwner{Section: section, ConfigName: config.Name, ConfigUID: string(config.UID)})
		}
	}
	if len(state.Owners) > 64 {
		return nil, errConfigurationTransition
	}
	sort.Slice(state.Owners, func(i, j int) bool { return state.Owners[i].Section < state.Owners[j].Section })
	var nonce [16]byte
	if _, err = rand.Read(nonce[:]); err != nil {
		return nil, err
	}
	op := &ciskov1.NetworkControllerConfigurationOperation{
		ID: hex.EncodeToString(nonce[:]), ConfigName: config.Name, ConfigUID: string(config.UID), ConfigGeneration: config.Generation,
		ControllerGeneration: nc.Generation, Section: section, Resource: resource, Action: action, Phase: "Prepared",
	}
	if source != nil {
		op.SourceUID, op.SourceVersion = string(source.uid), source.version
	}
	if len(identity) == 1 {
		if identity[0].preparationWait {
			metadataTemplate := section == "templates" && identity[0].serial == ""
			physicalTemplate := section == "inventory" && identity[0].serial != "" && isTemplateDeploymentResource(resource)
			if (!metadataTemplate && !physicalTemplate) || identity[0].groupTargets != nil {
				return nil, errConfigurationTransition
			}
			op.PreparedAt = &metav1.Time{Time: time.Now().UTC().Truncate(time.Second)}
		}
		op.TargetID, op.ParentID, op.TargetSerial = identity[0].target, identity[0].parent, identity[0].serial
		op.CredentialVersions = slices.Clone(identity[0].credentials)
		op.SSIDCredentialHistory = slices.Clone(identity[0].ssidCredentialHistory)
		op.DependencyVersions = identity[0].dependencies.DeepCopy()
	}
	if len(identity) == 1 && identity[0].groupTargets != nil {
		op, err = contract.WithGroupIdentity(op, identity[0].groupTargets)
		if err != nil {
			return nil, errConfigurationDeviceAdmission
		}
	}
	if pnpRemovalSelected(resource) {
		if err := pnpRemovalAdmission(state, config, source, op); err != nil {
			return nil, err
		}
	}

	if featureDeleteSelected(resource) {
		if err := featureDeleteCapacity(state, op); err != nil {
			return nil, err
		}
	}
	if featureUpdateSelected(resource) {
		if err := featureUpdateCapacity(state, op); err != nil {
			return nil, err
		}
	}
	if featureCreateSelected(resource) {
		if err := featureCreateCapacity(state, op); err != nil {
			return nil, err
		}
	}
	if ownedDiscoverySelected(resource) {
		if err := ownedDiscoveryCapacity(state, op, discoveryRetiredReceiptKeys(nc, op.ConfigUID)); err != nil {
			return nil, err
		}
	}
	if discoveryCredentialRetirementSelected(resource) {
		if err := discoveryCredentialRetirementCapacity(state, op); err != nil {
			return nil, err
		}
	}
	if discoveryCredentialInputSelected(resource) {
		if err := discoveryCredentialCreateCapacity(state, op); err != nil {
			return nil, err
		}
	}
	if switchingCreateSelected(resource) {
		if err := switchingCreatorCapacity(state, op); err != nil {
			return nil, err
		}
	}
	if ipamOwnedSelected(resource) {
		if err := ipamOwnedCapacity(state, op); err != nil {
			return nil, err
		}
	}
	if plannedAPObservationSelected(op) {
		if err := plannedAPIdentityCapacity(state, op); err != nil {
			return nil, err
		}
	}
	if plannedAPAppliedSelected(op) {
		if err := plannedAPApplyCapacity(state, op); err != nil {
			return nil, err
		}
	}
	state.Operation = op
	if err := a.statusWriter.Status().Update(ctx, nc); err != nil {
		return nil, err
	}
	if op.PhysicalGroup != nil {
		// Group identity and Prepared are one CAS. Detect schema pruning or a
		// competing update before any caller can request group reservations.
		stored, err := a.currentController(ctx)
		if err != nil {
			return nil, err
		}
		if stored.UID != nc.UID || stored.Status.Configuration == nil || !reflect.DeepEqual(stored.Status.Configuration.Operation, op) {
			return nil, errConfigurationStale
		}
	}
	return op.DeepCopy(), nil
}

func (a *adapter) checkConfigurationSnapshot(ctx context.Context, config *configapi.NetworkControllerConfig, source *configSourceSnapshot) error {
	if config.Namespace != a.key.Namespace || config.Spec.ControllerRef.Name != a.key.Name {
		return errConfigurationStale
	}
	var fresh configapi.NetworkControllerConfig
	if err := a.statusReader.Get(ctx, ctrlclient.ObjectKeyFromObject(config), &fresh); err != nil {
		return err
	}
	if fresh.UID != config.UID || fresh.Generation != config.Generation || fresh.ResourceVersion != config.ResourceVersion || !fresh.DeletionTimestamp.IsZero() && !featureDeletionSnapshotValid(config, &fresh, source) && !tagDeletionSnapshotValid(config, &fresh, source) && !discoveryCleanupPreparationSnapshot(config, &fresh, source) && !discoveryDrainSnapshotValid(config, &fresh, source) {
		return errConfigurationStale
	}
	if source != nil && source.discoveryScopeControllerVersion != "" {
		nc, err := a.currentController(ctx)
		if err != nil {
			return err
		}
		if !fresh.DeletionTimestamp.IsZero() || nc.ResourceVersion != source.discoveryScopeControllerVersion || nc.Status.Configuration != nil && nc.Status.Configuration.Operation != nil {
			return errConfigurationStale
		}
	}
	if tagDeletionCleanup(source) {
		if !tagDeletionSnapshotValid(config, &fresh, source) {
			return errConfigurationStale
		}
		return nil
	}
	if featureDeletionCleanup(source) {
		if !featureDeletionSnapshotValid(config, &fresh, source) {
			return errConfigurationStale
		}
		return nil
	}
	if config.Spec.Source.ConfigMapRef != nil {
		if source == nil || source.key.Namespace != config.Namespace || source.key.Name != config.Spec.Source.ConfigMapRef.Name || source.uid == "" || source.version == "" {
			return errConfigurationStale
		}
		if source.discoveryScope != nil {
			nc, err := a.currentController(ctx)
			if err != nil {
				return err
			}
			if !discoveryRetainedSourceValid(nc, &fresh, source) {
				return errConfigurationStale
			}
		} else {
			var cm corev1.ConfigMap
			if err := a.statusReader.Get(ctx, source.key, &cm); err != nil {
				return err
			}
			if cm.UID != source.uid || cm.ResourceVersion != source.version || !cm.DeletionTimestamp.IsZero() {
				return errConfigurationStale
			}
		}
	} else if source != nil && (source.uid != "" || source.version != "" || source.key.Name != "") {
		return errConfigurationStale
	}
	if len(fresh.Spec.SecretRefs) != 0 {
		if source == nil || source.secrets == nil {
			return errConfigurationSecret
		}
		nc, err := a.currentController(ctx)
		if err != nil {
			return err
		}
		if err := source.secrets.unchanged(nc, &fresh); err != nil {
			return err
		}
	} else if source != nil && source.secrets != nil {
		return errConfigurationSecret
	}
	return nil
}

// startConfiguration is the sole submission permit. Only its successful CAS
// caller may make ONE request. A conflict, timeout or a repeated call must not
// submit, even if the durable phase appears to be Submitting. Recovery observes
// or reconciles a receipt; it never repeats this transition.
func (a *adapter) startConfiguration(ctx context.Context, config *configapi.NetworkControllerConfig, source *configSourceSnapshot, id string, expected ...configurationResourceIdentity) error {
	nc, err := a.currentController(ctx)
	if err != nil {
		return err
	}
	if err = a.checkConfigurationSnapshot(ctx, config, source); err != nil {
		return err
	}
	if nc.Status.Configuration == nil || nc.Status.Configuration.Operation == nil {
		return errConfigurationTransition
	}
	if nc.Status.Configuration.MetadataHandoff != nil {
		if len(expected) != 1 || expected[0].metadata == nil || expected[0].metadata.permit != nil || !a.metadataAdmissionReady() {
			return errConfigurationBusy
		}
	} else if len(expected) == 1 && expected[0].metadata != nil {
		return errConfigurationTransition
	}
	op := nc.Status.Configuration.Operation
	if pnpRemovalSelected(op.Resource) && a.metadataAdmissionReady() && nc.Status.Configuration.MetadataHandoff == nil {
		return errConfigurationBusy
	}
	if pnpRemovalSelected(op.Resource) {
		if err := pnpRemovalAdmission(nc.Status.Configuration, config, source, op); err != nil {
			return err
		}
	}
	if tagFinalizeSelected(op.Resource) && !tagFinalizationMutationAllowed(config, source, op.Resource, op.Action) {
		return errConfigurationTransition
	}
	if !config.DeletionTimestamp.IsZero() && !featureFinalizationMutationAllowed(config, source, op.Resource, op.Action) && !tagFinalizationMutationAllowed(config, source, op.Resource, op.Action) && !discoveryCleanupSubmissionAllowed(config, source, op) {
		return errConfigurationTransition
	}
	if err := validateSSIDCredentialHistory(op.Section, op.Resource, op.CredentialVersions, op.SSIDCredentialHistory); err != nil {
		return err
	}
	if op.PhysicalGroup != nil || op.CancellationRequestedAt != nil || isReservedConfigurationGroupResource(op.Section, op.Resource) {
		return errConfigurationDeviceAdmission
	}
	if op.ID != id || op.Phase != "Prepared" || op.ConfigUID != string(config.UID) || op.ConfigName != config.Name || op.ConfigGeneration != config.Generation || op.ControllerGeneration != nc.Generation {
		return errConfigurationTransition
	}
	var uid, version string
	if source != nil {
		uid, version = string(source.uid), source.version
	}
	if op.SourceUID != uid || op.SourceVersion != version {
		return errConfigurationStale
	}
	if len(expected) > 1 {
		return errConfigurationTransition
	}
	if len(expected) == 1 && (op.TargetID != expected[0].target || op.ParentID != expected[0].parent || op.TargetSerial != expected[0].serial) {
		return errConfigurationStale
	}
	if (len(op.CredentialVersions) > 0 || len(op.SSIDCredentialHistory) > 0 || op.DependencyVersions != nil) && len(expected) == 0 {
		return errConfigurationStale
	}
	if len(expected) == 1 && (!slices.Equal(op.CredentialVersions, expected[0].credentials) || !slices.Equal(op.SSIDCredentialHistory, expected[0].ssidCredentialHistory) || !reflect.DeepEqual(op.DependencyVersions, expected[0].dependencies)) {
		return errConfigurationStale
	}
	if len(expected) == 1 {
		if err := validateConfigurationDependencyVersions(op.Section, op.Resource, op.Action, expected[0]); err != nil {
			return err
		}
	}
	if err := validateConfigurationCredentialVersions(source, op.Section, op.CredentialVersions); err != nil {
		return err
	}
	if discoveryCredentialRetirementSelected(op.Resource) {
		if err := discoveryCredentialRetirementCapacity(nc.Status.Configuration, op); err != nil {
			return err
		}
	}
	if discoveryCredentialInputSelected(op.Resource) {
		if err := discoveryCredentialCreateCapacity(nc.Status.Configuration, op); err != nil {
			return err
		}
	}
	if nc.Status.Configuration.MetadataHandoff != nil {
		if !validPnPRemovalOperation(op) {
			return errConfigurationTransition
		}
		permit, err := a.claimMetadataOperation(ctx, nc)
		if err != nil {
			return err
		}
		expected[0].metadata.permit = permit
		return nil
	}
	op.Phase = "Submitting"
	now := metav1.Now()
	op.SubmittedAt = &now
	return a.statusWriter.Status().Update(ctx, nc)
}

// submitConfigurationOperation connects the durable permit to exactly one
// resource API invocation. Resource compilers supply a fully validated closure
// using the existing no-replay mutation transport. A receipt-persistence error
// leaves Submitting in place; the caller cannot use an error as a retry permit.
func (a *adapter) submitConfigurationOperation(ctx context.Context, config *configapi.NetworkControllerConfig, source *configSourceSnapshot, section, resource, action string, submit func(context.Context) (Task, error), identity ...configurationResourceIdentity) error {
	if submit == nil {
		return errConfigurationTransition
	}
	return a.submitConfigurationReceipt(ctx, config, source, section, resource, action, func(ctx context.Context) (configurationReceipt, error) {
		task, err := submit(ctx)
		return configurationReceipt{task: task}, err
	}, identity...)
}

type configurationReceipt struct {
	noDispatch *configurationPreSubmitRefusal
	task       Task
	completed  bool
	createdID  string
}

func (a *adapter) submitConfigurationWrite(ctx context.Context, config *configapi.NetworkControllerConfig, source *configSourceSnapshot, section string, write *configurationWrite) error {
	if write == nil {
		return errConfigurationTransition
	}
	count := 0
	if write.submit != nil {
		count++
	}
	if write.submitCompleted != nil {
		count++
	}
	if write.submitCreated != nil {
		count++
	}
	if count != 1 {
		return errConfigurationTransition
	}
	if write.submitCreated != nil {
		if write.action != "Create" || write.identity.target != "" || write.identity.serial != "" || write.identity.dependencies != nil || len(write.identity.credentials) != 0 {
			return errConfigurationTransition
		}
		return a.submitConfigurationReceipt(ctx, config, source, section, write.resource, write.action, func(ctx context.Context) (configurationReceipt, error) {
			id, err := write.submitCreated(ctx)
			if err == nil && !validAPIID(id) {
				err = errInvalidResponse
			}
			var proof *configurationPreSubmitRefusal
			if err != nil && id == "" && noDispatchWriteSupported(section, write) {
				proof = write.noDispatch(err)
			}
			return configurationReceipt{completed: err == nil, createdID: id, noDispatch: proof}, err
		}, write.identity)
	}
	if write.identity.serial != "" {
		// A synchronous receipt has no task identity for the reservation completion
		// protocol. It remains unsupported instead of fabricating completion proof.
		admit, err := a.physicalConfigurationAdmission(write)
		if err != nil {
			return err
		}
		return a.submitConfigurationReceiptWithAdmission(ctx, config, source, section, write.resource, write.action, func(ctx context.Context) (configurationReceipt, error) {
			task, err := write.submit(ctx)
			return configurationReceipt{task: task}, err
		}, admit, write.identity)
	}
	if write.submit != nil {
		if noDispatchWriteSupported(section, write) {
			return a.submitConfigurationReceipt(ctx, config, source, section, write.resource, write.action, configurationTaskWriteReceipt(section, write), write.identity)
		}
		return a.submitConfigurationOperation(ctx, config, source, section, write.resource, write.action, write.submit, write.identity)
	}
	return a.submitConfigurationReceipt(ctx, config, source, section, write.resource, write.action, configurationCompletedWriteReceipt(section, write), write.identity)
}

func configurationCompletedWriteReceipt(section string, write *configurationWrite) func(context.Context) (configurationReceipt, error) {
	return func(ctx context.Context) (configurationReceipt, error) {
		err := write.submitCompleted(ctx)
		var proof *configurationPreSubmitRefusal
		if err != nil && noDispatchWriteSupported(section, write) {
			proof = write.noDispatch(err)
		}
		return configurationReceipt{completed: err == nil, noDispatch: proof}, err
	}
}

// New and exactly resumed Prepared metadata writes must claim the same original
// guard proof. Ordinary API failures never acquire this private capability.
func configurationTaskWriteReceipt(section string, write *configurationWrite) func(context.Context) (configurationReceipt, error) {
	return func(ctx context.Context) (configurationReceipt, error) {
		task, err := write.submit(ctx)
		var proof *configurationPreSubmitRefusal
		if err != nil && task == (Task{}) && noDispatchWriteSupported(section, write) {
			proof = write.noDispatch(err)
		}
		return configurationReceipt{task: task, noDispatch: proof}, err
	}
}

func (a *adapter) submitConfigurationReceipt(ctx context.Context, config *configapi.NetworkControllerConfig, source *configSourceSnapshot, section, resource, action string, submit func(context.Context) (configurationReceipt, error), identity ...configurationResourceIdentity) error {
	return a.submitConfigurationReceiptWithAdmission(ctx, config, source, section, resource, action, submit, nil, identity...)
}

func (a *adapter) submitConfigurationReceiptWithAdmission(ctx context.Context, config *configapi.NetworkControllerConfig, source *configSourceSnapshot, section, resource, action string, submit func(context.Context) (configurationReceipt, error), admit configurationAdmissionStep, identity ...configurationResourceIdentity) error {
	if submit == nil {
		return errConfigurationTransition
	}
	if len(identity) == 1 && identity[0].serial != "" && admit == nil {
		return errConfigurationDeviceAdmission
	}
	op, err := a.prepareConfiguration(ctx, config, source, section, resource, action, identity...)
	if err != nil {
		return err
	}
	expected := configurationResourceIdentity{}
	if len(identity) == 1 {
		expected = identity[0]
	}
	return a.submitPreparedConfigurationReceipt(ctx, config, source, op, section, resource, action, submit, admit, expected)
}

// Resume only an exact Prepared journal. Admission still has to create a new
// one-use claim permit; persisted Submitting/Accepted/Unknown work cannot enter
// this path. New and resumed operations share every subsequent transition.
func (a *adapter) submitPreparedConfigurationReceipt(ctx context.Context, config *configapi.NetworkControllerConfig, source *configSourceSnapshot, op *ciskov1.NetworkControllerConfigurationOperation, section, resource, action string, submit func(context.Context) (configurationReceipt, error), admit configurationAdmissionStep, expected configurationResourceIdentity) error {
	return a.submitPreparedConfigurationReceiptWithPreflight(ctx, config, source, op, section, resource, action, nil, submit, admit, expected)
}

// Optional product preflight runs before consuming the physical permit. Existing
// callers use nil and retain their original ordering. A product using this hook
// must keep submit write-only, so the final live permit follows all native reads.
func (a *adapter) submitPreparedConfigurationReceiptWithPreflight(ctx context.Context, config *configapi.NetworkControllerConfig, source *configSourceSnapshot, op *ciskov1.NetworkControllerConfigurationOperation, section, resource, action string, preflight func(context.Context) error, submit func(context.Context) (configurationReceipt, error), admit configurationAdmissionStep, expected configurationResourceIdentity) error {
	if config == nil || op == nil || op.PhysicalGroup != nil || op.CancellationRequestedAt != nil || submit == nil || op.Phase != "Prepared" || op.CreatedResourceID != "" || op.TaskID != "" || op.SubmittedAt != nil {
		return errConfigurationTransition
	}
	if op.TargetSerial != "" && admit == nil {
		return errConfigurationDeviceAdmission
	}
	var sourceUID, sourceVersion string
	if source != nil {
		sourceUID, sourceVersion = string(source.uid), source.version
	}
	if op.ConfigUID != string(config.UID) || op.ConfigName != config.Name || op.ConfigGeneration != config.Generation || op.ControllerGeneration != a.generation || op.Section != section || op.Resource != resource || op.Action != action || op.SourceUID != sourceUID || op.SourceVersion != sourceVersion || op.TargetID != expected.target || op.ParentID != expected.parent || op.TargetSerial != expected.serial || !slices.Equal(op.CredentialVersions, expected.credentials) || !slices.Equal(op.SSIDCredentialHistory, expected.ssidCredentialHistory) || !reflect.DeepEqual(op.DependencyVersions, expected.dependencies) {
		return errConfigurationStale
	}
	before, err := a.currentController(ctx)
	if err != nil {
		return err
	}
	if before.Status.Configuration == nil || !reflect.DeepEqual(before.Status.Configuration.Operation, op) {
		return errConfigurationStale
	}
	if err := a.checkConfigurationSnapshot(ctx, config, source); err != nil {
		return err
	}
	if before.Status.Configuration.MetadataHandoff != nil && (expected.metadata == nil || admit != nil || !validPnPRemovalOperation(op)) {
		return errConfigurationBusy
	}
	var permit configurationDispatchCheck
	if admit != nil {
		permit, err = admit(ctx)
		if err != nil {
			return err
		}
		if permit == nil {
			return errConfigurationDeviceAdmission
		}
	}
	if err = a.startConfiguration(ctx, config, source, op.ID, expected); err != nil {
		return err
	}
	if expected.metadata != nil {
		if expected.metadata.permit == nil {
			return errConfigurationBusy
		}
		permit = expected.metadata.permit
	}
	// Re-read the write-ahead record: an API-server schema change must not
	// silently strip identity while persisting the submission permit.
	nc, err := a.currentController(ctx)
	if err != nil {
		return err
	}
	if err = a.checkConfigurationSnapshot(ctx, config, source); err != nil {
		return err
	}
	if nc.Status.Configuration == nil || nc.Status.Configuration.Operation == nil {
		return errConfigurationStale
	}
	stored := nc.Status.Configuration.Operation
	if stored.ID != op.ID || stored.Phase != "Submitting" || stored.CreatedResourceID != "" || stored.SubmittedAt == nil || stored.TaskID != "" ||
		stored.ConfigUID != string(config.UID) || stored.ConfigName != config.Name || stored.ConfigGeneration != config.Generation ||
		stored.ControllerGeneration != a.generation || stored.Section != section || stored.Resource != resource || stored.Action != action ||
		stored.SourceUID != op.SourceUID || stored.SourceVersion != op.SourceVersion ||
		stored.TargetID != expected.target || stored.ParentID != expected.parent || stored.TargetSerial != expected.serial ||
		!slices.Equal(stored.CredentialVersions, expected.credentials) || !slices.Equal(stored.SSIDCredentialHistory, expected.ssidCredentialHistory) || !reflect.DeepEqual(stored.DependencyVersions, expected.dependencies) {
		return errConfigurationStale
	}
	if preflight != nil {
		if err := preflight(ctx); err != nil {
			return err
		}
	}
	if permit != nil {
		if err := permit.Check(ctx, time.Now()); err != nil {
			return err
		}
	}
	receipt, submissionErr := submit(ctx)
	// Persist the uncertain outcome even when the request's context timed out.
	receiptCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 10*time.Second)
	defer cancel()
	if submissionErr != nil && receipt.task == (Task{}) && !receipt.completed && receipt.createdID == "" && receipt.noDispatch != nil {
		if receipt.noDispatch.operationID != "" || !receipt.noDispatch.claimed.Load() {
			return errConfigurationTransition
		}
		receipt.noDispatch.operationID = stored.ID
		if err := a.retireProvenNoDispatch(receiptCtx, stored.DeepCopy(), receipt.noDispatch); err != nil {
			return err
		}
		return errConfigurationNotDispatched
	}
	if submissionErr != nil || (receipt.createdID != "" && (!receipt.completed || !validAPIID(receipt.createdID))) || (!receipt.completed && !validAPIID(receipt.task.ID)) || (receipt.completed && receipt.task.ID != "") {
		if err := a.advanceConfiguration(receiptCtx, op.ID, "Unknown", ""); err != nil {
			return err
		}
		return errConfigurationBusy
	}
	if receipt.completed {
		return a.recordConfigurationSynchronous(receiptCtx, op.ID, receipt.createdID)
	}
	return a.advanceConfiguration(receiptCtx, op.ID, "Accepted", receipt.task.ID)
}

// advanceConfiguration only stores sanitized execution evidence. Accepted
// requires the receipt returned by the single submission; Verifying requires
// terminal task observation (or deliberate recovery of an uncertain request).
// Verified requires an authoritative read-back by the resource executor. No
// journal transition itself interprets a controller task as configuration truth.
func (a *adapter) advanceConfiguration(ctx context.Context, id, phase, taskID string) error {
	// Retry only rejected Kubernetes writes; never replay the controller request.
	// Every attempt rereads and revalidates the operation identity and phase.
	return retry.RetryOnConflict(retry.DefaultBackoff, func() error {
		if !configurationOperationID.MatchString(id) {
			return errConfigurationTransition
		}
		nc, err := a.configurationRecoveryController(ctx)
		if err != nil {
			return err
		}
		if nc.Status.Configuration == nil || nc.Status.Configuration.Operation == nil {
			return errConfigurationTransition
		}
		op := nc.Status.Configuration.Operation
		if op.PhysicalGroup != nil || op.CancellationRequestedAt != nil || isReservedConfigurationGroupResource(op.Section, op.Resource) {
			return errConfigurationDeviceAdmission
		}
		if op.ID != id {
			return errConfigurationTransition
		}
		allowed := false
		switch phase {
		case "Accepted":
			allowed = op.Phase == "Submitting" && validAPIID(taskID)
		case "Unknown":
			allowed = op.Phase == "Submitting" || op.Phase == "Accepted" || op.Phase == "Verifying"
		case "Verifying":
			allowed = op.Phase == "Accepted" || op.Phase == "Unknown"
		case "Verified":
			// Write-only credentials require a separate atomic applied-version
			// receipt, bound to the observed resource UUID. Metadata alone cannot
			// take this generic verification path or discard the rotation binding.
			allowed = op.Phase == "Verifying" && !switchingCreateSelected(op.Resource) && !siteRetirementSettlementRequired(nc.Status.Configuration, op) && !networkSettingApplicationSelected(op) && op.CreatedResourceID == "" && !isPnPRegistrationOperation(op.Section, op.Resource) && !pnpRemovalSelected(op.Resource) && !isPnPSiteClaimOperation(op.Section, op.Resource) && !isFloorImageOperation(op.Section, op.Resource) && !featureCreateSelected(op.Resource) && !featureUpdateSelected(op.Resource) && !strings.HasPrefix(op.Resource, templateApplyPrefix) && !strings.HasPrefix(op.Resource, templateCreatePrefix) && !featureDeleteSelected(op.Resource) && !ownedDiscoverySelected(op.Resource) && !discoveryCredentialInputSelected(op.Resource) && !discoveryCredentialRetirementSelected(op.Resource) && !ipamOwnedSelected(op.Resource) && len(op.CredentialVersions) == 0 && !isWirelessAPCredentialResource(op.Section, op.Resource) && !isWirelessSSIDCredentialResource(op.Section, op.Resource)
		}
		if !allowed || (phase != "Accepted" && taskID != "") {
			return errConfigurationTransition
		}
		op.Phase = phase
		if phase == "Verified" && (op.TargetSerial != "" || validTemplateCommitReceipt(op)) {
			var receiptErr error
			if op.TargetSerial != "" {
				receiptErr = a.retainPhysicalActionReceipt(ctx, nc)
			} else {
				receiptErr = a.retainConfigurationActionReceipt(ctx, nc, op.Resource)
			}
			if receiptErr != nil {
				return receiptErr
			}
			return a.persistConfigurationActionReceipt(ctx, nc)
		}
		if phase == "Accepted" {
			op.TaskID = taskID
		}
		return a.statusWriter.Status().Update(ctx, nc)
	})
}

// finishConfiguration retains ownership but releases the operation slot after
// verified read-back. Prepared can be cancelled because no submission permit
// has been issued. All uncertain and failed-task states retain their fence.
func (a *adapter) finishConfiguration(ctx context.Context, id string) error {
	return a.finishConfigurationOperation(ctx, id, nil)
}

// expected constrains terminal recovery to the exact selected operation on the
// authoritative receipt/CAS read. Legacy Prepared cancellation callers retain
// their existing path; receipt-only recovery cannot change phase or family.
func (a *adapter) finishConfigurationOperation(ctx context.Context, id string, expected *ciskov1.NetworkControllerConfigurationOperation) error {
	nc, err := a.configurationRecoveryController(ctx)
	if err != nil {
		return err
	}
	if nc.Status.Configuration == nil || nc.Status.Configuration.Operation == nil {
		return errConfigurationTransition
	}
	if nc.Status.Configuration.MetadataHandoff != nil {
		return errConfigurationBusy
	}
	op := nc.Status.Configuration.Operation
	if expected != nil && !reflect.DeepEqual(op, expected) {
		return errConfigurationStale
	}
	if op.PhysicalGroup != nil && op.Phase == "Cancelling" && op.ID == id && a.configurationAdmissionReady() {
		return contract.FinishCancelledControllerGroup(ctx, a.statusReader, a.statusWriter, nc, a.authorizeConfigurationGroupMember, time.Now())
	}
	if op.PhysicalGroup != nil && op.CancellationRequestedAt == nil {
		if op.ID != id || op.Phase != "Verified" || !a.configurationAdmissionReady() {
			return errConfigurationDeviceAdmission
		}
		return contract.FinishVerifiedControllerGroup(ctx, a.statusReader, a.statusWriter, nc, a.authorizeConfigurationGroupMember, time.Now())
	}
	if op.PhysicalGroup != nil || op.CancellationRequestedAt != nil || isReservedConfigurationGroupResource(op.Section, op.Resource) {
		return errConfigurationDeviceAdmission
	}
	if op.ID != id || (op.Phase != "Verified" && op.Phase != "Prepared") {
		return errConfigurationBusy
	}
	if pnpRemovalSelected(op.Resource) && op.Phase == "Prepared" && !validPnPRemovalOperation(op) {
		return errConfigurationBusy
	}
	if siteRetirementSettlementRequired(nc.Status.Configuration, op) && op.Phase == "Prepared" {
		if op.SubmittedAt != nil || op.TaskID != "" {
			return errConfigurationBusy
		}
		for _, row := range nc.Status.Configuration.ActionApplications {
			if row.Operation.ID == op.ID {
				return errConfigurationBusy
			}
		}
	}
	if switchingCreateSelected(op.Resource) && op.Phase == "Verified" && !switchingCreatorReceiptPresent(nc.Status.Configuration, op) {
		return errConfigurationOwned
	}
	if op.Phase == "Verified" && op.Section == "authentication_templates" {
		if err := validateGlobalAuthCompletionBinding(op); err != nil {
			return err
		}
	}
	if selected, valid := templateMembershipJournalBinding(op.Section, op.Resource); op.Phase == "Verified" && selected &&
		(!valid || op.Action != "Update" || !validAPIID(op.TargetID) || !validAPIID(op.ParentID) || op.TargetSerial != "" || op.CreatedResourceID != "" || op.PhysicalGroup != nil || op.DependencyVersions != nil || len(op.CredentialVersions) != 0) {
		return errTemplateMembershipProjectBinding
	}
	if op.Phase == "Verified" && op.Section == "network_profiles" && strings.HasPrefix(op.Resource, "profiles/") && !validSwitchingProfileBinding(op.ParentID) && !legacyEmptySwitchingProfileBinding(op.ParentID) {
		return errSwitchingProfileBinding
	}
	// Site-claim acknowledgements have no native task/completion receipt.
	// Until the independent completion verifier is installed, never release
	// one through generic verification, even if an older schema prunes scope.
	if op.Phase == "Verified" && isPnPSiteClaimOperation(op.Section, op.Resource) {
		return errConfigurationBusy
	}
	if op.Phase == "Verified" && pnpRemovalSelected(op.Resource) && !pnpRemovedReceiptPresent(nc.Status.Configuration, op) {
		return errConfigurationBusy
	}
	if op.Phase == "Verified" && (op.CreatedResourceID != "" || isPnPRegistrationOperation(op.Section, op.Resource)) && !pnpRegistrationReceiptPresent(nc.Status.Configuration, op) {
		return errConfigurationBusy
	}
	if networkSettingApplicationSelected(op) && op.Phase == "Verified" && !networkSettingApplicationPresent(nc.Status.Configuration, op) {
		return errConfigurationBusy
	}
	if wirelessTagPrivateSelected(op.Resource) && op.Phase == "Verified" && !wirelessTagReceiptPresent(nc.Status.Configuration, op) {
		return errConfigurationOwned
	}
	if profileSiteApplicationSelected(op) && op.Phase == "Verified" && !profileSiteApplicationPresent(nc.Status.Configuration, op) {
		return errProfileSiteApplications
	}
	if featureDeleteSelected(op.Resource) && op.Phase == "Verified" && !featureDeletedReceiptPresent(nc.Status.Configuration, op) {
		return errConfigurationTransition
	}
	if featureUpdateSelected(op.Resource) && op.Phase == "Verified" && !featureAppliedReceiptPresent(nc.Status.Configuration, op) {
		return errConfigurationTransition
	}
	if strings.HasPrefix(op.Resource, templateApplyPrefix) && op.Phase == "Verified" && !templateAppliedReceiptPresent(nc.Status.Configuration, op) {
		return errConfigurationBusy
	}
	if strings.HasPrefix(op.Resource, templateCreatePrefix) && op.Phase == "Verified" && !templateCreatedReceiptPresent(nc.Status.Configuration, op) {
		return errConfigurationBusy
	}
	if siteRetirementSettlementRequired(nc.Status.Configuration, op) && op.Phase == "Verified" && !siteRetiredReceiptPresent(nc.Status.Configuration, op) {
		return errConfigurationBusy
	}
	siteCreatorProof, siteAppliedProof := siteOwnedSettlementRequired(nc.Status.Configuration, op)
	if plannedAPIdentitySettlementRequired(nc.Status.Configuration, op) && op.Phase == "Verified" && !plannedAPIdentityReceiptPresent(nc.Status.Configuration, op) {
		return errConfigurationTransition
	}
	if plannedAPAppliedSettlementRequired(nc.Status.Configuration, op) && op.Phase == "Verified" && !plannedAPAppliedReceiptPresent(nc.Status.Configuration, op) {
		return errConfigurationTransition
	}
	if siteCreatorProof && op.Phase == "Verified" && !siteCreatedReceiptPresent(nc.Status.Configuration, op) {
		return errConfigurationBusy
	}
	if siteAppliedProof && op.Phase == "Verified" && !siteAppliedReceiptPresent(nc.Status.Configuration, op) {
		return errConfigurationBusy
	}
	if tagDeleteSelected(op.Resource) && op.Phase == "Verified" && !tagDeletedReceiptPresent(nc.Status.Configuration, op) {
		return errConfigurationBusy
	}
	if tagOwnedSelected(op.Resource) && op.Phase == "Verified" && !tagOwnedReceiptPresent(nc.Status.Configuration, op) {
		return errConfigurationBusy
	}
	if featureCreateSelected(op.Resource) && op.Phase == "Verified" && !featureCreatedReceiptPresent(nc.Status.Configuration, op) {
		return errConfigurationBusy
	}
	if ipamOwnedSelected(op.Resource) && op.Phase == "Verified" && !ipamOwnedReceiptPresent(nc.Status.Configuration, op) {
		return errConfigurationBusy
	}
	if ownedDiscoverySelected(op.Resource) && op.Phase == "Verified" && !ownedDiscoveryReceiptPresent(nc.Status.Configuration, op, discoveryRetiredReceiptKeys(nc, op.ConfigUID)) {
		return errConfigurationBusy
	}
	if discoveryCredentialRetirementSelected(op.Resource) && op.Phase == "Verified" && discoveryCredentialCustodyRequired(nc, op.ConfigUID) {
		if _, err := a.discoveryCredentialOperationHistory(ctx, nc, op); err != nil {
			return err
		}
	}
	if discoveryCredentialRetirementSelected(op.Resource) && op.Phase == "Verified" && !discoveryCredentialRetirementReceiptPresent(nc.Status.Configuration, op) {
		return errConfigurationBusy
	}
	if discoveryCredentialInputSelected(op.Resource) && op.Phase == "Verified" && discoveryCredentialCustodyRequired(nc, op.ConfigUID) {
		if _, err := a.discoveryCredentialOperationHistory(ctx, nc, op); err != nil {
			return err
		}
	}
	if discoveryCredentialInputSelected(op.Resource) && op.Phase == "Verified" && (!discoveryCredentialReceiptPairPresent(nc.Status.Configuration, op) || !discoveryCredentialHistoryValid(nc.Status.Configuration, op.ConfigUID)) {
		return errConfigurationBusy
	}
	if op.Phase == "Verified" && (isGlobalCredentialResource(op.Section, op.Resource) || isWirelessAPCredentialResource(op.Section, op.Resource) || isWirelessSSIDCredentialResource(op.Section, op.Resource)) && len(op.CredentialVersions) == 0 {
		return errConfigurationBusy
	}
	if err := validateSSIDCredentialHistory(op.Section, op.Resource, op.CredentialVersions, op.SSIDCredentialHistory); err != nil {
		return errConfigurationBusy
	}
	if op.Phase == "Verified" && isWirelessSSIDCredentialResource(op.Section, op.Resource) {
		// An older schema can retain version counters while pruning semantic
		// keys from both the operation and receipt. Equality is insufficient.
		if _, err := wirelessSSIDCredentialRotation(op.CredentialVersions, op.CredentialVersions); err != nil {
			return errConfigurationBusy
		}
	}
	if op.Phase == "Verified" && op.Section == "system_settings" {
		if _, err := canonicalAAAAddress(op.Resource); err == nil && len(op.CredentialVersions) == 0 {
			// A schema downgrade may prune both the operation binding and
			// applied receipt. The AAA resource identity still requires proof.
			return errConfigurationBusy
		}
	}
	if op.Phase == "Verified" && len(op.CredentialVersions) > 0 && !discoveryCredentialRetirementSelected(op.Resource) {
		found := false
		for _, receipt := range nc.Status.Configuration.CredentialApplications {
			if receipt.Section == op.Section && receipt.Resource == op.Resource && receipt.ConfigUID == op.ConfigUID &&
				receipt.OperationID == op.ID && validAPIID(receipt.TargetID) && (op.TargetID == "" || op.TargetID == receipt.TargetID) &&
				slices.Equal(receipt.Versions, op.CredentialVersions) && slices.Equal(receipt.SSIDCredentialHistory, op.SSIDCredentialHistory) {
				found = true
			}
		}
		if !found {
			return errConfigurationBusy
		}
	}
	if op.Phase == "Verified" && strings.HasPrefix(op.Resource, wirelessProvisionRecordPrefix) && !configurationActionReceiptPresent(nc.Status.Configuration, op) {
		return errConfigurationBusy
	}
	if op.Phase == "Verified" && isFloorImageOperation(op.Section, op.Resource) && !floorImageOperationReceiptPresent(nc.Status.Configuration, op) {
		return errConfigurationBusy
	}
	if op.Phase == "Verified" && isMetadataCredentialOperation(op.Section, op.Resource) && !metadataCredentialOperationReceiptPresent(nc.Status.Configuration, op) {
		return errConfigurationBusy
	}
	if op.TargetSerial != "" {
		if err := a.configurationReservationSettled(ctx, nc); err != nil {
			return err
		}
	}
	nc.Status.Configuration.Operation = nil
	return a.statusWriter.Status().Update(ctx, nc)
}

// Observation and cancellation must remain possible after pause, spec edits,
// or a deletion request. Submission still uses currentController's strict gate.
func (a *adapter) configurationRecoveryController(ctx context.Context) (*ciskov1.NetworkController, error) {
	var nc ciskov1.NetworkController
	if err := a.statusReader.Get(ctx, a.key, &nc); err != nil {
		return nil, err
	}
	if nc.UID != a.uid || nc.Spec.Type != TypeName {
		return nil, errStaleController
	}
	return &nc, nil
}

// Only a validated synchronous response reaches this transition. A restarted
// worker seeing Submitting cannot infer that the response was received.
func (a *adapter) recordConfigurationCompleted(ctx context.Context, id string) error {
	return a.recordConfigurationSynchronous(ctx, id, "")
}

func (a *adapter) recordConfigurationSynchronous(ctx context.Context, id, createdID string) error {
	if createdID != "" && !validAPIID(createdID) {
		return errConfigurationTransition
	}
	// Retry only rejected Kubernetes writes; never replay the controller request.
	// Every attempt rereads and revalidates the operation identity and phase.
	return retry.RetryOnConflict(retry.DefaultBackoff, func() error {
		nc, err := a.configurationRecoveryController(ctx)
		if err != nil {
			return err
		}
		if nc.Status.Configuration == nil || nc.Status.Configuration.Operation == nil {
			return errConfigurationTransition
		}
		op := nc.Status.Configuration.Operation
		if op.ID != id || op.Phase != "Submitting" || op.SubmittedAt == nil || op.TaskID != "" || op.CreatedResourceID != "" || op.PhysicalGroup != nil || op.CancellationRequestedAt != nil || isReservedConfigurationGroupResource(op.Section, op.Resource) {
			return errConfigurationTransition
		}
		if createdID != "" && (op.Action != "Create" || op.TargetID != "" || op.TargetSerial != "" || op.PhysicalGroup != nil || op.DependencyVersions != nil || op.CancellationRequestedAt != nil || len(op.CredentialVersions) != 0) {
			return errConfigurationTransition
		}
		if isPnPSiteClaimOperation(op.Section, op.Resource) && (createdID != "" || op.Action != "Update" || !validAPIID(op.TargetID) || !validAPIID(op.ParentID) || op.TargetSerial == "" || op.PhysicalGroup != nil || op.DependencyVersions != nil || op.CancellationRequestedAt != nil || len(op.CredentialVersions) != 0) {
			return errConfigurationTransition
		}
		if pnpRemovalSelected(op.Resource) && (createdID != "" || !validPnPRemovalOperation(op)) {
			return errConfigurationTransition
		}
		op.Phase = "Verifying"
		op.CreatedResourceID = createdID
		if err := a.statusWriter.Status().Update(ctx, nc); err != nil {
			return err
		}
		if createdID != "" || isPnPSiteClaimOperation(op.Section, op.Resource) || pnpRemovalSelected(op.Resource) {
			fresh, err := a.configurationRecoveryController(ctx)
			if err != nil {
				return err
			}
			if fresh.Status.Configuration == nil || fresh.Status.Configuration.Operation == nil || fresh.Status.Configuration.Operation.CreatedResourceID != createdID || !reflect.DeepEqual(fresh.Status.Configuration.Operation, op) {
				return errConfigurationStale
			}
		}
		return nil
	})
}

var errConfigurationDeviceAdmission = errors.New("controller configuration device mutation admission is not yet available")
