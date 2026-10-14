// Copyright 2026 Cisco Systems Inc.
// SPDX-License-Identifier: Apache-2.0
package sdwan

import (
	"context"
	"crypto/sha256"
	"errors"
	"fmt"
	coordinationv1 "k8s.io/api/coordination/v1"
	"sort"
	"time"

	configv1 "github.com/cisco/virtual-kubelet-cisco/api/config/v1alpha1"
	cvkv1 "github.com/cisco/virtual-kubelet-cisco/api/v1alpha1"
	"github.com/cisco/virtual-kubelet-cisco/internal/controlleradapter"
	"github.com/google/uuid"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

const TypeName = "sdwan"

func Descriptor() controlleradapter.Descriptor {
	return controlleradapter.Descriptor{Type: TypeName, DisplayName: "Catalyst SD-WAN", NetAsCode: cvkv1.NetworkControllerNetAsCodeStatus{Format: "netascode-sdwan", Stripe: "sdwan", ModelVersions: []string{ModelVersion}, Sections: []string{"edge_feature_templates", "feature_profiles", "configuration_groups", "topology_groups", "policy_groups", "localized_policies", "centralized_policies", "policy_objects", "network_hierarchy", "application_catalog"}}, Capabilities: []string{"health", "inventory", "config"}, WorkerClusterRole: controlleradapter.SDWANWorkerClusterRole}
}
func init() {
	controlleradapter.Register(controlleradapter.Registration{Descriptor: Descriptor(), Factory: newAdapter})
}

type adapter struct {
	opts    controlleradapter.Options
	kube    client.Client
	remote  *Client
	session string
}

func newAdapter(o controlleradapter.Options) (controlleradapter.Adapter, error) {
	if o.Controller == nil {
		return nil, errors.New("missing SD-WAN endpoint")
	}
	if o.Controller.Spec.PreferredAPIVersion != "" && o.Controller.Spec.PreferredAPIVersion != "20.18" {
		return nil, errors.New("unsupported SD-WAN API profile")
	}
	r := &Client{Changes: o.MaterialRotation.Changes, Endpoint: o.Controller.Spec.Endpoint, CredentialPath: o.CredentialPath, CAPath: o.CAPath, MaxSessionLifetime: o.MaterialRotation.MaxSessionLifetime}
	if o.Controller.Spec.TLS != nil {
		r.InsecureSkipVerify = o.Controller.Spec.TLS.InsecureSkipVerify
	}
	if o.Controller.Spec.Connection.RequestTimeout != nil {
		r.Timeout = o.Controller.Spec.Connection.RequestTimeout.Duration
	}
	if o.Controller.Spec.Connection.RateLimit != nil {
		r.RequestsPerSecond = o.Controller.Spec.Connection.RateLimit.RequestsPerSecond
	}
	if r.Timeout > 30*time.Second {
		return nil, errors.New("SD-WAN mutation profile requires request timeout at most 30s")
	}
	return &adapter{opts: o, remote: r, session: uuid.NewString()}, nil
}
func (a *adapter) SetupWithManager(m ctrl.Manager) error {
	var err error
	a.kube, err = client.New(m.GetConfig(), client.Options{Scheme: m.GetScheme()})
	if err != nil {
		return err
	}
	a.remote.Guard = a.guard
	return m.Add(a)
}
func (a *adapter) NeedLeaderElection() bool { return false }

type intentContextKey struct{}
type mutationContextKey struct{}
type intentSnapshot struct {
	config *configv1.NetworkControllerConfig
	source *corev1.ConfigMap
}

func (a *adapter) live(ctx context.Context) (*cvkv1.NetworkController, error) {
	c := &cvkv1.NetworkController{}
	if err := a.kube.Get(ctx, client.ObjectKeyFromObject(a.opts.Controller), c); err != nil {
		return nil, err
	}
	if c.UID != a.opts.Controller.UID || c.Generation != a.opts.Controller.Generation || c.Spec.Paused || !c.DeletionTimestamp.IsZero() {
		return nil, errors.New("SD-WAN endpoint binding is no longer active")
	}
	return c, nil
}
func (a *adapter) guard(ctx context.Context) error {
	if check, ok := ctx.Value(mutationContextKey{}).(func(context.Context) error); ok {
		if err := check(ctx); err != nil {
			return err
		}
	}
	if _, err := a.live(ctx); err != nil {
		return errors.New("SD-WAN endpoint authorization changed")
	}
	if snap, ok := ctx.Value(intentContextKey{}).(intentSnapshot); ok {
		cfg := &configv1.NetworkControllerConfig{}
		if a.kube.Get(ctx, client.ObjectKeyFromObject(snap.config), cfg) != nil || cfg.UID != snap.config.UID || cfg.Generation != snap.config.Generation || !cfg.DeletionTimestamp.IsZero() {
			return errors.New("SD-WAN intent authorization changed")
		}
		if snap.source != nil {
			cm := &corev1.ConfigMap{}
			if a.kube.Get(ctx, client.ObjectKeyFromObject(snap.source), cm) != nil || cm.UID != snap.source.UID || cm.ResourceVersion != snap.source.ResourceVersion {
				return errors.New("SD-WAN intent source changed")
			}
		}
	}
	return nil
}
func (a *adapter) Start(ctx context.Context) error {
	ticker := time.NewTicker(5 * time.Second)
	defer ticker.Stop()
	defer a.remote.Invalidate()
	var lastHealth time.Time
	for {
		interval := time.Minute
		if p := a.opts.Controller.Spec.Connection; p.HealthCheckInterval != nil {
			interval = p.HealthCheckInterval.Duration
		}
		if time.Since(lastHealth) >= interval {
			a.health(ctx)
			lastHealth = time.Now()
		}
		list := &configv1.NetworkControllerConfigList{}
		if a.kube.List(ctx, list, client.InNamespace(a.opts.Controller.Namespace)) == nil {
			for i := range list.Items {
				cfg := &list.Items[i]
				if cfg.Spec.ControllerRef.Name != a.opts.Controller.Name || !cfg.DeletionTimestamp.IsZero() {
					continue
				}
				interval := 5 * time.Minute
				if cfg.Spec.DriftDetectInterval != nil {
					interval = cfg.Spec.DriftDetectInterval.Duration
				}
				if cfg.Status.ObservedGeneration == cfg.Generation && cfg.Status.LastControllerCheck != nil && time.Since(cfg.Status.LastControllerCheck.Time) < interval && cfg.Status.Phase != configv1.NetworkControllerConfigPhaseApplying {
					continue
				}
				a.reconcile(ctx, cfg)
			}
		}
		select {
		case <-ctx.Done():
			return nil
		case <-a.opts.MaterialRotation.Changes:
			a.remote.Invalidate()
			lastHealth = time.Time{}
		case <-ticker.C:
		}
	}
}
func (a *adapter) health(ctx context.Context) {
	c, err := a.live(ctx)
	if err != nil {
		return
	}
	result, err := a.remote.Discover(ctx)
	if a.guard(ctx) != nil {
		return
	}
	base := c.DeepCopy()
	now := metav1.Now()
	c.Status.LastAttemptTime = &now
	condition := metav1.Condition{Type: "Authenticated", ObservedGeneration: c.Generation, Status: metav1.ConditionFalse, Reason: "ConnectionFailed", Message: "SD-WAN authentication or inventory failed"}
	c.Status.Phase = cvkv1.NetworkControllerPhaseDegraded
	c.Status.Capabilities = []cvkv1.NetworkControllerCapabilityStatus{{Name: "health", Supported: false, Message: "Current endpoint observation unavailable"}, {Name: "inventory", Supported: false, Message: "Current complete inventory unavailable"}, {Name: "config", Supported: false, Message: "Endpoint compatibility not established"}}
	if err == nil {
		condition.Status = metav1.ConditionTrue
		condition.Reason = "Authenticated"
		condition.Message = "SD-WAN session established"
		c.Status.ControllerVersion = result.Version
		c.Status.LastSuccessfulConnectionTime = &now
		c.Status.Phase = cvkv1.NetworkControllerPhaseReady
		c.Status.APIVersion = "20.18"
		d := Descriptor()
		c.Status.NetAsCode = &d.NetAsCode
		c.Status.Capabilities = []cvkv1.NetworkControllerCapabilityStatus{{Name: "health", Supported: true}, {Name: "inventory", Supported: true, Message: fmt.Sprintf("%d inventory entries", result.Count)}, {Name: "config", Supported: qualifiedVersion(result.Version), Message: "Unattached banner, system profile/parcel and configuration group fixtures"}}
	}
	meta.SetStatusCondition(&c.Status.Conditions, condition)
	condition.Type = "APICompatible"
	condition.Status = metav1.ConditionFalse
	condition.Reason = "UnsupportedVersion"
	condition.Message = "Requires qualified 20.18 Manager API"
	if err == nil && qualifiedVersion(result.Version) {
		condition.Status = metav1.ConditionTrue
		condition.Reason = "Qualified"
		condition.Message = "Qualified 20.18 API profile"
	} else {
		c.Status.Phase = cvkv1.NetworkControllerPhaseDegraded
	}
	meta.SetStatusCondition(&c.Status.Conditions, condition)
	condition.Type = "Ready"
	meta.SetStatusCondition(&c.Status.Conditions, condition)
	_ = a.kube.Status().Patch(ctx, c, client.MergeFromWithOptions(base, client.MergeFromWithOptimisticLock{}))
}
func qualifiedVersion(v string) bool { return v == "20.18.2.2" }
func resourceKey(r Resource) string {
	h := sha256.Sum256([]byte(r.Key))
	return fmt.Sprintf("%x", h[:])
}
func (a *adapter) reconcile(ctx context.Context, cfg *configv1.NetworkControllerConfig) {
	c, err := a.live(ctx)
	if err != nil {
		return
	}
	fail := func(reason string) { a.publish(ctx, cfg, configv1.NetworkControllerConfigPhaseFailed, reason, 0) }
	if controlleradapter.ValidateConfigContract(c, cfg, a.opts.IntentSecretPath) != nil {
		fail("InvalidContract")
		return
	}
	if (cfg.Spec.Scope != "" && cfg.Spec.Scope != "global") || len(cfg.Spec.SecretRefs) > 0 || cfg.Spec.PrunePolicy == configv1.NetworkControllerRetentionPolicyDelete || cfg.Spec.DeletionPolicy == configv1.NetworkControllerRetentionPolicyDelete {
		fail("UnsupportedPolicy")
		return
	}
	var data []byte
	var source *corev1.ConfigMap
	if cfg.Spec.Source.Inline != nil {
		data = cfg.Spec.Source.Inline.Raw
	} else if ref := cfg.Spec.Source.ConfigMapRef; ref != nil {
		source = &corev1.ConfigMap{}
		if a.kube.Get(ctx, client.ObjectKey{Namespace: cfg.Namespace, Name: ref.Name}, source) != nil {
			fail("SourceUnavailable")
			return
		}
		data = []byte(source.Data[ref.Key])
	}
	resources, err := Compile(data)
	if err != nil {
		fail("UnsupportedOrInvalidModel")
		return
	}
	managed := map[string]bool{}
	for _, section := range cfg.Spec.ManagedSections {
		managed[section] = true
	}
	for _, r := range resources {
		if !managed[r.Section] {
			fail("UnmanagedSourceSection")
			return
		}
	}
	ctx = context.WithValue(ctx, intentContextKey{}, intentSnapshot{cfg, source})
	info, err := a.remote.Discover(ctx)
	if err != nil {
		fail("ObservationFailed")
		return
	}
	if !qualifiedVersion(info.Version) {
		fail("UnsupportedManagerVersion")
		return
	}
	observations := make([]*Observation, len(resources))
	drift := false
	for i, r := range resources {
		o, e := a.remote.Observe(ctx, r)
		if e != nil {
			fail("ObservationFailed")
			return
		}
		observations[i] = o
		if o == nil {
			drift = true
		} else {
			same, e := Equal(r, o.Body)
			if e != nil {
				fail("ObservationFailed")
				return
			}
			drift = drift || !same
		}
	}
	if cfg.Spec.Mode != configv1.NetworkControllerApplyModeApply {
		phase := configv1.NetworkControllerConfigPhaseInSync
		if drift {
			phase = configv1.NetworkControllerConfigPhaseDrifted
		}
		a.publish(ctx, cfg, phase, "Observed", len(resources))
		return
	}

	type claim struct {
		lock    *controlleradapter.SectionLease
		lease   *coordinationv1.Lease
		journal controlleradapter.SectionJournal
	}
	claims := map[string]claim{}
	sections := append([]string(nil), cfg.Spec.ManagedSections...)
	sort.Strings(sections)
	for _, section := range sections {
		lock := &controlleradapter.SectionLease{Client: a.kube, Namespace: cfg.Namespace, ControllerUID: string(c.UID), Scope: "global", Section: section, ConfigUID: string(cfg.UID), Session: a.session}
		lease, journal, err := lock.Acquire(ctx)
		if err != nil {
			a.publish(ctx, cfg, configv1.NetworkControllerConfigPhaseLeaseBlocked, "SectionOwned", len(resources))
			return
		}
		claims[section] = claim{lock, lease, journal}
	}
	// Check the entire plan before the first mutation. Existing foreign objects
	// require an explicit future import workflow, even when their values match.
	for i, r := range resources {
		o := observations[i]
		receipt := claims[r.Section].journal[resourceKey(r)]
		if receipt.State == "Dispatched" {
			fail("OutcomeUnknown")
			return
		}
		if o != nil && (receipt.ID != o.ID || receipt.State == "") {
			fail("UnownedResource")
			return
		}
		if o == nil && receipt.State == "Accepted" {
			a.publish(ctx, cfg, configv1.NetworkControllerConfigPhaseApplying, "AwaitingReadback", len(resources))
			return
		}
		if o != nil && o.Attached {
			same, err := Equal(r, o.Body)
			if err != nil || !same {
				fail("AttachedResourceUnsupported")
				return
			}
		}
	}
	for i, r := range resources {
		o := observations[i]
		if o != nil {
			same, _ := Equal(r, o.Body)
			if same {
				claim := claims[r.Section]
				key := resourceKey(r)
				receipt := claim.journal[key]
				if receipt.State == "Accepted" {
					receipt.State = "Verified"
					claim.journal[key] = receipt
					if claim.lock.Save(ctx, claim.lease, claim.journal) != nil {
						fail("OwnershipChanged")
						return
					}
				}
				continue
			}
		}
		claim := claims[r.Section]
		lock, lease, journal := claim.lock, claim.lease, claim.journal
		key := resourceKey(r)
		receipt := controlleradapter.MutationReceipt{State: "Dispatched"}
		if o != nil {
			receipt.ID = o.ID
		}
		journal[key] = receipt
		if a.guard(ctx) != nil {
			return
		}
		if lock.Save(ctx, lease, journal) != nil {
			fail("OwnershipChanged")
			return
		}
		if a.guard(ctx) != nil {
			return
		}
		id := receipt.ID
		if o == nil {
			id, err = a.remote.Create(context.WithValue(ctx, mutationContextKey{}, func(c context.Context) error { return lock.Check(c, lease) }), r)
		} else {
			err = a.remote.Update(context.WithValue(ctx, mutationContextKey{}, func(c context.Context) error { return lock.Check(c, lease) }), r, o)
		}
		if err != nil {
			fail("OutcomeUnknown")
			return
		}
		receipt.ID = id
		receipt.State = "Accepted"
		journal[key] = receipt
		if lock.Save(ctx, lease, journal) != nil {
			fail("OutcomeUnknown")
			return
		}
		observed, e := a.remote.Observe(ctx, r)
		if e != nil || observed == nil || observed.ID != id {
			fail("VerificationFailed")
			return
		}
		same, e := Equal(r, observed.Body)
		if e != nil || !same {
			fail("VerificationFailed")
			return
		}
		receipt.State = "Verified"
		journal[key] = receipt
		if lock.Save(ctx, lease, journal) != nil {
			fail("OwnershipChanged")
			return
		}
		a.publish(ctx, cfg, configv1.NetworkControllerConfigPhaseApplying, "ResourceVerified", len(resources))
		return
	}
	a.publish(ctx, cfg, configv1.NetworkControllerConfigPhaseInSync, "Verified", len(resources))
}
func (a *adapter) publish(ctx context.Context, cfg *configv1.NetworkControllerConfig, phase configv1.NetworkControllerConfigPhase, reason string, count int) {
	if a.guard(context.WithValue(ctx, intentContextKey{}, intentSnapshot{config: cfg})) != nil {
		return
	}
	live := &configv1.NetworkControllerConfig{}
	if a.kube.Get(ctx, client.ObjectKeyFromObject(cfg), live) != nil || live.UID != cfg.UID || live.Generation != cfg.Generation {
		return
	}
	base := live.DeepCopy()
	now := metav1.Now()
	live.Status.Phase = phase
	live.Status.ObservedGeneration = cfg.Generation
	live.Status.LastControllerCheck = &now
	condition := metav1.Condition{Type: "Ready", Status: metav1.ConditionFalse, Reason: reason, Message: reason, ObservedGeneration: cfg.Generation}
	state := configv1.NetworkControllerSectionStateApplyError
	if phase == configv1.NetworkControllerConfigPhaseInSync {
		condition.Status = metav1.ConditionTrue
		state = configv1.NetworkControllerSectionStateInSync
		if cfg.Spec.Mode == configv1.NetworkControllerApplyModeApply {
			live.Status.LastAppliedTime = &now
			live.Status.LastAppliedSourceRevision = cfg.Spec.ModelSource.SourceRevision
		}
	}
	if phase == configv1.NetworkControllerConfigPhaseDrifted {
		state = configv1.NetworkControllerSectionStateDrifted
	}
	if phase == configv1.NetworkControllerConfigPhaseApplying {
		state = configv1.NetworkControllerSectionStateApplying
	}
	meta.SetStatusCondition(&live.Status.Conditions, condition)
	live.Status.SectionStatus = nil
	for _, section := range cfg.Spec.ManagedSections {
		live.Status.SectionStatus = append(live.Status.SectionStatus, configv1.NetworkControllerSectionStatus{Name: section, State: state, Message: reason})
	}
	_ = a.kube.Status().Patch(ctx, live, client.MergeFromWithOptions(base, client.MergeFromWithOptimisticLock{}))
}
