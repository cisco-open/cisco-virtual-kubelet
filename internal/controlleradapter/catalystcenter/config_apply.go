// Copyright © 2026 Cisco Systems Inc.
// Licensed under the Apache License, Version 2.0 (the "License");
package catalystcenter

import (
	"context"
	"errors"
	"slices"
	"strings"
	"time"

	configapi "github.com/cisco/virtual-kubelet-cisco/api/config/v1alpha1"
	ciskov1 "github.com/cisco/virtual-kubelet-cisco/api/v1alpha1"
	contract "github.com/cisco/virtual-kubelet-cisco/internal/controllerconfiguration"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

func supportedConfigurationApply(config *configapi.NetworkControllerConfig) bool {
	if config.Spec.Mode != configapi.NetworkControllerApplyModeApply {
		return false
	}
	sections := config.Spec.ManagedSections
	if !combinedTagSections(sections) && !combinedFabricIPAMSections(sections) && (len(sections) != 1 || !reportSectionSupported(sections[0])) {
		return false
	}
	copy := config.DeepCopy()
	copy.Spec.Mode = configapi.NetworkControllerApplyModeReport
	if sitePruneApplyShape(config) {
		copy.Spec.PrunePolicy = configapi.NetworkControllerRetentionPolicyRetain
	}
	if featureDeletionShape(config) && config.Spec.DeletionPolicy == configapi.NetworkControllerRetentionPolicyDelete && hasFeatureConfigFinalizer(config) {
		copy.Spec.DeletionPolicy = configapi.NetworkControllerRetentionPolicyRetain
	}
	if tagDeletionShape(config) && config.Spec.DeletionPolicy == configapi.NetworkControllerRetentionPolicyDelete && hasTagCleanupFinalizer(config) {
		copy.Spec.DeletionPolicy = configapi.NetworkControllerRetentionPolicyRetain
	}
	if (discoveryConfigSpecShape(config) || discoveryCredentialDeletionSpecShape(config)) && hasDiscoveryConfigFinalizer(config) {
		copy.Spec.DeletionPolicy = configapi.NetworkControllerRetentionPolicyRetain
	}
	// Cleanup support has section-specific execution and retained ownership
	// gates. Do not widen Report or other sections to arbitrary deletion.
	if len(sections) == 1 && (sections[0] == "inventory" || (sections[0] == "network_settings" || sections[0] == "feature_templates" || sections[0] == "network_profiles") && (len(config.Spec.SecretRefs) == 0 || wirelessTagComparisonConfig(config))) && copy.Spec.PrunePolicy == configapi.NetworkControllerRetentionPolicyDelete {
		copy.Spec.PrunePolicy = configapi.NetworkControllerRetentionPolicyRetain
	}
	if (len(sections) == 1 && sections[0] == "templates" || combinedTagSections(sections)) && len(config.Spec.SecretRefs) == 0 && copy.Spec.PrunePolicy == configapi.NetworkControllerRetentionPolicyDelete {
		copy.Spec.PrunePolicy = configapi.NetworkControllerRetentionPolicyRetain
	}
	if len(copy.Spec.SecretRefs) > 0 {
		if discoveryCredentialApplyShape(config) {
			copy.Spec.SecretRefs = nil
			return supportedConfigReport(copy)
		}
		if templateComparisonConfig(config) || templateNativeComparisonConfig(config) || siteComparisonConfig(config) || networkComparisonConfig(config) || wirelessTagComparisonConfig(config) {
			copy.Spec.SecretRefs = nil
			return supportedConfigReport(copy)
		}
		if len(sections) != 1 || (sections[0] != "system_settings" && sections[0] != "network_settings" && sections[0] != "wireless") {
			return false
		}
		// Product parsing must authorize every destination before resolving.
		copy.Spec.SecretRefs = nil
	}
	return supportedConfigReport(copy)
}

// One reconcile performs at most one submission. Subsequent reconciles poll
// the persisted receipt and independently verify attributes before advancing
// to another resource. An ambiguous submission stays fenced for recovery.
func (r *configReconciler) evaluateConfigurationApply(ctx context.Context, nc *ciskov1.NetworkController, config *configapi.NetworkControllerConfig, status *configapi.NetworkControllerConfigStatus) (*configSourceSnapshot, bool, string, string) {
	source, err := loadControllerSource(ctx, r.a.statusReader, config)
	if err != nil {
		return nil, false, "SourceUnavailable", "Cannot read controller intent source"
	}
	if discoveryCredentialPlanSelected(nc, config, source) && (nc.Status.Configuration == nil || nc.Status.Configuration.Operation == nil) && !discoveryCredentialAttestationMatchesSource(nc, config, source) {
		status.Phase = configapi.NetworkControllerConfigPhaseWaiting
		return source, true, "DiscoveryCredentialScopeChanged", "Current source changed after validation; a new attestation is required before planning"
	}
	return r.evaluateConfigurationApplySource(ctx, nc, config, status, source)
}

func (r *configReconciler) evaluateConfigurationApplySource(ctx context.Context, nc *ciskov1.NetworkController, config *configapi.NetworkControllerConfig, status *configapi.NetworkControllerConfigStatus, source *configSourceSnapshot) (*configSourceSnapshot, bool, string, string) {
	var err error
	if tagOwnerCustodyIdentity(nc, config) && (!tagDeletionShape(config) || !tagPruneSource(source.raw, config.Spec.ManagedSections) || tagOwnerCustodyValid(nc, config) != nil) {
		return source, true, "TagCustodyScopeChanged", "Current tag custody cannot silently change resource family, ownership sections or retained history"
	}
	if slices.Contains(config.Finalizers, tagConfigFinalizer) && (!tagDeletionShape(config) || !tagPruneSource(source.raw, config.Spec.ManagedSections)) {
		return source, false, "TagCleanupSourceUnsupported", "Enrolled tag lifecycle requires metadata-only tag source"
	}
	if handled, err := r.a.handoffTagOwnership(ctx, nc, config, source); handled {
		status.Phase = configapi.NetworkControllerConfigPhaseWaiting
		if err != nil {
			return source, true, "TagOwnershipHandoffPending", "Prior tag ownership requires durable closure, absent old CR, unchanged complete history and repeated native absence"
		}
		return source, true, "TagOwnershipHandedOff", "Tag ownership transferred without native dispatch; current intent will use the normal journal on a later reconcile"
	}
	if handled, err := r.a.handoffDiscoveryOwnership(ctx, nc, config, source); handled {
		status.Phase = configapi.NetworkControllerConfigPhaseWaiting
		if err != nil {
			return source, true, "DiscoveryOwnershipHandoffPending", "Prior discovery ownership requires a completed attestation, absent original config, unchanged receipts and repeated native absence"
		}
		return source, true, "DiscoveryOwnershipHandedOff", "Completed discovery ownership transferred without altering retained audit receipts; normal planning follows on a later reconcile"
	}
	if handled, recoveryErr := r.recoverAcknowledgedFabricGroup(ctx, nc, config, source); handled {
		status.Phase = configapi.NetworkControllerConfigPhaseWaiting
		status.SectionStatus = []configapi.NetworkControllerSectionStatus{{Name: nc.Status.Configuration.Operation.Section, State: configapi.NetworkControllerSectionStateApplying}}
		if errors.Is(recoveryErr, errFabricDevicesBulkEvidencePending) {
			message := "Bulk fabric-device controller evidence is retained; fresh manager-bound read-only results are pending for every incoming, peer and parent device"
			status.SectionStatus[0].Message = message
			return source, true, "GroupDeviceEvidencePending", message
		}
		if errors.Is(recoveryErr, errFabricDevicesConvergenceUnavailable) {
			message := "Bulk fabric-device task, original memberships and worker evidence remain fenced; semantic device convergence is unavailable, so the journal and all member reservations remain retained"
			status.SectionStatus[0].Message = message
			return source, true, "GroupConvergenceVerificationUnavailable", message
		}
		if errors.Is(recoveryErr, errWiredBulkDeviceEvidencePending) {
			message := "Bulk provisioning controller evidence is retained; fresh manager-bound read-only network results are pending for every admitted device"
			status.SectionStatus[0].Message = message
			return source, true, "GroupDeviceEvidencePending", message
		}
		if errors.Is(recoveryErr, errWiredBulkConvergenceUnavailable) {
			message := "Bulk provisioning task and member records remain retained; supported declared network settings may be observed through workers, but complete provisioning convergence is unavailable, so the journal and reservations remain fenced"
			status.SectionStatus[0].Message = message
			return source, true, "GroupConvergenceVerificationUnavailable", message
		}
		if errors.Is(recoveryErr, errFabricEventConvergenceUnavailable) {
			message := "Fabric task and event checks completed; device convergence verification is not implemented, so the journal and reservations remain retained"
			status.SectionStatus[0].Message = message
			return source, true, "GroupConvergenceVerificationUnavailable", message
		}
		if errors.Is(recoveryErr, errSDATransitConvergenceUnavailable) {
			message := "SDA transit controller evidence and fresh worker-bound reads are verified; semantic transit convergence is unavailable, so the journal and all control-plane reservations remain retained"
			status.SectionStatus[0].Message = message
			return source, true, "GroupConvergenceVerificationUnavailable", message
		}
		if errors.Is(recoveryErr, errSDATransitDeviceEvidencePending) {
			message := "SDA transit controller evidence is retained; fresh manager-bound read-only results are pending for every admitted control-plane device"
			status.SectionStatus[0].Message = message
			return source, true, "GroupDeviceEvidencePending", message
		}
		if recoveryErr != nil {
			var failure *configurationTaskFailure
			if errors.As(recoveryErr, &failure) {
				status.Phase = configapi.NetworkControllerConfigPhaseFailed
				status.SectionStatus[0].Message = failure.Error()
				return source, true, "ControllerTaskFailed", failure.Error()
			}
			return source, true, "GroupVerificationPending", "Acknowledged group task remains fenced until source, actor, member and native evidence are verified"
		}
		return source, true, "ApplyProgressing", "Retained group operation advanced through its authorized lifecycle"
	}
	// Explicit bulk selection cannot silently fall through to single-device
	// execution. Retained acknowledged work above remains recoverable even if
	// these module options are changed or removed after native acceptance.
	bulk, _, bulkErr := wiredBulkOptions(config)
	if bulkErr != nil || bulk {
		status.Phase = configapi.NetworkControllerConfigPhaseFailed
		status.SectionStatus = []configapi.NetworkControllerSectionStatus{{Name: "inventory", State: configapi.NetworkControllerSectionStateApplyError}}
		if bulkErr != nil {
			return source, false, "InvalidBulkOptions", "Bulk provisioning options require an inventory-only manifest, an explicit boolean and valid grouping paths"
		}
		return source, false, "BulkExecutionUnavailable", "Bulk provisioning is explicitly selected; public all-member preparation, device convergence and settlement are not yet available, so single-device execution is refused"
	}
	fabricBulk, fabricBulkErr := fabricDevicesBulkEnabled(config)
	if fabricBulk || fabricBulkErr != nil {
		status.Phase = configapi.NetworkControllerConfigPhaseWaiting
		status.SectionStatus = []configapi.NetworkControllerSectionStatus{{Name: "fabric", State: configapi.NetworkControllerSectionStateApplying}}
		if fabricBulkErr != nil {
			status.Phase = configapi.NetworkControllerConfigPhaseFailed
			status.SectionStatus[0].State = configapi.NetworkControllerSectionStateApplyError
			return source, false, "InvalidBulkOptions", "Bulk fabric-device selection requires an explicit boolean, retained fabric-only scope and no unresolved secrets"
		}
		wants, err := parseFabricDeviceIntents(source.raw)
		var batches []fabricDevicesBulkBatch
		if err == nil {
			batches, err = groupFabricDevicesBulk(wants)
		}
		if err != nil {
			status.Phase = configapi.NetworkControllerConfigPhaseFailed
			status.SectionStatus[0].State = configapi.NetworkControllerSectionStateApplyError
			return source, false, "UnsupportedIntent", "Bulk fabric-device intent contains unsupported or invalid fields; no declaration is discarded"
		}
		prepared, err := r.prepareConfiguredFabricDevicesBulk(ctx, config, source, batches)
		if err != nil {
			return source, true, "GroupPreparationPending", "Bulk fabric-device preparation requires the complete supported source, installed group runtime and unchanged native/managed prerequisites"
		}
		if prepared {
			return source, true, "GroupPreparationPending", "Bulk fabric-device journal prepared; public dispatch awaits independent device convergence and settlement support"
		}
		return source, true, "GroupConvergenceVerificationUnavailable", "Bulk fabric-device memberships match native state; independent source-bound device convergence remains unavailable"
	}
	if pnpRegistrationStageSelected(nc, config, source) {
		valid, reason, message := r.evaluatePnPRegistrationStage(ctx, config, source, status)
		return source, valid, reason, message
	}
	if handled, converged, prepareErr := r.prepareConfigurationFabricGroup(ctx, nc, config, source); handled {
		status.Phase = configapi.NetworkControllerConfigPhaseWaiting
		status.SectionStatus = []configapi.NetworkControllerSectionStatus{{Name: config.Spec.ManagedSections[0], State: configapi.NetworkControllerSectionStateApplying}}
		if errors.Is(prepareErr, errFabricEWLCDisableUnavailable) {
			status.Phase = configapi.NetworkControllerConfigPhaseFailed
			status.SectionStatus[0].State = configapi.NetworkControllerSectionStateApplyError
			return source, false, "UnsupportedIntent", "Embedded wireless disablement requires reload verification that is not yet implemented"
		}
		if prepareErr != nil {
			return source, true, "GroupPreparationPending", "Group action preparation requires unchanged intent, ownership and converged prerequisites"
		}
		if converged {
			status.Phase = configapi.NetworkControllerConfigPhaseInSync
			status.SectionStatus[0].State = configapi.NetworkControllerSectionStateInSync
			return source, true, "InSync", "Declared group actions match native state"
		}
		return source, true, "GroupPreparationPending", "Group journal prepared; waiting for all-member device reservation admission"
	}
	// Resource-specific group compilers validate their own bounded operation
	// models above, including existing-only settings. Full inventory provisioning
	// preconditions must not be imposed on those retained/delta operations.
	if err := validatePinnedModelPreconditions(source.raw); err != nil {
		status.Phase = configapi.NetworkControllerConfigPhaseFailed
		return source, true, "ModelPreconditionFailed", "Controller model violates pinned inventory or access point preconditions"
	}
	if config.Spec.PrunePolicy == configapi.NetworkControllerRetentionPolicyDelete && (len(config.Spec.ManagedSections) == 1 && config.Spec.ManagedSections[0] == "templates" || combinedTagSections(config.Spec.ManagedSections)) && !tagPruneSource(source.raw, config.Spec.ManagedSections) {
		return source, false, "UnsupportedIntent", "Tag pruning requires a metadata-only tag source"
	}
	var plan configurationApplyPlan
	ownedPlan, ownedHandled, ownedErr := r.a.compileOwnedDiscoveryApply(nc, config, source)
	if ownedHandled {
		plan, err = ownedPlan, ownedErr
	} else if templateComparisonConfig(config) {
		plan, err = r.a.compilePrivateTemplateInventoryApply(nc, config, source)
	} else if templateNativeComparisonConfig(config) {
		plan, err = r.a.compilePrivateTemplateDefinitions(nc, config, source)
	} else if siteComparisonConfig(config) {
		plan, err = r.a.compileSiteImageApply(nc, config, source)
	} else if networkComparisonConfig(config) {
		plan, err = r.a.compileNetworkComparisonApply(nc, config, source)
	} else if wirelessTagComparisonConfig(config) {
		plan, err = r.a.compileWirelessTagComparison(nc, config, source)
	} else if discoveryCredentialPlanSelected(nc, config, source) {
		plan, err = r.a.compileDiscoveryCredentialApply(nc, config, source)
	} else if len(config.Spec.SecretRefs) > 0 {
		plan, err = r.a.compileCredentialConfigurationApply(nc, config, source)
	} else if len(config.Spec.ManagedSections) == 1 && config.Spec.ManagedSections[0] == "network_settings" {
		plan, err = r.a.compileNetworkSettingsApply(nc, config, source)
	} else if r.a.configurationAdmissionReady() && len(config.Spec.ManagedSections) == 1 && config.Spec.ManagedSections[0] == "inventory" {
		plan, err = r.a.compileOwnedInventoryApply(nc, config, source)
	} else if len(config.Spec.ManagedSections) == 1 && config.Spec.ManagedSections[0] == "sites" {
		plan, err = r.a.compileSiteImageApply(nc, config, source)
	} else {
		plan, err = r.a.client.compileConfigurationApply(source.raw, config.Spec.ManagedSections, source.templateFiles)
	}
	if err != nil {
		return source, false, "UnsupportedIntent", "Configuration intent contains invalid, unresolved or unimplemented execution fields"
	}
	if err := r.a.bindNetworkSettingApplications(nc, config, source, &plan); err != nil {
		return source, false, "NetworkSettingOwnershipUnavailable", "Retained network setting applications do not match their owner, journal or complete receipt cohort"
	}
	r.a.bindNetworkSettingTaskRecovery(config, source, &plan)
	if err := r.a.bindProfileSiteApplications(nc, config, source, &plan); err != nil {
		return source, false, "ProfileSiteOwnershipUnavailable", "Profile/site application evidence does not match its retained owner, identities or journal"
	}
	if err := r.a.bindWirelessTagReceipts(nc, config, source, &plan); err != nil {
		return source, false, "WirelessTagEvidenceUnavailable", "Private tag evidence or its projected comparison key is unavailable, changed or inconsistent"
	}
	if err := r.a.bindSwitchingCreatorOwnership(nc, config, source, &plan); err != nil {
		return source, false, "SwitchingCreatorEvidenceUnavailable", "Switching creator receipts are missing, malformed or conflict with their retained owner"
	}
	r.a.bindNetworkProfileTaskRecovery(config, source, &plan)
	if err := r.a.bindProfileSitePrune(nc, config, source, &plan); err != nil {
		return source, false, "ProfileSitePruneUnavailable", "Association pruning requires valid retained applications, one network_profiles section and an unchanged owner"
	}
	if err := r.a.bindFeatureCreateOwnership(nc, config, source, &plan); err != nil {
		return source, false, "FeatureOwnershipUnavailable", "Feature ownership does not match the compiled scope or retained state"
	}
	if err := r.a.bindFeaturePrune(nc, config, source, &plan); err != nil {
		return source, false, "FeaturePruneUnavailable", "Feature pruning requires a section-only source and retained native-state ownership evidence"
	}
	if err := r.a.bindDiscoveryOwnership(nc, config, source, &plan); err != nil {
		return source, false, "DiscoveryOwnershipUnavailable", "Discovery ownership does not match the compiled scope or retained state"
	}
	if err := r.a.bindIPAMOwnership(nc, config, source, &plan); err != nil {
		return source, false, "IPAMOwnershipUnavailable", "IPAM receipt binding is invalid or conflicts with retained state"
	}
	if err := r.a.bindTemplateOwnership(nc, config, source, &plan); err != nil {
		return source, false, "TemplateOwnershipUnavailable", "Template creator ownership does not match the compiled scope or retained state"
	}
	if err := r.a.bindAnycastBulk(config, source, plan.executor); err != nil {
		return source, false, "InvalidAnycastBulkOptions", "Bulk anycast selection requires a supported retained fabric gateway manifest and valid boolean"
	}
	r.a.client.bindAnycastBulkTaskRecovery(&plan)
	r.a.bindTemplateSettling(plan.executor, config, source, "")
	// Metadata-only intent must also observe retained credential ownership.
	if wireless, ok := plan.executor.(*wirelessConfiguration); ok {
		if wireless.ap != nil {
			wireless.ap.owner = string(config.UID)
			if nc.Status.Configuration != nil {
				wireless.ap.receipts = nc.Status.Configuration.DeepCopy().CredentialApplications
			}
		}
		if wireless.ssid != nil {
			wireless.ssid.owner = string(config.UID)
			if nc.Status.Configuration != nil {
				wireless.ssid.receipts = nc.Status.Configuration.DeepCopy().CredentialApplications
			}
		}
	}
	if err := r.a.bindTagOwnership(nc, config, source, &plan); err != nil {
		return source, false, "TagOwnershipUnavailable", "Tag ownership does not match retained creator and latest-state evidence"
	}
	executor, section := plan.executor, plan.section
	defer func() {
		if len(status.SectionStatus) == 0 {
			state := configapi.NetworkControllerSectionStateApplying
			if status.Phase == configapi.NetworkControllerConfigPhaseFailed {
				state = configapi.NetworkControllerSectionStateApplyError
			}
			if status.Phase == configapi.NetworkControllerConfigPhaseLeaseBlocked {
				state = configapi.NetworkControllerSectionStatePending
			}
			status.SectionStatus = plan.statuses(state)
		}
	}()
	operationCtx, cancel := context.WithTimeout(ctx, 90*time.Second)
	defer cancel()
	if err = r.a.claimConfigurationSection(operationCtx, config, source); err != nil {
		status.Phase = configapi.NetworkControllerConfigPhaseLeaseBlocked
		return source, true, "OwnershipUnavailable", "Configuration ownership or source identity could not be confirmed"
	}
	// Refresh after the ownership CAS, and never use an old journal snapshot.
	nc, err = r.a.currentController(operationCtx)
	if err != nil {
		return source, true, "ControllerChanged", "Controller identity or policy changed"
	}
	if nc.Status.Configuration != nil && nc.Status.Configuration.Operation != nil {
		op := nc.Status.Configuration.Operation
		if op.PhysicalGroup != nil || op.CancellationRequestedAt != nil || isReservedConfigurationGroupResource(op.Section, op.Resource) {
			status.Phase = configapi.NetworkControllerConfigPhaseWaiting
			if op.PhysicalGroup != nil && op.Phase == "Cancelling" && op.ConfigUID == string(config.UID) && op.ConfigName == config.Name && r.a.configurationAdmissionReady() {
				if err := r.a.finishConfiguration(operationCtx, op.ID); err != nil {
					return source, true, "GroupCancellationPending", "Cancelled group journal retained until all members settle and cleanup is authorized"
				}
				return source, true, "ApplyProgressing", "Cancelled group settled; cancellation receipt retained"
			}
			if op.PhysicalGroup != nil && op.CancellationRequestedAt == nil && op.Phase == "Verified" && op.ConfigUID == string(config.UID) && op.ConfigName == config.Name && r.a.configurationAdmissionReady() {
				if err := r.a.completeConfigurationGroup(operationCtx, nc); err != nil {
					return source, true, "GroupSettlementPending", "Verified group evidence retained while authorized completion, device release and manager settlement finish"
				}
				return source, true, "ApplyProgressing", "Verified group operation and all reservations settled"
			}
			return source, true, "GroupReservationRecoveryRequired", "Multi-device operation requires verified group recovery evidence and authorization; the journal remains retained"
		}
		status.Phase = configapi.NetworkControllerConfigPhaseWaiting
		if op.ConfigUID != string(config.UID) || op.ConfigName != config.Name {
			return source, true, "OperationRecoveryRequired", "An outstanding operation belongs to another owner or section"
		}
		if op.TargetSerial != "" {
			if op.Phase == "Prepared" && r.a.configurationAdmission != nil {
				reservation, err := r.a.configurationReservation(operationCtx, nc)
				if err != nil {
					return source, true, "DevicePreparationPending", "Waiting for the exact device reservation"
				}
				grant := reservation.Status.Grant
				if r.a.configurationAdmissionReady() && grant != nil && grant.Phase == "Held" && reservation.Status.Claim == nil && reservation.Status.Completion == nil && contract.ValidateGrantFreshness(reservation, time.Now()) == nil {
					mutation, err := executor.next(operationCtx)
					if err != nil || mutation == nil {
						return source, true, "DevicePreparationPending", "Prepared device action could not be replanned from current controller state"
					}
					if err := r.a.resumePhysicalConfigurationWrite(operationCtx, config, source, op, section, mutation); err != nil {
						return source, true, "SubmissionRecoveryRequired", "Device operation was not confirmed accepted; retained journal determines recovery"
					}
					return source, true, "TaskAccepted", "Catalyst Center accepted the reserved device operation"
				}
				// Expired/revoked preparation cannot be reused. Clear only after
				// the unclaimed reservation was released and manager-settled;
				// the next reconciliation replans with a new operation identity.
				if err := r.a.finishConfiguration(operationCtx, op.ID); err != nil {
					return source, true, "DevicePreparationPending", "Unsubmitted device operation retained while reservation preparation or settlement is outstanding"
				}
				return source, true, "ApplyProgressing", "Unsubmitted device reservation settled; current intent will be replanned"
			}
			if op.Phase == "Verified" && r.a.configurationAdmission != nil {
				if err := r.a.completeConfigurationReservation(operationCtx, nc); err != nil {
					return source, true, "DeviceSettlementPending", "Verified action evidence retained while device reservation and manager settlement complete"
				}
				return source, true, "ApplyProgressing", "Verified device operation and reservation settled"
			}
			if (op.Phase != "Accepted" && op.Phase != "Verifying") || !r.a.configurationAdmissionReady() {
				return source, true, "DeviceAdmissionRecoveryRequired", "Physical-device operation requires a durable managed reservation; journal evidence is retained"
			}
			if err := r.a.validatePhysicalConfigurationRecovery(operationCtx, nc); err != nil {
				return source, true, "DeviceAdmissionRecoveryRequired", "Accepted device operation does not match its retained claim and worker identity"
			}
		}
		// An unsubmitted or already verified operation cannot produce further
		// remote effects. A source revision change must not strand these safe
		// terminal boundaries; uncertain and accepted work keeps its old binding.
		if op.Phase == "Prepared" && op.PreparedAt != nil && op.TargetSerial == "" && op.Section == section && op.ConfigGeneration == config.Generation && op.ControllerGeneration == nc.Generation && op.SourceUID == string(source.uid) && op.SourceVersion == source.version {
			mutation, err := executor.next(operationCtx)
			if err != nil {
				return source, true, "DependencySettlingPending", "Prepared operation retained while dependency settling and exact replanning are pending"
			}
			if mutation == nil || !mutation.identity.preparationWait || mutation.resource != op.Resource || mutation.action != op.Action || mutation.identity.target != op.TargetID || mutation.identity.parent != op.ParentID {
				// Nothing was submitted at this boundary. If native dependency
				// drift requires a different action, retire only this unsubmitted
				// preparation so the next pass can correct it and start a fresh wait.
				if err := r.a.finishConfiguration(operationCtx, op.ID); err != nil {
					return source, true, "JournalUnavailable", "Cannot retire changed dependency preparation"
				}
				return source, true, "ApplyProgressing", "Unsubmitted dependency preparation retired; current native state will be replanned"
			}
			if err := r.a.submitPreparedConfigurationReceipt(operationCtx, config, source, op, section, mutation.resource, mutation.action, configurationTaskWriteReceipt(section, mutation), nil, mutation.identity); err != nil {
				if errors.Is(err, errConfigurationNotDispatched) {
					status.Phase = configapi.NetworkControllerConfigPhaseWaiting
					return source, true, "NativePreflightRefused", "Native preflight proved no configuration mutation was sent; the unsubmitted operation was retired and current intent will be replanned"
				}
				return source, true, "SubmissionRecoveryRequired", "Submission or journal outcome requires inspection; no automatic replay"
			}
			return source, true, "TaskAccepted", "Catalyst Center accepted the operation after dependency settling"
		}
		if op.Phase == "Prepared" || op.Phase == "Verified" {
			if err = r.a.finishConfiguration(operationCtx, op.ID); err != nil {
				if errors.Is(err, errGlobalAuthBinding) {
					return source, true, "OperationRecoveryRequired", "Global authentication completion lacks its original result binding or task/UUID evidence; the journal remains retained"
				}
				if errors.Is(err, errSwitchingProfileBinding) {
					return source, true, "OperationRecoveryRequired", "Switching profile lacks its original durable template project binding; the journal remains fenced"
				}
				if errors.Is(err, errTemplateMembershipProjectBinding) {
					return source, true, "OperationRecoveryRequired", "Template association lacks its original durable project binding; the operation remains fenced for evidence-based recovery"
				}
				return source, true, "JournalUnavailable", "Cannot release unsubmitted or verified operation"
			}
			return source, true, "ApplyProgressing", "Operation boundary released; current intent will be replanned"
		}
		if op.Section != section || !configurationOperationGenerationMatches(config, source, op) || op.ControllerGeneration != nc.Generation || op.SourceUID != string(source.uid) || op.SourceVersion != source.version {
			return source, true, "OperationRecoveryRequired", "An outstanding operation is bound to another input revision or owner"
		}
		switch op.Phase {
		case "Accepted", "Unknown":
			// A resource-qualified retained task can be observed after uncertainty
			// through the existing Unknown -> Verifying transition. This grants no
			// submission permit; missing-task and other executor paths remain fenced.
			if op.Phase == "Unknown" {
				if !plan.canObserveUnknownTask(op) {
					return source, true, "OperationRecoveryRequired", "Submission outcome is uncertain and lacks a supported retained task; automatic replay is prohibited"
				}
				if err := validateSSIDCredentialHistory(op.Section, op.Resource, op.CredentialVersions, op.SSIDCredentialHistory); err != nil {
					return source, true, "OperationRecoveryRequired", "SSID credential history is missing or changed; the operation remains fenced"
				}
				if err := validateConfigurationCredentialVersions(source, op.Section, op.CredentialVersions); err != nil {
					return source, true, "OperationRecoveryRequired", "Credential source bindings are missing or changed; the operation remains fenced"
				}
			}
			if op.SubmittedAt == nil {
				return source, true, "OperationRecoveryRequired", "Submission time is unavailable"
			}
			timeout := 30 * time.Minute
			if config.Spec.TaskTimeout != nil {
				timeout = config.Spec.TaskTimeout.Duration
			}
			if op.Phase == "Accepted" && (timeout <= 0 || time.Since(op.SubmittedAt.Time) > timeout) {
				if err := r.a.advanceConfiguration(operationCtx, op.ID, "Unknown", ""); err != nil {
					return source, true, "JournalUnavailable", "Cannot retain timed-out task"
				}
				status.Phase = configapi.NetworkControllerConfigPhaseFailed
				return source, true, "TaskTimeout", "Task exceeded its configured timeout; operation remains fenced for recovery"
			}
			done, err := plan.taskComplete(operationCtx, r.a.client, op, time.Now())
			if err != nil {
				var failure *configurationTaskFailure
				if errors.As(err, &failure) {
					status.Phase = configapi.NetworkControllerConfigPhaseFailed
					return source, true, "ControllerTaskFailed", failure.Error()
				}
				return source, true, "TaskObservationFailed", "Task could not be correlated or completed successfully; operation remains fenced"
			}
			if !done {
				return source, true, "TaskRunning", "Catalyst Center configuration task is running"
			}
			if err = r.a.advanceConfiguration(operationCtx, op.ID, "Verifying", ""); err != nil {
				return source, true, "JournalUnavailable", "Cannot record completed task"
			}
		case "Verifying":
			status.Phase = configapi.NetworkControllerConfigPhaseVerifying
			if isMetadataCredentialOperation(op.Section, op.Resource) {
				if err := r.a.recordMetadataCredentialApplication(operationCtx, config, source, op.ID); err != nil {
					return source, true, "VerificationPending", "Metadata-only credential task, UUID and ownership receipt could not be verified"
				}
				return source, true, "ApplyProgressing", "Metadata-only credential ownership receipt retained"
			}
			if isFloorImageOperation(op.Section, op.Resource) {
				if err := r.a.recordFloorImageApplication(operationCtx, config, source, op.ID); err != nil {
					return source, true, "VerificationPending", "Image upload completion and floor identity have not produced a retained receipt"
				}
				return source, true, "ApplyProgressing", "Image upload completion receipt retained"
			}
			if strings.HasPrefix(op.Resource, wirelessProvisionRecordPrefix) {
				name, err := wirelessProvisionRecoveryName(source.raw, op)
				if err != nil {
					return source, true, "OperationRecoveryRequired", "Wireless provisioning baseline does not bind the current device intent"
				}
				if err := r.a.recordWirelessProvisionApplication(operationCtx, config, source, op.ID, name); err != nil {
					return source, true, "VerificationPending", "Wireless task and provisioning state have not produced a retained verified receipt"
				}
				return source, true, "ApplyProgressing", "Wireless provisioning receipt retained with its original baseline"
			}
			if credentials, ok := executor.(interface {
				verifyCredential(context.Context, *ciskov1.NetworkControllerConfigurationOperation) (string, bool, error)
				credentialOperation(*ciskov1.NetworkControllerConfigurationOperation) bool
			}); ok && credentials.credentialOperation(op) {
				id, verified, err := credentials.verifyCredential(operationCtx, op)
				if err != nil || !verified {
					return source, true, "VerificationPending", "Credential operation identity and metadata could not be verified"
				}
				if err := r.a.recordConfigurationCredentialApplication(operationCtx, config, source, op.ID, id); err != nil {
					return source, true, "JournalUnavailable", "Cannot record applied credential version and verified resource identity"
				}
				return source, true, "ApplyProgressing", "Credential version application recorded; no additional request was submitted"
			}
			if plan.recordCompletion != nil && (plan.receiptOperation == nil || plan.receiptOperation(op.DeepCopy())) {
				if err := plan.completeWithReceipt(operationCtx, r.a, op); err != nil {
					return source, true, "VerificationPending", "Authoritative configuration and its retained completion receipt could not be verified"
				}
				return source, true, "ApplyProgressing", "Verified configuration and native completion receipt retained"
			}
			verified, err := executor.verify(operationCtx, op)
			if err != nil || !verified {
				if errors.Is(err, errGlobalAuthBinding) {
					return source, true, "OperationRecoveryRequired", "Global authentication result binding is missing or changed; native preservation and identity require evidence-based recovery"
				}
				if errors.Is(err, errSwitchingProfileBinding) {
					return source, true, "OperationRecoveryRequired", "Switching profile template project identity is missing or changed; name matching cannot recover the original binding"
				}
				if errors.Is(err, errTemplateMembershipProjectBinding) {
					return source, true, "OperationRecoveryRequired", "Template association's original project binding is missing or changed; name matching cannot recover that evidence"
				}
				return source, true, "VerificationPending", "Authoritative configuration identity and attributes do not yet match"
			}
			if err = r.a.advanceConfiguration(operationCtx, op.ID, "Verified", ""); err != nil {
				return source, true, "JournalUnavailable", "Cannot record verified configuration"
			}
		default:
			return source, true, "OperationRecoveryRequired", "Submission outcome is uncertain; automatic replay is prohibited"
		}
		return source, true, "ApplyProgressing", "Configuration operation advanced; no additional request was submitted"
	}
	mutation, err := executor.next(operationCtx)
	if errors.Is(err, errConfigurationReplan) {
		status.Phase = configapi.NetworkControllerConfigPhaseWaiting
		return source, true, "ApplyProgressing", "Owned bookkeeping advanced; the next reconciliation will replan current intent"
	}
	if errors.Is(err, errWirelessProfileSwitchingModes) {
		status.Phase = configapi.NetworkControllerConfigPhaseFailed
		status.SectionStatus = plan.statuses(configapi.NetworkControllerSectionStateApplyError)
		return source, false, "MixedWirelessSwitchingModes", "A wireless profile cannot combine Fabric and FlexConnect SSIDs, including retained settings. Explicitly choose a compatible switching mode; no configuration request was submitted"
	}
	if errors.Is(err, errWirelessPolicyCohortRequired) {
		status.Phase = configapi.NetworkControllerConfigPhaseFailed
		status.SectionStatus = plan.statuses(configapi.NetworkControllerSectionStateApplyError)
		return source, false, "PolicyTagCohortRequired", "Mixed existing/new policy tags require a preserving migration workflow: the appliance rejects bulk creation with existing names and rejects unassigned policy definitions. Existing or partial cohorts were retained; no merge or replacement was submitted"
	}
	if errors.Is(err, errWirelessAPZoneCombination) {
		status.Phase = configapi.NetworkControllerConfigPhaseFailed
		status.SectionStatus = plan.statuses(configapi.NetworkControllerSectionStateApplyError)
		return source, false, "DuplicateAPZoneCombination", "Each SSID and RF profile pair must be unique across AP zones. No configuration request was submitted"
	}
	if errors.Is(err, errWirelessTagScopeConflict) {
		status.Phase = configapi.NetworkControllerConfigPhaseFailed
		status.SectionStatus = plan.statuses(configapi.NetworkControllerSectionStateApplyError)
		return source, false, "ConflictingWirelessTags", "Wireless tags overlap a site or policy-zone scope; use distinct sites or disjoint policy zones. Existing definitions were retained and no configuration request was submitted"
	}
	if errors.Is(err, errWirelessSiteTagAPProfile) {
		status.Phase = configapi.NetworkControllerConfigPhaseFailed
		status.SectionStatus = plan.statuses(configapi.NetworkControllerSectionStateApplyError)
		return source, false, "MissingSiteTagAPProfile", "Wireless site tag requires ap_profile_name referencing an existing AP profile, or a retained observed reference. No configuration request was submitted"
	}
	if errors.Is(err, errWirelessAPImmutable) {
		status.Phase = configapi.NetworkControllerConfigPhaseFailed
		status.SectionStatus = plan.statuses(configapi.NetworkControllerSectionStateApplyError)
		return source, false, "ImmutableAPProfileField", "AP remote-worker mode cannot change in place; use a different profile name. Existing definitions were retained"
	}
	if plan.section == "sites" && config.Spec.PrunePolicy == configapi.NetworkControllerRetentionPolicyDelete && (errors.Is(err, errSiteRetirementReferenced) || errors.Is(err, errConfigurationOwned) || errors.Is(err, errProfileSitePruneReferenced)) {
		status.Phase = configapi.NetworkControllerConfigPhaseWaiting
		return source, true, "SiteRetirementBlocked", "Site lifecycle requires intact private ownership, absent consumers and complete native observations; no mutation was submitted for this plan"
	}
	if errors.Is(err, errProfileSitePruneReferenced) {
		status.Phase = configapi.NetworkControllerConfigPhaseWaiting
		return source, true, "ProfileSitePruneReferenced", "Association is referenced by a tag, assigned device or onboarding record, or complete reference evidence is unavailable; no removal was submitted"
	}
	if config.Spec.PrunePolicy == configapi.NetworkControllerRetentionPolicyDelete && tagPruneSource(source.raw, config.Spec.ManagedSections) && (errors.Is(err, errTagPruneReferenced) || errors.Is(err, errConfigurationOwned)) {
		status.Phase = configapi.NetworkControllerConfigPhaseWaiting
		return source, true, "TagPruneBlocked", "Tag pruning requires original ownership, unchanged complete native state and absent consumers; no mutation was submitted"
	}
	if errors.Is(err, errFeaturePruneReferenced) {
		status.Phase = configapi.NetworkControllerConfigPhaseWaiting
		status.SectionStatus = plan.statuses(configapi.NetworkControllerSectionStateApplying)
		return source, true, "FeaturePruneReferenced", "A feature definition is referenced by a wireless profile, or complete profile reference metadata is unavailable. Clear the reference or retain the definition before pruning"
	}
	if errors.Is(err, errConfigurationOwned) && config.Spec.PrunePolicy == configapi.NetworkControllerRetentionPolicyDelete && plan.section == "feature_templates" {
		status.Phase = configapi.NetworkControllerConfigPhaseWaiting
		return source, true, "FeaturePruneOwnershipRequired", "Pruning requires retained creation and complete native-state receipts with unchanged UUID/name/attributes. Missing, replaced or externally edited definitions require recovery; no Delete was submitted for this plan"
	}
	if errors.Is(err, errIPAMPruneReferenced) {
		status.Phase = configapi.NetworkControllerConfigPhaseWaiting
		status.SectionStatus = plan.statuses(configapi.NetworkControllerSectionStateApplying)
		return source, true, "IPAMPruneReferenced", "An allocation has a gateway, LAN-session or unowned child reservation reference, or complete reference metadata is unavailable. Retain it or clear the dependency before pruning"
	}
	if errors.Is(err, errConfigurationOwned) && config.Spec.PrunePolicy == configapi.NetworkControllerRetentionPolicyDelete && plan.section == "network_settings" {
		status.Phase = configapi.NetworkControllerConfigPhaseWaiting
		return source, true, "IPAMPruneOwnershipRequired", "Pruning requires an unambiguous creation receipt and unchanged native UUID/site identity. Missing or replaced resources require recovery; no delete was submitted for this plan"
	}
	if err != nil {
		return source, true, "ObservationOrPlanFailed", "Selected configuration could not be fully observed and planned"
	}

	now := metav1.Now()
	status.LastControllerCheck = &now
	status.ControllerAPIVersion = "v1/v2"
	if mutation == nil {
		if plan.finalize != nil {
			if err := plan.finalize(operationCtx); err != nil {
				status.Phase = configapi.NetworkControllerConfigPhaseWaiting
				return source, true, "ApplyFinalizationPending", "Native effects are verified; retained ownership evidence is awaiting safe retirement"
			}
		}
		status.Phase = configapi.NetworkControllerConfigPhaseInSync
		status.LastAppliedTime = &now
		status.LastAppliedSourceRevision = config.Spec.ModelSource.SourceRevision
		status.SectionStatus = plan.statuses(configapi.NetworkControllerSectionStateInSync)
		if sites, ok := executor.(*sitePlannedAPExecutor); ok && sites.images != nil && sites.images.count() > 0 {
			message := "Owned geometry matches and declared floor uploads have retained completion receipts; remote image contents remain unverified"
			status.SectionStatus[0].Message = message
			return source, true, "ApplyVerified", message
		}
		return source, true, "ApplyVerified", "Owned configuration attributes match authoritative controller state"
	}
	if mutation.identity.serial != "" {
		status.Phase = configapi.NetworkControllerConfigPhaseLeaseBlocked
		if _, err := r.a.physicalConfigurationAdmission(mutation); err != nil {
			return source, true, "DeviceAdmissionUnavailable", "Physical-device configuration requires a durable managed reservation"
		}
		if _, err := r.a.prepareConfiguration(operationCtx, config, source, section, mutation.resource, mutation.action, mutation.identity); err != nil {
			return source, true, "JournalUnavailable", "Cannot retain device preparation intent"
		}
		status.Phase = configapi.NetworkControllerConfigPhaseWaiting
		return source, true, "DevicePreparationPending", "Device operation prepared; waiting for its managed reservation"
	}
	if mutation.identity.preparationWait {
		if _, err := r.a.prepareConfiguration(operationCtx, config, source, section, mutation.resource, mutation.action, mutation.identity); err != nil {
			return source, true, "JournalUnavailable", "Cannot retain dependency settling preparation"
		}
		status.Phase = configapi.NetworkControllerConfigPhaseWaiting
		return source, true, "DependencySettlingPending", "Operation prepared; dependency settling is checked before submission"
	}
	status.Phase = configapi.NetworkControllerConfigPhaseApplying
	err = r.a.submitConfigurationWrite(operationCtx, config, source, section, mutation)
	if errors.Is(err, errConfigurationNotDispatched) {
		status.Phase = configapi.NetworkControllerConfigPhaseWaiting
		return source, true, "NativePreflightRefused", "Native preflight proved no configuration mutation was sent; the unsubmitted operation was retired and current intent will be replanned"
	}
	if err != nil {
		return source, true, "SubmissionRecoveryRequired", "Submission or journal outcome requires inspection; no automatic replay"
	}
	if mutation.submitCompleted != nil || mutation.submitCreated != nil {
		status.Phase = configapi.NetworkControllerConfigPhaseVerifying
		return source, true, "SynchronousResponseAccepted", "Controller response was recorded; independent configuration read-back remains pending"
	}
	status.Phase = configapi.NetworkControllerConfigPhaseWaiting
	return source, true, "TaskAccepted", "Catalyst Center accepted the configuration operation; task completion and read-back remain pending"
}
