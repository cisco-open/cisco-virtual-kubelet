// Copyright © 2026 Cisco Systems Inc.
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//     http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.

package maintenance

import (
	"context"
	"errors"
	"fmt"
	"strconv"
	"sync"
	"time"

	"github.com/google/uuid"
	"github.com/virtual-kubelet/virtual-kubelet/log"
	coordv1 "k8s.io/api/coordination/v1"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/util/retry"
	"sigs.k8s.io/controller-runtime/pkg/client"

	opsv1alpha1 "github.com/cisco/virtual-kubelet-cisco/api/ops/v1alpha1"
	ciskov1 "github.com/cisco/virtual-kubelet-cisco/api/v1alpha1"
	"github.com/cisco/virtual-kubelet-cisco/internal/configengine/engine"
	"github.com/cisco/virtual-kubelet-cisco/internal/devicecoordination"
	"github.com/cisco/virtual-kubelet-cisco/internal/managedprotocol"
	"github.com/cisco/virtual-kubelet-cisco/internal/provider/mutationguard"
	"github.com/cisco/virtual-kubelet-cisco/internal/workloaddrain"
)

const (
	maxManagedDrainDuration = 2 * time.Hour
	drainRecoveryExtraLimit = 2 * time.Minute
)

// PodDeleteDisposition identifies the only three provider-side outcomes for a
// Kubernetes Pod deletion callback. Released completion is deliberately not a
// device mutation: it acknowledges an already-proven teardown so upstream
// Virtual Kubelet can complete its exact-UID Kubernetes deletion.
type PodDeleteDisposition uint8

const (
	PodDeleteOrdinary PodDeleteDisposition = iota
	PodDeleteDrainTeardown
	PodDeleteReleasedCompletion
)

// ResolveDrainDeletePod selects the strict teardown path from both the callback
// and the uncached live Pod. Informer delivery can lag the manager's protection
// patch, so an unmarked callback must not bypass a live exact-UID drain marker
// while ordinary writes are open during recovery. A stale caller marker may be
// superseded only by a fresh, exact DeviceClean/Released completion proof;
// otherwise it still fails closed into the strict path. Unmanaged and
// absent/unprotected live Pods retain the established ordinary-delete behavior.
func (c *Coordinator) ResolveDrainDeletePod(
	ctx context.Context,
	requested *corev1.Pod,
) (*corev1.Pod, PodDeleteDisposition, error) {
	callerMarked := hasDrainPodMarker(requested)
	if c == nil || !c.ManagedTopology {
		if callerMarked {
			return requested, PodDeleteDrainTeardown, nil
		}
		return requested, PodDeleteOrdinary, nil
	}
	if c.Client == nil || requested == nil || requested.Namespace == "" || requested.Name == "" || requested.UID == "" {
		return nil, PodDeleteOrdinary, fmt.Errorf("resolve managed drain Pod: callback identity or API client is incomplete")
	}

	readCtx, cancel := context.WithTimeout(ctx, apiTimeout)
	defer cancel()
	var live corev1.Pod
	if err := c.Client.Get(readCtx, types.NamespacedName{
		Namespace: requested.Namespace,
		Name:      requested.Name,
	}, &live); err != nil {
		if apierrors.IsNotFound(err) {
			if callerMarked {
				return requested, PodDeleteDrainTeardown, nil
			}
			return requested, PodDeleteOrdinary, nil
		}
		return nil, PodDeleteOrdinary, fmt.Errorf("read live Pod before managed delete routing: %w", err)
	}
	if hasDrainPodMarker(&live) {
		if live.UID == "" || live.UID != requested.UID {
			return nil, PodDeleteOrdinary, fmt.Errorf(
				"live protected Pod %s/%s has UID %q, not callback UID %q",
				live.Namespace, live.Name, live.UID, requested.UID,
			)
		}
		return &live, PodDeleteDrainTeardown, nil
	}
	if live.UID != requested.UID {
		return nil, PodDeleteOrdinary, fmt.Errorf(
			"live Pod %s/%s has UID %q, not callback UID %q",
			live.Namespace, live.Name, live.UID, requested.UID,
		)
	}
	if live.Spec.NodeName != c.NodeName {
		return nil, PodDeleteOrdinary, fmt.Errorf(
			"live Pod %s/%s binds Node %q, not runtime Node %q",
			live.Namespace, live.Name, live.Spec.NodeName, c.NodeName,
		)
	}
	if live.DeletionTimestamp == nil || live.DeletionTimestamp.IsZero() {
		if callerMarked {
			return requested, PodDeleteDrainTeardown, nil
		}
		return requested, PodDeleteOrdinary, nil
	}
	released, err := c.authorizeReleasedDrainCompletion(readCtx, &live, time.Now())
	if err != nil {
		return nil, PodDeleteOrdinary, err
	}
	if released {
		return &live, PodDeleteReleasedCompletion, nil
	}
	if callerMarked {
		return requested, PodDeleteDrainTeardown, nil
	}
	return requested, PodDeleteOrdinary, nil
}

func hasDrainPodMarker(pod *corev1.Pod) bool {
	if pod == nil {
		return false
	}
	if _, marked := pod.Annotations[managedprotocol.AnnotationDrainSession]; marked {
		return true
	}
	for _, finalizer := range pod.Finalizers {
		if finalizer == managedprotocol.DrainPodFinalizer {
			return true
		}
	}
	return false
}

