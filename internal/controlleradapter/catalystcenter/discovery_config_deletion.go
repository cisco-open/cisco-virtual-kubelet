// Copyright 2026 Cisco Systems Inc.
// SPDX-License-Identifier: Apache-2.0
package catalystcenter

import (
	"context"
	"errors"
	"reflect"
	"slices"
	"time"

	configapi "github.com/cisco/virtual-kubelet-cisco/api/config/v1alpha1"
	cvk "github.com/cisco/virtual-kubelet-cisco/api/v1alpha1"
	"github.com/cisco/virtual-kubelet-cisco/internal/controlleradapter"
	"k8s.io/apimachinery/pkg/api/meta"
	ctrl "sigs.k8s.io/controller-runtime"
	ctrlclient "sigs.k8s.io/controller-runtime/pkg/client"
)

func hasDiscoveryConfigFinalizer(c *configapi.NetworkControllerConfig) bool {
	return c != nil && slices.Contains(c.Finalizers, discoveryConfigFinalizer)
}

// Metadata-only enrollment. The worker validates the complete source before any
// write; ConfigMap contents are never read by the registry or trusted manager.
func discoveryDeletionEligible(nc *cvk.NetworkController, c *configapi.NetworkControllerConfig) bool {
	return nc != nil && nc.Spec.Type == TypeName && !nc.Spec.Paused && nc.DeletionTimestamp.IsZero() && (discoveryConfigSpecShape(c) || discoveryCredentialDeletionSpecShape(c)) && c.DeletionTimestamp.IsZero() && !hasFeatureConfigFinalizer(c) && controlleradapter.ValidateConfigMetadataContract(nc, c) == nil
}

