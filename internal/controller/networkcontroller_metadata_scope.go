// Copyright 2026 Cisco Systems Inc.
// SPDX-License-Identifier: Apache-2.0
package controller

import (
	"context"
	"errors"
	"time"

	config "github.com/cisco/virtual-kubelet-cisco/api/config/v1alpha1"
	cvk "github.com/cisco/virtual-kubelet-cisco/api/v1alpha1"
	"github.com/cisco/virtual-kubelet-cisco/internal/controlleradapter"
	contract "github.com/cisco/virtual-kubelet-cisco/internal/controllerconfiguration"
	core "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

// The manager evaluates pure product projection plus the shared read-only
// prerequisite. Nil compatibility remains observation-only. A compatible
// callback permits staged seal/fence custody; neither path grants dispatch.
func (r *NetworkControllerReconciler) reconcileConfigurationMetadataScope(ctx context.Context, nc *cvk.NetworkController) (bool, error) {
	if nc == nil {
		return false, nil
	}
	scope := controlleradapter.ConfigurationMetadataScopeFor(string(nc.Spec.Type))
	if scope == nil || !scope.Selected(nc) {
		return false, nil
	}
	reject := errors.New("configuration metadata scope requires current manager installation and source binding")
	if r.APIReader == nil || r.ConfigurationAdmissionBinding == nil || r.ConfigurationAdmissionBinding.Validate() != nil || r.ConfigurationAdmissionBinding.LeaseNamespace == "" || nc.Status.Configuration == nil || nc.Status.Configuration.Operation == nil {
		return true, reject
	}
	op := nc.Status.Configuration.Operation
	var intent config.NetworkControllerConfig
	if err := r.APIReader.Get(ctx, client.ObjectKey{Namespace: nc.Namespace, Name: op.ConfigName}, &intent); err != nil {
		return true, err
	}
	if string(intent.UID) != op.ConfigUID || intent.Generation != op.ConfigGeneration || intent.DeletionTimestamp != nil || (intent.Spec.Source.Inline == nil) == (intent.Spec.Source.ConfigMapRef == nil) {
		return true, reject
	}
	source := controlleradapter.ConfigurationMetadataSource{}
	if inline := intent.Spec.Source.Inline; inline != nil {
		source.Raw = append([]byte(nil), inline.Raw...)
	} else {
		ref := intent.Spec.Source.ConfigMapRef
		if ref.Name == "" || ref.Key == "" {
			return true, reject
		}
		var cm core.ConfigMap
		if err := r.APIReader.Get(ctx, client.ObjectKey{Namespace: intent.Namespace, Name: ref.Name}, &cm); err != nil {
			return true, err
		}
		value, ok := cm.Data[ref.Key]
		if !ok || cm.UID == "" || cm.DeletionTimestamp != nil || string(cm.UID) != op.SourceUID || cm.ResourceVersion != op.SourceVersion {
			return true, reject
		}
		source.Raw = []byte(value)
		source.UID = string(cm.UID)
		source.Version = cm.ResourceVersion
	}
	serials, err := scope.PhysicalIdentities(nc.DeepCopy(), intent.DeepCopy(), source)
	if err != nil {
		return true, err
	}
	// The shared helper corroborates current config/source after projection as
	// well as after the two full cluster observations. The callback is not a grant.

	if r.AuthorizeMetadataHandoff != nil {
		binding := r.ConfigurationAdmissionBinding
		policyName := binding.AdmissionPrefix + "-metadata-handoff"
		if nc.Status.Configuration.MetadataHandoff == nil {
			if _, err = contract.EnsureMetadataHandoff(ctx, r.APIReader, r.Client, nc, policyName, binding.ManagerUsername, binding.LeaseNamespace, serials, r.AuthorizeMetadataHandoff, time.Now()); err != nil {
				return true, err
			}
			// The seal changed resourceVersion. Use a fresh authoritative snapshot;
			// acquisition repeats the original source/actor/cohort corroboration.
			var sealed cvk.NetworkController
			if err = r.APIReader.Get(ctx, client.ObjectKeyFromObject(nc), &sealed); err != nil {
				return true, err
			}
			nc = &sealed
		}
		_, err = contract.AcquireMetadataFenceCohort(ctx, r.APIReader, r.Client, nc, contract.BootstrapFenceInstallation{PolicyName: binding.AdmissionPrefix + "-bootstrap-fence", ManagerUsername: binding.ManagerUsername, LeaseNamespace: binding.LeaseNamespace}, policyName, serials, r.AuthorizeMetadataHandoff, time.Now())
		return true, err
	}
	_, err = contract.ReadMetadataScopeWorker(ctx, r.APIReader, nc, r.ConfigurationAdmissionBinding.LeaseNamespace, serials, time.Now())
	return true, err
}

// Outcome settlement is product-pure and independent of current model presence
// or a live original worker. In-flight/Unknown custody remains pending. Nil
// compatibility prevents mutations. Installed manager composition provides a
// live recovery check independently of enabling new handoffs.
func (r *NetworkControllerReconciler) reconcileMetadataSettlement(ctx context.Context, nc *cvk.NetworkController) (bool, error) {
	if nc == nil || nc.Status.Configuration == nil || nc.Status.Configuration.MetadataHandoff == nil {
		return false, nil
	}
	h := nc.Status.Configuration.MetadataHandoff
	op := nc.Status.Configuration.Operation
	scope := controlleradapter.ConfigurationMetadataScopeFor(string(nc.Spec.Type))
	if op != nil && op.Phase == "Prepared" && h.ReadyAt != nil && h.CancelledAt == nil && h.RefusedAt == nil && h.ReleasedAt == nil {
		invalid, err := r.metadataPreparationInvalidated(ctx, nc)
		if err != nil {
			return true, err
		}
		if invalid {
			if scope == nil || scope.Cancellation == nil || r.AuthorizeMetadataSettlement == nil || r.ConfigurationAdmissionBinding == nil || r.ConfigurationAdmissionBinding.Validate() != nil {
				return true, errors.New("prepared cancellation requires compatible installed manager recovery")
			}
			b := r.ConfigurationAdmissionBinding
			updated, err := contract.CancelMetadataHandoff(ctx, r.APIReader, r.Client, nc, contract.BootstrapFenceInstallation{PolicyName: b.AdmissionPrefix + "-bootstrap-fence", ManagerUsername: b.ManagerUsername, LeaseNamespace: b.LeaseNamespace}, b.AdmissionPrefix+"-metadata-handoff", scope.Cancellation, r.AuthorizeMetadataSettlement, time.Now())
			if err != nil {
				return true, err
			}
			*nc = *updated.DeepCopy()
			return true, nil
		}
	}
	if h.CancelledAt == nil && h.RefusedAt == nil && h.ReleasedAt == nil && (op == nil || op.Phase != "Verified") {
		return true, nil
	}
	if scope == nil || scope.Settlement == nil || r.APIReader == nil || r.AuthorizeMetadataSettlement == nil || r.ConfigurationAdmissionBinding == nil || r.ConfigurationAdmissionBinding.Validate() != nil {
		return true, errors.New("terminal metadata custody requires compatible installed manager settlement")
	}
	var current cvk.NetworkController
	if err := r.APIReader.Get(ctx, client.ObjectKeyFromObject(nc), &current); err != nil {
		return true, err
	}
	if current.UID != nc.UID {
		return true, errors.New("metadata settlement controller changed")
	}
	binding := r.ConfigurationAdmissionBinding
	installation := contract.BootstrapFenceInstallation{PolicyName: binding.AdmissionPrefix + "-bootstrap-fence", ManagerUsername: binding.ManagerUsername, LeaseNamespace: binding.LeaseNamespace}
	settle := contract.SettleMetadataHandoff
	if current.Status.Configuration != nil && current.Status.Configuration.MetadataHandoff != nil && current.Status.Configuration.MetadataHandoff.ReleasedAt != nil {
		settle = contract.RetireMetadataHandoff
	}
	updated, err := settle(ctx, r.APIReader, r.Client, &current, installation, binding.AdmissionPrefix+"-metadata-handoff", scope.Settlement, r.AuthorizeMetadataSettlement, time.Now())
	if err != nil {
		return true, err
	}
	*nc = *updated.DeepCopy()
	return nc.Status.Configuration.MetadataHandoff != nil, nil
}

// Only definitive invalidation selects cancellation. Read errors are not proof
// of deletion, and a changed source never authorizes native replay.
func (r *NetworkControllerReconciler) metadataPreparationInvalidated(ctx context.Context, nc *cvk.NetworkController) (bool, error) {
	if r.APIReader == nil {
		return false, errors.New("metadata cancellation requires uncached source observations")
	}
	op := nc.Status.Configuration.Operation
	if nc.Spec.Paused || nc.DeletionTimestamp != nil || op.ControllerGeneration != nc.Generation {
		return true, nil
	}
	var c config.NetworkControllerConfig
	err := r.APIReader.Get(ctx, client.ObjectKey{Namespace: nc.Namespace, Name: op.ConfigName}, &c)
	if apierrors.IsNotFound(err) {
		return true, nil
	}
	if err != nil {
		return false, err
	}
	if string(c.UID) != op.ConfigUID || c.Generation != op.ConfigGeneration || c.DeletionTimestamp != nil || c.Spec.ControllerRef.Name != nc.Name {
		return true, nil
	}
	if op.SourceUID == "" {
		return c.Spec.Source.Inline == nil || c.Spec.Source.ConfigMapRef != nil, nil
	}
	if c.Spec.Source.ConfigMapRef == nil || c.Spec.Source.Inline != nil {
		return true, nil
	}
	var cm core.ConfigMap
	err = r.APIReader.Get(ctx, client.ObjectKey{Namespace: c.Namespace, Name: c.Spec.Source.ConfigMapRef.Name}, &cm)
	if apierrors.IsNotFound(err) {
		return true, nil
	}
	if err != nil {
		return false, err
	}
	return string(cm.UID) != op.SourceUID || cm.ResourceVersion != op.SourceVersion || cm.DeletionTimestamp != nil, nil
}