// AcquireDrainDelete authorizes only the device-side deletion of one exact Pod
// selected by the manager's PDB-aware drain. It never grants ordinary Pod
// writes. The provider owns the deterministic drain Lease for the complete
// callback and retains it whenever the device outcome is uncertain.
func (c *Coordinator) AcquireDrainDelete(
	ctx context.Context,
	pod *corev1.Pod,
) (context.Context, func(error) error, error) {
	if c == nil || !c.ManagedTopology {
		return ctx, nil, fmt.Errorf("managed drain delete requires a managed maintenance coordinator")
	}
	if c.Client == nil || c.Namespace == "" || c.DeviceName == "" || c.DeviceUID == "" ||
		c.NodeName == "" || c.WorkerRevision == "" || c.LeaseNamespace == "" {
		return ctx, nil, fmt.Errorf("managed drain delete coordinator is incomplete")
	}
	if pod == nil || pod.Namespace == "" || pod.Name == "" || pod.UID == "" {
		return ctx, nil, fmt.Errorf("managed drain delete Pod identity is incomplete")
	}

	// Every selected Pod in one drain uses the same immutable holder. Serialize
	// callbacks so an earlier completion cannot release a later callback's Lease.
	c.drainMu.Lock()
	locked := true
	unlock := func() {
		if locked {
			locked = false
			c.drainMu.Unlock()
		}
	}

	readCtx, readCancel := context.WithTimeout(ctx, apiTimeout)
	now := time.Now()
	authority, err := c.authorizeDrainDelete(readCtx, pod, now, false)
	if err != nil {
		// A recovery revision can advance while an uncertain, same-session
		// teardown still quarantines the canonical Lease under the preceding
		// revision. Never renew or dispatch under that stale acknowledgement.
		// Once its exact TTL has elapsed, retire only that fully bound holder so
		// the manager can acknowledge the current revision on a later reconcile.
		if handled, retirementErr := c.retireExpiredStaleDrainRecoveryLease(readCtx, pod, now); handled {
			readCancel()
			unlock()
			return ctx, nil, retirementErr
		}
	}
	readCancel()
	if err != nil {
		unlock()
		return ctx, nil, err
	}
	initialAuthority := authority
	leaser := &engine.FamilyLeaser{
		Client: c.Client, Namespace: c.LeaseNamespace, TTL: writeLeaseTTL, RequireExisting: true,
	}
	deviceKey := devicecoordination.DeviceKey(c.Namespace, c.DeviceName)
	acquireCtx, acquireCancel := context.WithTimeout(ctx, apiTimeout)
	guard, err := mutationguard.EnsureCanonicalQuarantine(
		acquireCtx, c.Client, leaser, c.Namespace, c.DeviceName, deviceKey, authority.holder, time.Now(),
	)
	if err != nil {
		acquireCancel()
		unlock()
		return ctx, nil, fmt.Errorf("managed drain safety scan: %w", err)
	}
	if guard.Risk != nil {
		acquireCancel()
		unlock()
		return ctx, nil, fmt.Errorf("managed drain waits for %s", guard.Risk)
	}
	result, err := leaser.Acquire(acquireCtx, deviceKey, devicecoordination.MutationLeaseFamily, authority.holder)
	if err != nil {
		acquireCancel()
		unlock()
		return ctx, nil, fmt.Errorf("acquire managed drain mutation Lease: %w", err)
	}
	if !result.Owned {
		acquireCancel()
		unlock()
		return ctx, nil, fmt.Errorf("managed drain mutation Lease is held by %s", result.Holder)
	}
	acquiredHolder := authority.holder
	releaseBeforeDispatch := func(cause error) error {
		releaseCtx, releaseCancel := context.WithTimeout(context.WithoutCancel(ctx), apiTimeout)
		defer releaseCancel()
		if releaseErr := leaser.Release(
			releaseCtx, deviceKey, devicecoordination.MutationLeaseFamily, acquiredHolder,
		); releaseErr != nil {
			return errors.Join(cause, fmt.Errorf("release managed drain Lease before device dispatch: %w", releaseErr))
		}
		return cause
	}
	if err := c.publishDrainLeaseRequest(acquireCtx, authority); err != nil {
		acquireCancel()
		err = releaseBeforeDispatch(err)
		unlock()
		return ctx, nil, err
	}
	// Re-read every authority object after acquiring and publishing under the
	// Lease. A concurrent manager transition must win before device dispatch.
	authority, err = c.authorizeDrainDelete(acquireCtx, pod, time.Now(), true)
	if err == nil {
		err = validateDrainPreDispatchFence(initialAuthority, authority)
	}
	acquireCancel()
	if err != nil {
		err = releaseBeforeDispatch(err)
		unlock()
		return ctx, nil, err
	}

	maximum := time.Now().Add(maxWriteDuration)
	if authority.deadline.After(maximum) {
		authority.deadline = maximum
	}
	deleteCtx, cancel := context.WithDeadline(ctx, authority.deadline)
	stop := make(chan struct{})
	done := make(chan struct{})
	interval := c.renewInterval
	if interval <= 0 {
		interval = 30 * time.Second
	}
	go func() {
		defer close(done)
		ticker := time.NewTicker(interval)
		defer ticker.Stop()
		for {
			select {
			case <-stop:
				return
			case <-deleteCtx.Done():
				return
			case <-ticker.C:
				renewCtx, renewCancel := context.WithTimeout(deleteCtx, apiTimeout)
				freshAuthority, authorityErr := c.authorizeDrainDelete(renewCtx, pod, time.Now(), true)
				if authorityErr != nil {
					renewCancel()
					log.G(ctx).WithError(authorityErr).Warn(
						"managed drain authorization lost; cancelling Pod teardown before Lease renewal",
					)
					cancel()
					return
				}
				renewed, renewErr := leaser.Acquire(
					renewCtx, deviceKey, devicecoordination.MutationLeaseFamily, freshAuthority.holder,
				)
				renewCancel()
				if renewErr != nil || !renewed.Owned {
					log.G(ctx).WithError(renewErr).Warn(
						"managed drain Lease renewal lost; cancelling Pod teardown",
					)
					cancel()
					return
				}
			}
		}
	}()
	var once sync.Once
	var finishErr error
	finish := func(outcome error) error {
		once.Do(func() {
			defer unlock()
			close(stop)
			<-done
			cancelled := deleteCtx.Err() != nil
			cancel()
			if outcome != nil {
				return
			}
			if cancelled {
				finishErr = fmt.Errorf("%w: managed drain context ended before Lease release", devicecoordination.ErrMutationIncomplete)
				return
			}
			releaseCtx, releaseCancel := context.WithTimeout(context.WithoutCancel(ctx), apiTimeout)
			defer releaseCancel()
			if err := leaser.Release(
				releaseCtx, deviceKey, devicecoordination.MutationLeaseFamily, authority.holder,
			); err != nil {
				finishErr = fmt.Errorf("release managed drain Lease after device-clean publication: %w", err)
			}
		})
		return finishErr
	}
	return deleteCtx, finish, nil
}

type drainDeleteAuthority struct {
	deadline             time.Time
	holder               string
	podPhase             opsv1alpha1.UpgradeDrainPodPhase
	lease                coordv1.Lease
	session              ciskov1.DeviceMaintenanceSessionStatus
	drain                opsv1alpha1.UpgradeManagerDrainStatus
	workerConfigRevision string
}

type drainDeleteAuthorizationMode uint8

const (
	drainDeleteDeviceTeardown drainDeleteAuthorizationMode = iota
	drainDeleteReleasedCompletion
)

func (c *Coordinator) authorizeDrainDelete(
	ctx context.Context,
	requested *corev1.Pod,
	now time.Time,
	requireHeldLease bool,
) (drainDeleteAuthority, error) {
	return c.authorizeDrainDeleteWithRevisionRollover(
		ctx, requested, now, requireHeldLease, false, drainDeleteDeviceTeardown,
	)
}

