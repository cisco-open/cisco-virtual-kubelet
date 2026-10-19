// Copyright 2026 Cisco Systems Inc.
// SPDX-License-Identifier: Apache-2.0
package catalystcenter

import (
	"context"
	"encoding/json"
	"errors"
	configapi "github.com/cisco/virtual-kubelet-cisco/api/config/v1alpha1"
	cvk "github.com/cisco/virtual-kubelet-cisco/api/v1alpha1"
	"github.com/cisco/virtual-kubelet-cisco/internal/controlleradapter"
	"k8s.io/apimachinery/pkg/api/meta"
	ctrl "sigs.k8s.io/controller-runtime"
	ctrlclient "sigs.k8s.io/controller-runtime/pkg/client"
	"slices"
	"time"
)

type metadataConfigDeletionSource struct {
	ObservationGeneration                int64
	ConfigUID, SpecDigest, DeletionToken string
	Cleanup                              bool
}

type metadataConfigCleanup struct {
	finalizer, reasonPrefix string
	shape                   func(*configapi.NetworkControllerConfig) bool
	validOperation          func(*cvk.NetworkControllerConfigurationOperation) bool
	finalizeSelected        func(string) bool
	cleanupSource           func(*configapi.NetworkControllerConfig) *configSourceSnapshot
	observationContext      func(*configSourceSnapshot) *metadataConfigDeletionSource
	bindObservation         func(*configapi.NetworkControllerConfig, *configSourceSnapshot)
	closed                  func(*cvk.NetworkController, *configapi.NetworkControllerConfig) bool
	retire                  func(context.Context, *adapter, *cvk.NetworkController, *configapi.NetworkControllerConfig) error
	absent                  func(context.Context, *adapter, *cvk.NetworkController, *configapi.NetworkControllerConfig) (bool, error)
}