func (r *configReconciler) reconcileDiscoveryConfigDeletion(ctx context.Context, c *configapi.NetworkControllerConfig) (ctrl.Result, error) {
	a := r.a
	nc, err := a.currentController(ctx)
	if errors.Is(err, errStaleController) {
		return ctrl.Result{RequeueAfter: 5 * time.Second}, nil
	}
	if err != nil {
		return ctrl.Result{}, err
	}
	if r.tagDeletionSelected(ctx, nc, c) {
		return r.reconcileTagConfigDeletion(ctx, c)
	}
	credential := discoveryCredentialHistoryOwner(nc, c) || discoveryCredentialDeletionSpecShape(c)
	reason, message := "DiscoveryConfigCleanupProgressing", "Discovery cleanup retains its original tasks, ownership and exact native identities"
	shapeOK := discoveryConfigCleanupShape(c)
	contractErr := controlleradapter.ValidateConfigContract(nc, c, a.intentSecretPath)
	if credential {
		shapeOK = discoveryCredentialCleanupObservationShape(c)
		contractErr = controlleradapter.ValidateConfigMetadataContract(nc, c)
	}
	if !shapeOK || hasFeatureConfigFinalizer(c) || contractErr != nil {
		reason, message = "DiscoveryConfigCleanupUnsupported", "Deleting intent no longer matches the enrolled discovery-only lifecycle; finalizer retained"
	} else {
		var op *cvk.NetworkControllerConfigurationOperation
		if nc.Status.Configuration != nil {
			op = nc.Status.Configuration.Operation
		}
		switch {
		case op != nil:
			if op.ConfigUID != string(c.UID) || op.ConfigName != c.Name {
				err = errConfigurationBusy
			} else if credential {
				if discoveryFinalizeSelected(op.Resource) {
					err = a.advanceDiscoveryCredentialConfigCleanup(ctx, c)
				} else {
					err = a.drainDiscoveryCredentialConfigOperation(ctx, c)
				}
			} else if discoveryFinalizeSelected(op.Resource) {
				err = a.advanceDiscoveryConfigCleanup(ctx, c)
			} else {
				err = a.drainDiscoveryConfigOperation(ctx, c)
			}
		case credential:
			if _, unsubmittedErr := discoveryCredentialUnsubmittedRecord(nc, c); unsubmittedErr == nil {
				err = a.publishUnsubmittedDiscoveryCredentialCompletion(ctx, c)
				if err == nil {
					return ctrl.Result{RequeueAfter: 5 * time.Second}, nil
				}
			} else if _, closedErr := discoveryCredentialClosedEvidence(nc, c); closedErr == nil {
				err = a.publishDiscoveryCredentialConfigCompletion(ctx, c)
				if err == nil {
					return ctrl.Result{RequeueAfter: 5 * time.Second}, nil
				}
			} else {
				_, err = a.prepareDiscoveryCredentialConfigCleanup(ctx, c)
			}
		case discoveryConfigKnownWorkClosed(nc, c):
			if err = a.publishDiscoveryConfigCompletion(ctx, c); err == nil {
				return ctrl.Result{RequeueAfter: 5 * time.Second}, nil
			}
		default:
			_, err = a.prepareDiscoveryConfigCleanup(ctx, c)
		}
		if err != nil {
			reason, message = "DiscoveryConfigCleanupPending", "Cleanup requires unchanged source, ownership, original-task completion and native absence; no replay is authorized"
		}
	}
	var fresh configapi.NetworkControllerConfig
	if err := a.statusReader.Get(ctx, ctrlclient.ObjectKeyFromObject(c), &fresh); err != nil {
		return ctrl.Result{}, ctrlclient.IgnoreNotFound(err)
	}
	if fresh.UID != c.UID || fresh.Generation != c.Generation || fresh.ResourceVersion != c.ResourceVersion || !reflect.DeepEqual(fresh.Spec, c.Spec) || !reflect.DeepEqual(fresh.DeletionTimestamp, c.DeletionTimestamp) || !hasDiscoveryConfigFinalizer(&fresh) {
		return ctrl.Result{RequeueAfter: time.Second}, nil
	}
	fresh.Status.ObservedGeneration = fresh.Generation
	fresh.Status.Phase = configapi.NetworkControllerConfigPhaseWaiting
	fresh.Status.SectionStatus = []configapi.NetworkControllerSectionStatus{{Name: "inventory", State: configapi.NetworkControllerSectionStateApplying, Message: message}}
	meta.SetStatusCondition(&fresh.Status.Conditions, condition(configapi.NetworkControllerConfigConditionAdapterAvailable, true, "AdapterAvailable", "Catalyst Center discovery cleanup adapter is available", fresh.Generation))
	meta.SetStatusCondition(&fresh.Status.Conditions, condition(discoveryConfigCleanupCondition, false, reason, message, fresh.Generation))
	meta.SetStatusCondition(&fresh.Status.Conditions, condition(configapi.NetworkControllerConfigConditionReady, false, reason, "Deleting config is retained until manager finalization", fresh.Generation))
	if err := a.statusWriter.Status().Update(ctx, &fresh); err != nil {
		return ctrl.Result{}, err
	}
	return ctrl.Result{RequeueAfter: 5 * time.Second}, nil
}

// Match the common Apply task-timeout contract without issuing another request.
// Accepted work becomes Unknown once; subsequent observation retains its exact
// original task and may still verify a late successful completion.
func (a *adapter) observeDiscoveryDeletionTask(ctx context.Context, c *configapi.NetworkControllerConfig, op *cvk.NetworkControllerConfigurationOperation) (bool, error) {
	timeout := 30 * time.Minute
	if c.Spec.TaskTimeout != nil {
		timeout = c.Spec.TaskTimeout.Duration
	}
	if op.Phase == "Accepted" && (timeout <= 0 || time.Since(op.SubmittedAt.Time) > timeout) {
		if err := a.advanceConfiguration(ctx, op.ID, "Unknown", ""); err != nil {
			return false, err
		}
		return false, errConfigurationBusy
	}
	return a.client.configurationTaskComplete(ctx, op.TaskID, op.SubmittedAt.Time, time.Now())
}