// authorizeReleasedDrainCompletion recognizes only the narrow post-clean
// callback boundary where the manager removed its protection from an exact,
// terminating Pod but upstream Virtual Kubelet still needs a successful
// provider acknowledgement before it deletes the Kubernetes object. A false
// result means this is not an active PDB-drain completion candidate. A true
// result never grants device access and never writes Kubernetes state.
func (c *Coordinator) authorizeReleasedDrainCompletion(
	ctx context.Context,
	pod *corev1.Pod,
	now time.Time,
) (bool, error) {
	var device ciskov1.CiscoDevice
	if err := c.Client.Get(ctx, types.NamespacedName{Namespace: c.Namespace, Name: c.DeviceName}, &device); err != nil {
		return false, fmt.Errorf("read managed drain CiscoDevice for released completion: %w", err)
	}
	session := device.Status.MaintenanceSession
	if session == nil || session.ProtocolVersion != ciskov1.DeviceMaintenanceProtocolPDBDrainV1 ||
		session.Purpose != ciskov1.DeviceMaintenancePurposeWorkloadDrain ||
		(session.Phase != ciskov1.DeviceMaintenanceSessionActive &&
			session.Phase != ciskov1.DeviceMaintenanceSessionRecovering) ||
		pod.Spec.NodeName != c.NodeName {
		return false, nil
	}
	if session.Operation.Namespace == "" || session.Operation.Name == "" {
		return false, fmt.Errorf("released managed drain session has no operation identity")
	}
	var leaf opsv1alpha1.IOSXESoftwareUpgrade
	if err := c.Client.Get(ctx, types.NamespacedName{
		Namespace: session.Operation.Namespace, Name: session.Operation.Name,
	}, &leaf); err != nil {
		return false, fmt.Errorf("read managed drain leaf for released completion: %w", err)
	}
	selected := false
	if leaf.Status.ManagerDrain != nil {
		for i := range leaf.Status.ManagerDrain.Pods {
			candidate := &leaf.Status.ManagerDrain.Pods[i]
			if candidate.Namespace == pod.Namespace && candidate.Name == pod.Name &&
				candidate.UID == string(pod.UID) {
				selected = true
				break
			}
		}
	}
	if !selected {
		return false, nil
	}
	if _, err := c.authorizeDrainDeleteWithRevisionRollover(
		ctx, pod, now, false, false, drainDeleteReleasedCompletion,
	); err != nil {
		return false, fmt.Errorf("authorize released managed drain completion: %w", err)
	}
	return true, nil
}

func (c *Coordinator) authorizeDrainDeleteWithRevisionRollover(
	ctx context.Context,
	requested *corev1.Pod,
	now time.Time,
	requireHeldLease bool,
	allowStaleRecoveryRevision bool,
	mode drainDeleteAuthorizationMode,
) (drainDeleteAuthority, error) {
	var pod corev1.Pod
	if err := c.Client.Get(ctx, types.NamespacedName{Namespace: requested.Namespace, Name: requested.Name}, &pod); err != nil {
		if mode == drainDeleteReleasedCompletion && apierrors.IsNotFound(err) {
			// ResolveDrainDeletePod already read this exact live UID uncached. If
			// it disappears between reads, retain that snapshot but still require
			// every durable DeviceClean and idle-Lease proof below. Disappearance
			// alone must never turn an incomplete teardown into completion.
			pod = *requested.DeepCopy()
		} else {
			return drainDeleteAuthority{}, fmt.Errorf("read managed drain Pod: %w", err)
		}
	}
	if pod.UID == "" || pod.UID != requested.UID || pod.Spec.NodeName != c.NodeName {
		return drainDeleteAuthority{}, fmt.Errorf("managed drain Pod does not bind the exact requested UID and Node")
	}

	var device ciskov1.CiscoDevice
	if err := c.Client.Get(ctx, types.NamespacedName{Namespace: c.Namespace, Name: c.DeviceName}, &device); err != nil {
		return drainDeleteAuthority{}, fmt.Errorf("read managed drain CiscoDevice: %w", err)
	}
	if device.UID == "" || string(device.UID) != c.DeviceUID ||
		(mode == drainDeleteDeviceTeardown && !device.DeletionTimestamp.IsZero()) {
		return drainDeleteAuthority{}, fmt.Errorf("managed drain CiscoDevice incarnation is missing, foreign, or terminating")
	}

	var node corev1.Node
	if err := c.Client.Get(ctx, types.NamespacedName{Name: c.NodeName}, &node); err != nil {
		return drainDeleteAuthority{}, fmt.Errorf("read managed drain Node: %w", err)
	}
	if err := c.validateManagedNode(&node); err != nil {
		return drainDeleteAuthority{}, err
	}
	if mode == drainDeleteDeviceTeardown {
		if err := validateDrainRuntimeBinding(&device, &node, c); err != nil {
			return drainDeleteAuthority{}, err
		}
	} else if err := validateDrainNodeIdentityBinding(&device, &node, c); err != nil {
		return drainDeleteAuthority{}, err
	}

	session := device.Status.MaintenanceSession
	if session == nil || session.ProtocolVersion != ciskov1.DeviceMaintenanceProtocolPDBDrainV1 ||
		session.Purpose != ciskov1.DeviceMaintenancePurposeWorkloadDrain ||
		(session.Phase != ciskov1.DeviceMaintenanceSessionActive &&
			session.Phase != ciskov1.DeviceMaintenanceSessionRecovering) {
		return drainDeleteAuthority{}, fmt.Errorf("managed drain has no active workload-drain maintenance session")
	}
	if session.AcknowledgedAt == nil || session.AcknowledgedAt.IsZero() || session.RequestedAt.IsZero() ||
		session.AcknowledgedAt.Before(&session.RequestedAt) || session.DeviceUID != c.DeviceUID ||
		session.NodeName != node.Name || session.NodeUID != string(node.UID) ||
		session.Operation.Namespace != c.Namespace || session.Operation.Name == "" || session.Operation.UID == "" {
		return drainDeleteAuthority{}, fmt.Errorf("managed drain maintenance session identity is incomplete or stale")
	}
	parsedToken, tokenErr := uuid.Parse(session.SessionToken)
	if tokenErr != nil || parsedToken.String() != session.SessionToken || parsedToken.Version() != 4 ||
		parsedToken.Variant() != uuid.RFC4122 {
		return drainDeleteAuthority{}, fmt.Errorf("managed drain maintenance session token is not a canonical UUIDv4")
	}

	var leaf opsv1alpha1.IOSXESoftwareUpgrade
	leafKey := types.NamespacedName{Namespace: session.Operation.Namespace, Name: session.Operation.Name}
	if err := c.Client.Get(ctx, leafKey, &leaf); err != nil {
		return drainDeleteAuthority{}, fmt.Errorf("read managed drain software-upgrade leaf: %w", err)
	}
	if string(leaf.UID) != session.Operation.UID ||
		(mode == drainDeleteDeviceTeardown && !leaf.DeletionTimestamp.IsZero()) ||
		leaf.Spec.DeviceRef.Name != c.DeviceName {
		return drainDeleteAuthority{}, fmt.Errorf("managed drain leaf does not bind the active operation and device")
	}
	drain, err := validateDrainLeafBindingWithRevisionRollover(
		&leaf, &device, &node, session, c.WorkerRevision,
		allowStaleRecoveryRevision || mode == drainDeleteReleasedCompletion,
		mode == drainDeleteDeviceTeardown,
	)
	if err != nil {
		return drainDeleteAuthority{}, err
	}
	if mode == drainDeleteDeviceTeardown {
		if session.Phase == ciskov1.DeviceMaintenanceSessionActive {
			if err := validateActiveDrainGuard(&node, drain); err != nil {
				return drainDeleteAuthority{}, err
			}
		} else if err := validateRestoredDrainGuard(&device, &node, drain); err != nil {
			return drainDeleteAuthority{}, err
		}
		if err := validateDrainTopologyLock(&device, &node, &leaf, drain); err != nil {
			return drainDeleteAuthority{}, err
		}
	}
	selected, err := drainPodSnapshot(drain.Pods, &pod)
	if err != nil {
		return drainDeleteAuthority{}, err
	}
	switch mode {
	case drainDeleteDeviceTeardown:
		if err := validateLiveDrainPodEligibility(&pod, selected); err != nil {
			return drainDeleteAuthority{}, err
		}
		if err := validateDrainPodProtection(&pod, session.SessionToken); err != nil {
			return drainDeleteAuthority{}, err
		}
		if selected.Phase == opsv1alpha1.UpgradeDrainPodProtected &&
			(pod.DeletionTimestamp == nil || pod.DeletionTimestamp.IsZero()) {
			return drainDeleteAuthority{}, fmt.Errorf("managed drain Pod has neither deletionTimestamp nor an accepted Eviction")
		}
	case drainDeleteReleasedCompletion:
		if err := validateReleasedDrainPodCompletion(&pod, selected); err != nil {
			return drainDeleteAuthority{}, err
		}
	default:
		return drainDeleteAuthority{}, fmt.Errorf("unknown managed drain delete authorization mode %d", mode)
	}
	leaseKey := types.NamespacedName{
		Namespace: c.LeaseNamespace,
		Name: engine.LeaseName(
			devicecoordination.DeviceKey(c.Namespace, c.DeviceName),
			devicecoordination.MutationLeaseFamily,
		),
	}
	var lease coordv1.Lease
	if err := c.Client.Get(ctx, leaseKey, &lease); err != nil {
		return drainDeleteAuthority{}, fmt.Errorf("read managed drain mutation Lease: %w", err)
	}
	holder := devicecoordination.HolderIdentity("software-drain", leaf.Namespace, leaf.Name, string(leaf.UID))
	if session.Lease.Namespace != lease.Namespace || session.Lease.Name != lease.Name ||
		session.Lease.UID != string(lease.UID) || session.Lease.Holder != holder {
		return drainDeleteAuthority{}, fmt.Errorf("managed drain maintenance session does not bind the canonical Lease")
	}
	if err := c.validateManagedLeaseBinding(&lease, &node); err != nil {
		return drainDeleteAuthority{}, err
	}
	if mode == drainDeleteReleasedCompletion {
		if requireHeldLease {
			return drainDeleteAuthority{}, fmt.Errorf("released managed drain completion cannot require a held Lease")
		}
		if err := validateIdleManagedMutationLease(&lease); err != nil {
			return drainDeleteAuthority{}, err
		}
	} else {
		switch selected.Phase {
		case opsv1alpha1.UpgradeDrainPodProtected,
			opsv1alpha1.UpgradeDrainPodEvictionRequested,
			opsv1alpha1.UpgradeDrainPodTerminationObserved:
		case opsv1alpha1.UpgradeDrainPodDeviceClean:
			// DeviceClean is the manager's durable acquisition fence. A callback
			// already holding this exact session Lease may finish or retry the same
			// idempotent teardown, but an idle or foreign Lease cannot be acquired
			// for new device work after final proof has started.
			if err := validateActiveManagedMutationLease(&lease, holder); err != nil {
				return drainDeleteAuthority{}, fmt.Errorf("DeviceClean fences new managed drain deletion: %w", err)
			}
			if hasManagedMaintenanceRequestAnnotations(lease.Annotations) {
				if err := validateDrainLeaseAnnotations(&lease, session, drain); err != nil {
					return drainDeleteAuthority{}, err
				}
			}
		default:
			return drainDeleteAuthority{}, fmt.Errorf("managed drain Pod phase %q does not authorize device teardown", selected.Phase)
		}
	}

	authorityDeadline := drain.DrainDeadline.Time
	if drain.State == opsv1alpha1.UpgradeManagerDrainRecovering {
		if drain.RecoveryDeadline == nil || drain.RecoveryDeadline.IsZero() {
			return drainDeleteAuthority{}, fmt.Errorf("managed drain recovery has no bounded deadline")
		}
		authorityDeadline = drain.RecoveryDeadline.Time
	}
	if mode == drainDeleteDeviceTeardown && !now.Before(authorityDeadline) {
		return drainDeleteAuthority{}, fmt.Errorf("managed drain delete authority has expired")
	}
	if requireHeldLease {
		if err := validateActiveManagedMutationLease(&lease, holder); err != nil {
			return drainDeleteAuthority{}, err
		}
		leaseDeadline := lease.Spec.RenewTime.Add(time.Duration(*lease.Spec.LeaseDurationSeconds) * time.Second)
		if !now.Before(leaseDeadline) {
			return drainDeleteAuthority{}, fmt.Errorf("managed drain mutation Lease has expired")
		}
		if err := validateDrainLeaseAnnotations(&lease, session, drain); err != nil {
			return drainDeleteAuthority{}, err
		}
		if leaseDeadline.Before(authorityDeadline) {
			authorityDeadline = leaseDeadline
		}
	}
	workerConfigRevision := ""
	if leaf.Status.WorkerControl != nil {
		workerConfigRevision = leaf.Status.WorkerControl.ObservedWorkerConfigRevision
	}
	return drainDeleteAuthority{
		deadline:             authorityDeadline,
		holder:               holder,
		podPhase:             selected.Phase,
		lease:                lease,
		session:              *session,
		drain:                *drain,
		workerConfigRevision: workerConfigRevision,
	}, nil
}