func (r *configReconciler) reconcileMetadataConfigDeletion(ctx context.Context, c *configapi.NetworkControllerConfig, family metadataConfigCleanup) (ctrl.Result, error) {
	a := r.a
	nc, err := a.currentController(ctx)
	if err != nil {
		if errors.Is(err, errStaleController) {
			return ctrl.Result{RequeueAfter: 5 * time.Second}, nil
		}
		return ctrl.Result{}, err
	}
	status := *c.Status.DeepCopy()
	status.ObservedGeneration = c.Generation
	status.Phase = configapi.NetworkControllerConfigPhaseWaiting
	complete := false
	reason, message := family.reasonPrefix+"CleanupPending", "Metadata cleanup is awaiting verified ownership, references, tasks and native absence"
	if !family.shape(c) || controlleradapter.ValidateConfigContract(nc, c, a.intentSecretPath) != nil {
		reason, message = family.reasonPrefix+"CleanupUnsupported", "Deleting intent no longer matches the enrolled metadata lifecycle; finalizer retained"
	} else {
		var source *configSourceSnapshot
		var op *cvk.NetworkControllerConfigurationOperation
		if nc.Status.Configuration != nil {
			op = nc.Status.Configuration.Operation
		}
		if op != nil && op.ConfigUID == string(c.UID) {
			if op.ConfigName != c.Name || !configurationOperationID.MatchString(op.ID) {
				reason, message = family.reasonPrefix+"CleanupRecoveryRequired", "Retained operation identity does not match the config; finalizer retained"
			} else if !family.validOperation(op) {
				reason, message = family.reasonPrefix+"CleanupRecoveryRequired", "Outstanding operation is outside the enrolled metadata protocol; finalizer retained"
			} else if op.Phase == "Prepared" && (op.SubmittedAt != nil || op.TaskID != "") {
				reason, message = family.reasonPrefix+"CleanupRecoveryRequired", "Prepared operation has uncertain submission evidence; finalizer retained"
			} else if op.Phase == "Prepared" {
				err = a.finishConfiguration(ctx, op.ID)
				if err != nil {
					reason, message = family.reasonPrefix+"CleanupRecoveryRequired", "Unsubmitted metadata preparation could not be retired"
				} else {
					reason, message = family.reasonPrefix+"PreparationRetired", "Unsubmitted metadata preparation retired without a native request"
				}
			} else {
				if family.finalizeSelected(op.Resource) {
					source = family.cleanupSource(c)
				} else {
					source, err = loadControllerSource(ctx, a.statusReader, c)
					if err == nil && family.bindObservation != nil {
						family.bindObservation(c, source)
					}
				}
				if err != nil {
					reason, message = family.reasonPrefix+"CleanupSourceUnavailable", "Original source is unavailable for retained-task verification; finalizer retained"
				} else {
					if family.observationContext(source) != nil && !family.observationContext(source).Cleanup && op.ConfigGeneration+1 == c.Generation {
						family.observationContext(source).ObservationGeneration = op.ConfigGeneration
					}
					_, _, reason, message = r.evaluateConfigurationApplySource(ctx, nc, c, &status, source)
				}
			}
		} else if c.Spec.DeletionPolicy != configapi.NetworkControllerRetentionPolicyDelete {
			complete = true
			reason, message = family.reasonPrefix+"Retained", "Remote state and ownership history retained by policy"
		} else if family.closed(nc, c) {
			complete, err = family.absent(ctx, a, nc, c)
			if err != nil {
				complete = false
			}
			if !complete {
				reason, message = family.reasonPrefix+"CleanupObservationPending", "Owned deletion evidence requires a fresh complete native absence check"
			}
		} else if op != nil {
			reason, message = family.reasonPrefix+"CleanupBlocked", "Another controller operation owns the journal; finalizer retained"
		} else {
			source = family.cleanupSource(c)
			_, _, reason, message = r.evaluateConfigurationApplySource(ctx, nc, c, &status, source)
			if status.Phase == configapi.NetworkControllerConfigPhaseInSync {
				latest, readErr := a.currentController(ctx)
				if readErr != nil {
					return ctrl.Result{}, readErr
				}
				nc = latest
				complete = family.closed(nc, c)
				if complete {
					complete, err = family.absent(ctx, a, nc, c)
					if err != nil {
						complete = false
					}
				}
			}
		}
	}
	if complete && family.retire != nil {
		if err := family.retire(ctx, a, nc, c); err != nil {
			complete = false
			reason, message = family.reasonPrefix+"RetirementPending", "Cleanup cannot release its finalizer until ownership retirement is durably retained; no native request is replayed"
		}
	}
	// Publish only status. The trusted manager validates this bound proof and owns
	// the metadata patch; the isolated worker never gains finalizer permissions.
	if complete {
		reason = family.reasonPrefix + "Finalized"
		proof, _ := json.Marshal(featureConfigCompletionProof(nc, c))
		message = string(proof)
	}
	meta.SetStatusCondition(&status.Conditions, condition(configapi.NetworkControllerConfigConditionAdapterAvailable, true, "AdapterAvailable", "Catalyst Center cleanup adapter is available", c.Generation))
	meta.SetStatusCondition(&status.Conditions, condition(featureConfigCleanupCondition, complete, reason, message, c.Generation))
	meta.SetStatusCondition(&status.Conditions, condition(configapi.NetworkControllerConfigConditionReady, false, reason, "Deleting config is retained until manager finalization", c.Generation))
	var fresh configapi.NetworkControllerConfig
	if err = a.statusReader.Get(ctx, ctrlclient.ObjectKeyFromObject(c), &fresh); err != nil {
		return ctrl.Result{}, ctrlclient.IgnoreNotFound(err)
	}
	if fresh.UID != c.UID || fresh.Generation != c.Generation || fresh.ResourceVersion != c.ResourceVersion || featureIdentityDigest(fresh.Spec) != featureIdentityDigest(c.Spec) || featureIdentityDigest(fresh.DeletionTimestamp) != featureIdentityDigest(c.DeletionTimestamp) || !slices.Contains(fresh.Finalizers, family.finalizer) {
		return ctrl.Result{RequeueAfter: time.Second}, nil
	}
	fresh.Status = status
	if err = a.statusWriter.Status().Update(ctx, &fresh); err != nil {
		return ctrl.Result{}, err
	}
	return ctrl.Result{RequeueAfter: 5 * time.Second}, nil
}