// validateDrainPreDispatchFence distinguishes a callback that was already
// authorized and dispatched before DeviceClean from one whose stale initial
// read merely raced that fence. Inventory/renewal revalidation intentionally
// remains allowed in DeviceClean, but a pre-fence initial read may never cross
// the status CAS into a new device mutation.
func validateDrainPreDispatchFence(initial, current drainDeleteAuthority) error {
	if current.podPhase != opsv1alpha1.UpgradeDrainPodDeviceClean {
		return nil
	}
	if initial.podPhase != opsv1alpha1.UpgradeDrainPodDeviceClean {
		return fmt.Errorf("managed drain entered DeviceClean before device dispatch")
	}
	if !sameDrainLeaseAcquisition(initial.lease, current.lease, current.holder) {
		return fmt.Errorf("managed drain DeviceClean retry no longer owns the retained Lease acquisition")
	}
	return nil
}

func sameDrainLeaseAcquisition(before, after coordv1.Lease, holder string) bool {
	if before.UID == "" || before.UID != after.UID ||
		before.Spec.HolderIdentity == nil || after.Spec.HolderIdentity == nil ||
		*before.Spec.HolderIdentity != holder || *after.Spec.HolderIdentity != holder ||
		before.Spec.AcquireTime == nil || after.Spec.AcquireTime == nil ||
		!before.Spec.AcquireTime.Equal(after.Spec.AcquireTime) ||
		before.Spec.LeaseTransitions == nil || after.Spec.LeaseTransitions == nil {
		return false
	}
	return *before.Spec.LeaseTransitions == *after.Spec.LeaseTransitions
}

// retireExpiredStaleDrainRecoveryLease is the sole revision-rollover escape
// hatch for a retained drain Lease. It cannot grant device access: it either
// leaves an unexpired quarantine untouched, or clears an expired exact holder
// and forces a later call to obtain a fresh current-revision manager session.
func (c *Coordinator) retireExpiredStaleDrainRecoveryLease(
	ctx context.Context,
	requested *corev1.Pod,
	now time.Time,
) (bool, error) {
	var (
		handled         bool
		retired         bool
		leaseDeadline   time.Time
		staleRevision   int64
		currentRevision int64
	)
	err := retry.RetryOnConflict(retry.DefaultBackoff, func() error {
		handled = false
		retired = false
		leaseDeadline = time.Time{}

		authority, err := c.authorizeDrainDeleteWithRevisionRollover(
			ctx, requested, now, false, true, drainDeleteDeviceTeardown,
		)
		if err != nil {
			return err
		}
		if authority.drain.State != opsv1alpha1.UpgradeManagerDrainRecovering ||
			authority.session.ControlRevision >= authority.drain.ControlRevision {
			return nil
		}
		handled = true
		staleRevision = authority.session.ControlRevision
		currentRevision = authority.drain.ControlRevision

		lease := authority.lease.DeepCopy()
		if lease.Spec.HolderIdentity == nil {
			if lease.Spec.AcquireTime != nil || lease.Spec.RenewTime != nil ||
				lease.Spec.LeaseDurationSeconds != nil || lease.Spec.LeaseTransitions == nil ||
				*lease.Spec.LeaseTransitions <= 0 || hasManagedMaintenanceRequestAnnotations(lease.Annotations) {
				return fmt.Errorf("stale managed drain Lease has malformed idle state")
			}
			return nil
		}
		if *lease.Spec.HolderIdentity != authority.holder {
			return fmt.Errorf("stale managed drain Lease is held by a different operation")
		}
		if err := validateActiveManagedMutationLease(lease, authority.holder); err != nil {
			return err
		}
		if *lease.Spec.LeaseDurationSeconds != int32(writeLeaseTTL/time.Second) {
			return fmt.Errorf("stale managed drain Lease has an unexpected quarantine duration")
		}

		// A process may fail immediately before or after publishing the request.
		// Wholly absent metadata proves no valid request was presented. Otherwise
		// require the complete request to bind the exact older session revision;
		// partial or foreign metadata must remain quarantined for investigation.
		if hasManagedMaintenanceRequestAnnotations(lease.Annotations) {
			staleDrain := authority.drain
			staleDrain.ControlRevision = authority.session.ControlRevision
			if err := validateDrainLeaseAnnotations(lease, &authority.session, &staleDrain); err != nil {
				return err
			}
		}

		leaseDeadline = lease.Spec.RenewTime.Add(
			time.Duration(*lease.Spec.LeaseDurationSeconds) * time.Second,
		)
		if now.Before(leaseDeadline) {
			return nil
		}

		before := lease.DeepCopy()
		lease.Spec.HolderIdentity = nil
		lease.Spec.AcquireTime = nil
		lease.Spec.RenewTime = nil
		lease.Spec.LeaseDurationSeconds = nil
		for annotation := range drainLeaseAnnotations(&authority.session, &authority.drain) {
			delete(lease.Annotations, annotation)
		}
		if err := c.Client.Patch(ctx, lease,
			client.MergeFromWithOptions(before, client.MergeFromWithOptimisticLock{})); err != nil {
			return err
		}
		retired = true
		return nil
	})
	if err != nil {
		if !handled {
			return false, nil
		}
		return true, fmt.Errorf("retire stale managed drain Lease: %w", err)
	}
	if !handled {
		return false, nil
	}
	if retired {
		return true, fmt.Errorf(
			"%w: retired expired stale drain Lease revision %d; waiting for fresh manager acknowledgement at revision %d",
			devicecoordination.ErrMutationIncomplete, staleRevision, currentRevision,
		)
	}
	if !leaseDeadline.IsZero() {
		return true, fmt.Errorf(
			"%w: stale drain Lease revision %d remains quarantined until %s; recovery revision %d may need bounded renewal while waiting",
			devicecoordination.ErrMutationIncomplete, staleRevision,
			leaseDeadline.UTC().Format(time.RFC3339Nano), currentRevision,
		)
	}
	return true, fmt.Errorf(
		"%w: stale drain Lease revision %d is idle; waiting for fresh manager acknowledgement at revision %d",
		devicecoordination.ErrMutationIncomplete, staleRevision, currentRevision,
	)
}

func validateDrainTopologyLock(
	device *ciskov1.CiscoDevice,
	node *corev1.Node,
	leaf *opsv1alpha1.IOSXESoftwareUpgrade,
	drain *opsv1alpha1.UpgradeManagerDrainStatus,
) error {
	if device == nil || node == nil || leaf == nil || drain == nil ||
		device.Status.TopologyLock == nil || leaf.Status.ManagerAdmission == nil {
		return fmt.Errorf("managed drain topology-lock identity is incomplete")
	}
	lock := device.Status.TopologyLock
	admission := leaf.Status.ManagerAdmission
	if lock.State != ciskov1.DeviceTopologyLockActive ||
		lock.PolicyEpoch != drain.PolicyEpoch || lock.AcquisitionID != admission.TopologyLockID ||
		lock.CampaignNamespace != leaf.Namespace || lock.CampaignUID != admission.CampaignUID ||
		lock.PlanHash != admission.PlanHash || lock.ReservationID != drain.ReservationID ||
		lock.DeviceUID != string(device.UID) || lock.DeviceGeneration != device.Generation ||
		lock.NodeUID != string(node.UID) || lock.ProjectionHash != device.Status.TopologyProjection.EffectiveLabelHash {
		return fmt.Errorf("managed drain topology-lock binding is stale or inconsistent")
	}
	return nil
}

func validateDrainRuntimeBinding(device *ciskov1.CiscoDevice, node *corev1.Node, c *Coordinator) error {
	if err := validateDrainNodeIdentityBinding(device, node, c); err != nil {
		return err
	}
	ready := meta.FindStatusCondition(device.Status.Conditions, ciskov1.CiscoDeviceConditionTopologyReady)
	if ready == nil || ready.Status != metav1.ConditionTrue || ready.ObservedGeneration < device.Generation ||
		device.Status.TopologyProjection == nil || device.Status.TopologyProjection.EffectiveLabelHash == "" ||
		device.Status.TopologyProjection.EffectiveLabelHash != node.Annotations[managedprotocol.AnnotationProjectionHash] {
		return fmt.Errorf("managed drain topology is not ready or its projection is not bound to the Node")
	}
	if err := validateManagedWorkerRuntimeBinding(device, c); err != nil {
		return err
	}
	for _, taint := range node.Spec.Taints {
		if taint.Key == managedprotocol.TopologyInitializingTaint && taint.Effect == corev1.TaintEffectNoSchedule {
			return fmt.Errorf("managed drain topology initialization guard remains active")
		}
	}
	return nil
}

func validateDrainNodeIdentityBinding(device *ciskov1.CiscoDevice, node *corev1.Node, c *Coordinator) error {
	if device == nil || node == nil || c == nil {
		return fmt.Errorf("managed drain CiscoDevice/Node identity is incomplete")
	}
	identity := device.Status.NodeIdentity
	if identity == nil || identity.DeviceUID != c.DeviceUID || identity.NodeName != node.Name ||
		identity.NodeUID != string(node.UID) || identity.PhysicalIdentity == "" {
		return fmt.Errorf("managed drain CiscoDevice/Node identity is not manager-bound")
	}
	return nil
}

func validateManagedWorkerRuntimeBinding(device *ciskov1.CiscoDevice, c *Coordinator) error {
	if device == nil || c == nil {
		return fmt.Errorf("managed worker runtime binding is incomplete")
	}
	worker := device.Status.WorkerRevision
	if worker == nil || c.WorkerRevision == "" || c.WorkerPodUID == "" ||
		worker.DesiredRevision != c.WorkerRevision || worker.ObservedRevision != c.WorkerRevision ||
		worker.DeploymentUID == "" || worker.DeploymentGeneration < 1 || worker.PodUID != c.WorkerPodUID ||
		worker.PodStartTime == nil || worker.PodStartTime.IsZero() ||
		worker.ReadyHeartbeatTime == nil || worker.ReadyHeartbeatTime.IsZero() ||
		worker.ReadyHeartbeatTime.Before(worker.PodStartTime) {
		return fmt.Errorf("managed worker revision and Pod incarnation are not manager-authenticated and ready")
	}
	return nil
}

func validateActiveDrainGuard(node *corev1.Node, drain *opsv1alpha1.UpgradeManagerDrainStatus) error {
	if node == nil || drain == nil || !node.Spec.Unschedulable || !hasMaintenanceTaint(node.Spec.Taints) {
		return fmt.Errorf("managed drain Node has no exact active scheduling guard")
	}
	cordonOwner := node.Annotations[managedprotocol.AnnotationDrainCordonOwner]
	if (!drain.NodeUnschedulableBefore && cordonOwner != drain.SessionToken) ||
		(drain.NodeUnschedulableBefore && cordonOwner != "") {
		return fmt.Errorf("managed drain Node cordon ownership does not match the active session")
	}
	taintOwner := node.Annotations[managedprotocol.AnnotationDrainTaintOwner]
	if (!drain.MaintenanceTaintPresentBefore && taintOwner != drain.SessionToken) ||
		(drain.MaintenanceTaintPresentBefore && taintOwner != "") {
		return fmt.Errorf("managed drain Node maintenance-taint ownership does not match the active session")
	}
	return nil
}

func validateRestoredDrainGuard(
	device *ciskov1.CiscoDevice,
	node *corev1.Node,
	drain *opsv1alpha1.UpgradeManagerDrainStatus,
) error {
	if device == nil || node == nil || drain == nil {
		return fmt.Errorf("managed drain recovery guard identity is incomplete")
	}
	if node.Annotations[managedprotocol.AnnotationDrainCordonOwner] != "" ||
		node.Annotations[managedprotocol.AnnotationDrainTaintOwner] != "" {
		return fmt.Errorf("managed recovery scheduling guard is still session-owned")
	}
	hold := device.Annotations[managedprotocol.AnnotationDrainCordonHold] == "true"
	if drain.NodeUnschedulableBefore || hold {
		if !node.Spec.Unschedulable {
			return fmt.Errorf("managed recovery did not preserve the operator-owned cordon")
		}
	} else if node.Spec.Unschedulable {
		return fmt.Errorf("managed recovery session-owned cordon remains applied")
	}
	if hasMaintenanceTaint(node.Spec.Taints) != drain.MaintenanceTaintPresentBefore {
		return fmt.Errorf("managed recovery session-owned maintenance taint is not restored")
	}
	return nil
}

func validateDrainLeafBinding(
	leaf *opsv1alpha1.IOSXESoftwareUpgrade,
	device *ciskov1.CiscoDevice,
	node *corev1.Node,
	session *ciskov1.DeviceMaintenanceSessionStatus,
	workerRevision string,
) (*opsv1alpha1.UpgradeManagerDrainStatus, error) {
	return validateDrainLeafBindingWithRevisionRollover(leaf, device, node, session, workerRevision, false, true)
}

func validateDrainLeafBindingWithRevisionRollover(
	leaf *opsv1alpha1.IOSXESoftwareUpgrade,
	device *ciskov1.CiscoDevice,
	node *corev1.Node,
	session *ciskov1.DeviceMaintenanceSessionStatus,
	workerRevision string,
	allowStaleRecoveryRevision bool,
	requireCurrentWorker bool,
) (*opsv1alpha1.UpgradeManagerDrainStatus, error) {
	admission := leaf.Status.ManagerAdmission
	control := leaf.Status.ManagerControl
	worker := leaf.Status.WorkerControl
	drain := leaf.Status.ManagerDrain
	if admission == nil || control == nil || drain == nil || (requireCurrentWorker && worker == nil) {
		return nil, fmt.Errorf("managed drain leaf authority is incomplete")
	}
	if drain.ProtocolVersion != opsv1alpha1.ManagedDrainProtocolPDBV1 ||
		(drain.State != opsv1alpha1.UpgradeManagerDrainEvicting &&
			drain.State != opsv1alpha1.UpgradeManagerDrainRecovering) {
		return nil, fmt.Errorf("managed drain state %q does not authorize Pod teardown", drain.State)
	}
	recovering := drain.State == opsv1alpha1.UpgradeManagerDrainRecovering
	staleRecoveryRevision := allowStaleRecoveryRevision && recovering &&
		session.ControlRevision < control.Revision
	if (!recovering && admission.State != opsv1alpha1.UpgradeManagerAdmissionPending &&
		admission.State != opsv1alpha1.UpgradeManagerAdmissionGranted) ||
		(recovering && admission.State != opsv1alpha1.UpgradeManagerAdmissionPending &&
			admission.State != opsv1alpha1.UpgradeManagerAdmissionGranted &&
			admission.State != opsv1alpha1.UpgradeManagerAdmissionRevoked) {
		return nil, fmt.Errorf("managed drain leaf admission state %q is not authorized", admission.State)
	}
	if admission.ProtocolVersion != opsv1alpha1.ManagedUpgradeProtocolRolloutV1 ||
		(!recovering && admission.RevocationReason != "") ||
		(admission.State == opsv1alpha1.UpgradeManagerAdmissionRevoked) != (admission.RevocationReason != "") ||
		admission.ControlRevision == nil ||
		admission.CampaignUID == "" || admission.PlanHash == "" || admission.PolicyUID == "" ||
		admission.PolicyResourceVersion == "" || admission.PolicyEpoch < 1 || admission.LedgerUID == "" ||
		admission.ReservationID == "" || admission.TopologyLockID == "" ||
		admission.LeafUID != string(leaf.UID) || admission.DeviceUID != string(device.UID) ||
		admission.DeviceGeneration != device.Generation ||
		admission.PhysicalIdentity != device.Status.NodeIdentity.PhysicalIdentity ||
		admission.NodeUID != string(node.UID) {
		return nil, fmt.Errorf("managed drain admission identity and policy binding is incomplete or stale")
	}
	if control.Revision != *admission.ControlRevision ||
		(session.ControlRevision != control.Revision && !staleRecoveryRevision) {
		return nil, fmt.Errorf("managed drain leaf control or worker acknowledgement is incomplete or stale")
	}
	if requireCurrentWorker && (worker.ObservedPolicyEpoch != admission.PolicyEpoch ||
		worker.ObservedControlRevision > control.Revision || workerRevision == "" ||
		worker.ObservedWorkerConfigRevision != workerRevision ||
		worker.ObservedWorkerConfigRevision != node.Annotations[managedprotocol.AnnotationWorkerObservedRevision]) {
		return nil, fmt.Errorf("managed drain leaf control or worker acknowledgement is incomplete or stale")
	}
	if requireCurrentWorker && !recovering {
		if control.Pause || control.Cancel || worker.ObservedAdmissionState != admission.State ||
			worker.ObservedControlRevision != control.Revision ||
			(worker.EffectiveState != opsv1alpha1.UpgradeWorkerControlReady &&
				worker.EffectiveState != opsv1alpha1.UpgradeWorkerControlClaimed) {
			return nil, fmt.Errorf("managed drain leaf is paused, cancelled, or lacks an exact worker acknowledgement")
		}
	}
	if drain.SessionToken != session.SessionToken || drain.ReservationID != admission.ReservationID ||
		drain.PolicyEpoch != admission.PolicyEpoch || drain.ControlRevision != control.Revision ||
		drain.NodeUID != string(node.UID) || drain.StartedAt.IsZero() ||
		!session.RequestedAt.Equal(&drain.StartedAt) || drain.DrainDeadline.IsZero() ||
		drain.UpdatedAt.IsZero() || !drain.DrainDeadline.After(drain.StartedAt.Time) ||
		drain.UpdatedAt.Before(&drain.StartedAt) {
		return nil, fmt.Errorf("managed drain authority does not match the maintenance session and leaf grant")
	}
	drainDuration := drain.DrainDeadline.Sub(drain.StartedAt.Time)
	if drainDuration > maxManagedDrainDuration {
		return nil, fmt.Errorf("managed drain duration exceeds the protocol maximum")
	}
	if recovering {
		if drain.RecoveryDeadline == nil || !drain.RecoveryDeadline.After(drain.StartedAt.Time) ||
			drain.RecoveryDeadline.After(drain.UpdatedAt.Add(drainDuration+drainRecoveryExtraLimit)) {
			return nil, fmt.Errorf("managed drain recovery deadline exceeds its bounded renewal window")
		}
	} else if drain.RecoveryDeadline != nil {
		return nil, fmt.Errorf("managed drain recovery deadline is present outside recovery")
	}
	if !staleRecoveryRevision &&
		(drain.State == opsv1alpha1.UpgradeManagerDrainEvicting) !=
			(session.Phase == ciskov1.DeviceMaintenanceSessionActive) {
		return nil, fmt.Errorf("managed drain and maintenance-session phases disagree")
	}

	expectedAnnotations := map[string]string{
		managedprotocol.AnnotationManaged:          "true",
		managedprotocol.AnnotationDeviceNamespace:  device.Namespace,
		managedprotocol.AnnotationDeviceName:       device.Name,
		managedprotocol.AnnotationDeviceUID:        string(device.UID),
		managedprotocol.AnnotationDeviceGeneration: strconv.FormatInt(device.Generation, 10),
		managedprotocol.AnnotationNodeName:         node.Name,
		managedprotocol.AnnotationNodeUID:          string(node.UID),
		managedprotocol.AnnotationWorkerUsername:   node.Annotations[managedprotocol.AnnotationWorkerUsername],
		managedprotocol.AnnotationWorkerProtocol:   managedprotocol.Version,
		managedprotocol.AnnotationCampaignUID:      admission.CampaignUID,
		managedprotocol.AnnotationPlanHash:         admission.PlanHash,
		managedprotocol.AnnotationLedgerUID:        admission.LedgerUID,
		managedprotocol.AnnotationReservationID:    admission.ReservationID,
	}
	for key, want := range expectedAnnotations {
		if want == "" || leaf.Annotations[key] != want {
			return nil, fmt.Errorf("managed drain leaf annotation %s is missing or inconsistent", key)
		}
	}
	return drain, nil
}

func authorizedDrainPod(
	pods []opsv1alpha1.UpgradeDrainPodStatus,
	pod *corev1.Pod,
	sessionToken string,
) (*opsv1alpha1.UpgradeDrainPodStatus, error) {
	selected, err := drainPodSnapshot(pods, pod)
	if err != nil {
		return nil, err
	}
	if err := validateLiveDrainPodEligibility(pod, selected); err != nil {
		return nil, err
	}
	if err := validateDrainPodProtection(pod, sessionToken); err != nil {
		return nil, err
	}
	return selected, nil
}

// drainPodSnapshot validates the immutable, exact-UID manager snapshot. The
// completion acknowledgement deliberately does not reapply mutable live
// eligibility after device-clean evidence has been durably accepted.
func drainPodSnapshot(
	pods []opsv1alpha1.UpgradeDrainPodStatus,
	pod *corev1.Pod,
) (*opsv1alpha1.UpgradeDrainPodStatus, error) {
	seenUIDs := make(map[string]struct{}, len(pods))
	seenNames := make(map[string]struct{}, len(pods))
	var selected *opsv1alpha1.UpgradeDrainPodStatus
	for i := range pods {
		candidate := &pods[i]
		key := candidate.Namespace + "\x00" + candidate.Name
		if candidate.Namespace == "" || candidate.Name == "" || candidate.UID == "" {
			return nil, fmt.Errorf("managed drain Pod snapshot contains an incomplete identity")
		}
		if _, duplicate := seenUIDs[candidate.UID]; duplicate {
			return nil, fmt.Errorf("managed drain Pod snapshot contains duplicate UIDs")
		}
		if _, duplicate := seenNames[key]; duplicate {
			return nil, fmt.Errorf("managed drain Pod snapshot contains duplicate names")
		}
		seenUIDs[candidate.UID] = struct{}{}
		seenNames[key] = struct{}{}
		if candidate.Namespace == pod.Namespace && candidate.Name == pod.Name && candidate.UID == string(pod.UID) {
			selected = candidate
		}
	}
	if selected == nil {
		return nil, fmt.Errorf("managed drain Pod is not in the exact UID snapshot")
	}
	if err := workloaddrain.VerifyEligibilityHash(selected); err != nil {
		return nil, fmt.Errorf("managed drain Pod eligibility evidence is invalid: %w", err)
	}
	return selected, nil
}

func validateLiveDrainPodEligibility(
	pod *corev1.Pod,
	selected *opsv1alpha1.UpgradeDrainPodStatus,
) error {
	if pod == nil || selected == nil || pod.Labels["operations.cisco.vk/drain-safe"] != "true" {
		return fmt.Errorf("managed drain Pod is not explicitly marked drain-safe")
	}
	controller := metav1.GetControllerOf(pod)
	if controller == nil || selected.Controller.Namespace != pod.Namespace ||
		selected.Controller.APIVersion != controller.APIVersion || selected.Controller.Kind != controller.Kind ||
		selected.Controller.Name != controller.Name || selected.Controller.UID != string(controller.UID) ||
		selected.Controller.Generation < 1 || len(selected.PDBs) == 0 ||
		selected.TerminationGracePeriodSeconds < 1 {
		return fmt.Errorf("managed drain Pod eligibility snapshot no longer matches its controlling owner")
	}
	return nil
}

func validateDrainPodProtection(pod *corev1.Pod, sessionToken string) error {
	if pod == nil || pod.Annotations[managedprotocol.AnnotationDrainSession] != sessionToken {
		return fmt.Errorf("managed drain Pod session annotation does not match")
	}
	finalizers := 0
	for _, finalizer := range pod.Finalizers {
		if finalizer == managedprotocol.DrainPodFinalizer {
			finalizers++
		}
	}
	if finalizers != 1 {
		return fmt.Errorf("managed drain Pod does not have one exact manager finalizer")
	}
	return nil
}

func validateReleasedDrainPodCompletion(
	pod *corev1.Pod,
	selected *opsv1alpha1.UpgradeDrainPodStatus,
) error {
	if pod == nil || selected == nil {
		return fmt.Errorf("released managed drain completion evidence is incomplete")
	}
	if pod.DeletionTimestamp == nil || pod.DeletionTimestamp.IsZero() {
		return fmt.Errorf("released managed drain Pod is not terminating")
	}
	if _, present := pod.Annotations[managedprotocol.AnnotationDrainSession]; present {
		return fmt.Errorf("released managed drain Pod retains its session annotation")
	}
	for _, finalizer := range pod.Finalizers {
		if finalizer == managedprotocol.DrainPodFinalizer {
			return fmt.Errorf("released managed drain Pod retains its manager finalizer")
		}
	}
	if selected.Phase != opsv1alpha1.UpgradeDrainPodDeviceClean &&
		selected.Phase != opsv1alpha1.UpgradeDrainPodReleased {
		return fmt.Errorf("managed drain Pod phase %q is not device-clean completion", selected.Phase)
	}
	if selected.ProtectedAt == nil || selected.ProtectedAt.IsZero() ||
		selected.EvictionRequestedAt == nil || selected.EvictionRequestedAt.IsZero() ||
		selected.DeletionObservedAt == nil || selected.DeletionObservedAt.IsZero() ||
		selected.DeviceCleanAt == nil || selected.DeviceCleanAt.IsZero() ||
		selected.EvictionRequestedAt.Before(selected.ProtectedAt) ||
		selected.DeletionObservedAt.Before(selected.EvictionRequestedAt) ||
		selected.DeviceCleanAt.Before(selected.DeletionObservedAt) ||
		selected.DeviceCleanInventoryRevision < 1 ||
		selected.DeviceCleanInventoryRevision <= selected.DeletionObservedInventoryRevision {
		return fmt.Errorf("released managed drain Pod lacks ordered accepted-eviction and device-clean evidence")
	}
	if selected.Phase == opsv1alpha1.UpgradeDrainPodReleased &&
		(selected.ReleasedAt == nil || selected.ReleasedAt.IsZero() ||
			selected.ReleasedAt.Before(selected.DeviceCleanAt)) {
		return fmt.Errorf("released managed drain Pod lacks ordered release evidence")
	}
	return nil
}

func validateIdleManagedMutationLease(lease *coordv1.Lease) error {
	if lease == nil || (lease.Spec.HolderIdentity != nil && *lease.Spec.HolderIdentity != "") ||
		lease.Spec.AcquireTime != nil ||
		lease.Spec.RenewTime != nil || lease.Spec.LeaseDurationSeconds != nil ||
		lease.Spec.Strategy != nil || lease.Spec.PreferredHolder != nil ||
		(lease.Spec.LeaseTransitions != nil && *lease.Spec.LeaseTransitions < 0) {
		return fmt.Errorf("released managed drain completion requires a wholly idle canonical Lease")
	}
	if hasManagedMaintenanceRequestAnnotations(lease.Annotations) {
		return fmt.Errorf("released managed drain completion Lease retains maintenance request metadata")
	}
	return nil
}

func (c *Coordinator) publishDrainLeaseRequest(ctx context.Context, authority drainDeleteAuthority) error {
	key := types.NamespacedName{
		Namespace: c.LeaseNamespace,
		Name: engine.LeaseName(
			devicecoordination.DeviceKey(c.Namespace, c.DeviceName),
			devicecoordination.MutationLeaseFamily,
		),
	}
	err := retry.RetryOnConflict(retry.DefaultBackoff, func() error {
		var lease coordv1.Lease
		if err := c.Client.Get(ctx, key, &lease); err != nil {
			return err
		}
		if string(lease.UID) != authority.session.Lease.UID ||
			authority.session.Lease.Namespace != lease.Namespace || authority.session.Lease.Name != lease.Name ||
			authority.session.Lease.Holder != authority.holder {
			return fmt.Errorf("managed drain Lease incarnation no longer matches the session")
		}
		if err := validateActiveManagedMutationLease(&lease, authority.holder); err != nil {
			return err
		}
		if hasManagedMaintenanceRequestAnnotations(lease.Annotations) {
			if err := validateDrainLeaseAnnotations(&lease, &authority.session, &authority.drain); err != nil {
				return err
			}
			return nil
		}
		before := lease.DeepCopy()
		if lease.Annotations == nil {
			lease.Annotations = map[string]string{}
		}
		for key, value := range drainLeaseAnnotations(&authority.session, &authority.drain) {
			lease.Annotations[key] = value
		}
		return c.Client.Patch(ctx, &lease,
			client.MergeFromWithOptions(before, client.MergeFromWithOptimisticLock{}))
	})
	if err != nil {
		return fmt.Errorf("publish managed drain Lease request: %w", err)
	}
	return nil
}

func validateDrainLeaseAnnotations(
	lease *coordv1.Lease,
	session *ciskov1.DeviceMaintenanceSessionStatus,
	drain *opsv1alpha1.UpgradeManagerDrainStatus,
) error {
	for key, want := range drainLeaseAnnotations(session, drain) {
		if lease.Annotations[key] != want {
			return fmt.Errorf("managed drain mutation Lease annotation %s does not match the session", key)
		}
	}
	return nil
}

func drainLeaseAnnotations(
	session *ciskov1.DeviceMaintenanceSessionStatus,
	drain *opsv1alpha1.UpgradeManagerDrainStatus,
) map[string]string {
	return map[string]string{
		managedprotocol.AnnotationMaintenanceRequestVersion:  managedprotocol.DrainProtocolVersion,
		managedprotocol.AnnotationMaintenanceSessionToken:    session.SessionToken,
		managedprotocol.AnnotationMaintenanceRequestedAt:     session.RequestedAt.Time.UTC().Format(time.RFC3339Nano),
		managedprotocol.AnnotationMaintenanceOperationNS:     session.Operation.Namespace,
		managedprotocol.AnnotationMaintenanceOperationName:   session.Operation.Name,
		managedprotocol.AnnotationMaintenanceOperationUID:    session.Operation.UID,
		managedprotocol.AnnotationMaintenanceControlRevision: strconv.FormatInt(drain.ControlRevision, 10),
		managedprotocol.AnnotationMaintenancePurpose:         managedprotocol.MaintenancePurposeWorkloadDrain,
	}
}
