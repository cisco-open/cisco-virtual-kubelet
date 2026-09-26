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
	"crypto/sha256"
	"encoding/binary"
	"errors"
	"fmt"
	"math"
	"sort"
	"strconv"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/apimachinery/pkg/util/validation"
	"k8s.io/client-go/util/retry"
	"sigs.k8s.io/controller-runtime/pkg/client"

	opsv1alpha1 "github.com/cisco/virtual-kubelet-cisco/api/ops/v1alpha1"
	"github.com/cisco/virtual-kubelet-cisco/internal/workloaddrain"
)

// ErrDrainInventoryUnproven means the device-derived inventory could not prove
// that the exact Pod UID was removed. The caller must retain the mutation
// quarantine and retry observation; neither DeletePod success nor a Kubernetes
// deletionTimestamp is evidence of device cleanup.
var ErrDrainInventoryUnproven = errors.New("managed drain device cleanup is not proven")

type drainInventoryObservation struct {
	complete              bool
	remainingSelectedUIDs []string
	foreignCount          int32
	unknownCount          int32
	targetPresent         bool
}

// VerifyAndPublishDrainInventory performs one fresh Driver.ListPods-equivalent
// scan while the exact drain Lease remains held, then publishes only the
// worker-owned inventory acknowledgement. Authority is re-read after the scan
// so an observation can never be attached to a changed session, policy,
// control revision, worker configuration, or selected Pod snapshot.
func (c *Coordinator) VerifyAndPublishDrainInventory(
	ctx context.Context,
	pod *corev1.Pod,
	scan func(context.Context) ([]*corev1.Pod, error),
) error {
	if c == nil || c.Client == nil || pod == nil || pod.Namespace == "" || pod.Name == "" || pod.UID == "" {
		return fmt.Errorf("%w: inventory coordinator or Pod identity is incomplete", ErrDrainInventoryUnproven)
	}
	if scan == nil {
		return fmt.Errorf("%w: inventory scanner is nil", ErrDrainInventoryUnproven)
	}
	initial, err := c.authorizeDrainDelete(ctx, pod, time.Now(), true)
	if err != nil {
		return fmt.Errorf("authorize managed drain inventory scan: %w", err)
	}
	if err := drainInventoryPodReady(initial.drain.Pods, pod, initial.drain.SessionToken); err != nil {
		return fmt.Errorf("%w: %v", ErrDrainInventoryUnproven, err)
	}
	initialSelection, err := drainSelectionDigest(initial.drain.Pods)
	if err != nil {
		return fmt.Errorf("%w: %v", ErrDrainInventoryUnproven, err)
	}

	devicePods, scanErr := scan(ctx)
	observation := inspectDrainInventory(devicePods, initial.drain.Pods, string(pod.UID), scanErr)

	latest, err := c.authorizeDrainDelete(ctx, pod, time.Now(), true)
	if err != nil {
		return fmt.Errorf("revalidate managed drain inventory authority: %w", err)
	}
	if err := drainInventoryPodReady(latest.drain.Pods, pod, latest.drain.SessionToken); err != nil {
		return fmt.Errorf("%w: %v", ErrDrainInventoryUnproven, err)
	}
	latestSelection, err := drainSelectionDigest(latest.drain.Pods)
	if err != nil {
		return fmt.Errorf("%w: %v", ErrDrainInventoryUnproven, err)
	}
	if err := sameDrainInventoryAuthority(initial, latest, initialSelection, latestSelection); err != nil {
		return fmt.Errorf("%w: %v", ErrDrainInventoryUnproven, err)
	}
	if err := c.publishDrainInventoryStatus(ctx, pod, latest, latestSelection, observation); err != nil {
		return err
	}

	var proofErrors []error
	if scanErr != nil {
		proofErrors = append(proofErrors, fmt.Errorf("device inventory scan failed: %w", scanErr))
	}
	if !observation.complete {
		proofErrors = append(proofErrors, errors.New("device inventory is partial or ambiguous"))
	}
	if observation.foreignCount != 0 && latest.drain.State != opsv1alpha1.UpgradeManagerDrainRecovering {
		proofErrors = append(proofErrors, fmt.Errorf(
			"device inventory contains %d non-selected workload(s)", observation.foreignCount,
		))
	}
	if observation.targetPresent {
		proofErrors = append(proofErrors, fmt.Errorf("selected Pod UID %q remains on the device", pod.UID))
	}
	if len(proofErrors) != 0 {
		return fmt.Errorf("%w: %v", ErrDrainInventoryUnproven, errors.Join(proofErrors...))
	}
	return nil
}

func drainInventoryPodReady(
	pods []opsv1alpha1.UpgradeDrainPodStatus,
	pod *corev1.Pod,
	sessionToken string,
) error {
	selected, err := authorizedDrainPod(pods, pod, sessionToken)
	if err != nil {
		return err
	}
	if (selected.Phase != opsv1alpha1.UpgradeDrainPodTerminationObserved &&
		selected.Phase != opsv1alpha1.UpgradeDrainPodDeviceClean) ||
		selected.DeletionObservedAt == nil || selected.DeletionObservedAt.IsZero() {
		return errors.New("manager has not durably recorded termination for the selected Pod UID")
	}
	return nil
}

func sameDrainInventoryAuthority(
	before, after drainDeleteAuthority,
	beforeSelection, afterSelection string,
) error {
	if before.holder != after.holder ||
		before.session.Operation != after.session.Operation ||
		before.session.SessionToken != after.session.SessionToken ||
		before.session.DeviceUID != after.session.DeviceUID ||
		before.session.NodeUID != after.session.NodeUID ||
		before.drain.ProtocolVersion != after.drain.ProtocolVersion ||
		before.drain.SessionToken != after.drain.SessionToken ||
		before.drain.ReservationID != after.drain.ReservationID ||
		before.drain.PolicyEpoch != after.drain.PolicyEpoch ||
		before.drain.ControlRevision != after.drain.ControlRevision ||
		before.drain.NodeUID != after.drain.NodeUID ||
		before.workerConfigRevision != after.workerConfigRevision ||
		before.networkWorkerRevision != after.networkWorkerRevision ||
		beforeSelection != afterSelection {
		return errors.New("managed drain authority changed during the device inventory scan")
	}
	return nil
}

func (c *Coordinator) publishDrainInventoryStatus(
	ctx context.Context,
	pod *corev1.Pod,
	authority drainDeleteAuthority,
	selectionDigest string,
	observation drainInventoryObservation,
) error {
	key := types.NamespacedName{
		Namespace: authority.session.Operation.Namespace,
		Name:      authority.session.Operation.Name,
	}
	err := retry.RetryOnConflict(retry.DefaultBackoff, func() error {
		fresh, err := c.authorizeDrainDelete(ctx, pod, time.Now(), true)
		if err != nil {
			return err
		}
		freshSelection, err := drainSelectionDigest(fresh.drain.Pods)
		if err != nil {
			return err
		}
		if err := sameDrainInventoryAuthority(authority, fresh, selectionDigest, freshSelection); err != nil {
			return err
		}
		var current opsv1alpha1.IOSXESoftwareUpgrade
		if err := c.Client.Get(ctx, key, &current); err != nil {
			return err
		}
		if err := validateDrainInventoryStatusBinding(&current, pod, fresh, freshSelection); err != nil {
			return err
		}

		before := current.DeepCopy()
		revision := int64(1)
		// metav1.Time is serialized at whole-second precision. Generate an API-
		// visible monotonic timestamp rather than relying on sub-second progress
		// that can collapse to equality after the status round trip.
		observedAt := time.Now().UTC().Truncate(time.Second)
		if previous := current.Status.WorkerDrain; previous != nil {
			if previous.ProtocolVersion != fresh.drain.ProtocolVersion ||
				previous.ObservedSessionToken != fresh.drain.SessionToken ||
				previous.ObservedPolicyEpoch != fresh.drain.PolicyEpoch ||
				previous.ObservedControlRevision > fresh.drain.ControlRevision ||
				previous.InventoryRevision < 1 || previous.InventoryRevision == math.MaxInt64 ||
				previous.InventoryObservedAt.IsZero() || previous.UpdatedAt.IsZero() ||
				previous.ForeignDeviceWorkloadCount < 0 || previous.UnknownDeviceWorkloadCount < 0 {
				return errors.New("existing worker drain inventory has foreign, stale, or exhausted identity")
			}
			revision = previous.InventoryRevision + 1
			if !observedAt.After(previous.InventoryObservedAt.Time) {
				observedAt = previous.InventoryObservedAt.Time.Truncate(time.Second).Add(time.Second)
			}
			if !observedAt.After(previous.UpdatedAt.Time) {
				observedAt = previous.UpdatedAt.Time.Truncate(time.Second).Add(time.Second)
			}
		}
		recovering := fresh.drain.State == opsv1alpha1.UpgradeManagerDrainRecovering
		current.Status.WorkerDrain = &opsv1alpha1.UpgradeWorkerDrainStatus{
			ProtocolVersion:              fresh.drain.ProtocolVersion,
			ObservedSessionToken:         fresh.drain.SessionToken,
			ObservedPolicyEpoch:          fresh.drain.PolicyEpoch,
			ObservedControlRevision:      fresh.drain.ControlRevision,
			ObservedWorkerConfigRevision: fresh.workerConfigRevision,
			ObservedWorkerPodUID:         c.WorkerPodUID,
			InventoryRevision:            revision,
			InventoryObservedAt:          metav1.NewTime(observedAt),
			InventoryComplete:            observation.complete,
			RemainingAuthorizedPodUIDs:   append([]string(nil), observation.remainingSelectedUIDs...),
			ForeignDeviceWorkloadCount:   observation.foreignCount,
			UnknownDeviceWorkloadCount:   observation.unknownCount,
			UpdatedAt:                    metav1.NewTime(observedAt),
			Reason:                       drainInventoryReason(observation, recovering),
			Message:                      drainInventoryMessage(observation, recovering),
		}
		return c.Client.Status().Patch(
			ctx,
			&current,
			client.MergeFromWithOptions(before, client.MergeFromWithOptimisticLock{}),
		)
	})
	if err != nil {
		return fmt.Errorf("publish managed drain device inventory: %w", err)
	}
	return nil
}

func validateDrainInventoryStatusBinding(
	leaf *opsv1alpha1.IOSXESoftwareUpgrade,
	pod *corev1.Pod,
	authority drainDeleteAuthority,
	selectionDigest string,
) error {
	if leaf == nil || pod == nil || string(leaf.UID) != authority.session.Operation.UID ||
		leaf.Namespace != authority.session.Operation.Namespace || leaf.Name != authority.session.Operation.Name ||
		leaf.DeletionTimestamp != nil || leaf.Status.ManagerAdmission == nil ||
		leaf.Status.ManagerControl == nil || leaf.Status.WorkerControl == nil || leaf.Status.ManagerDrain == nil {
		return errors.New("managed drain leaf no longer has the exact inventory authority")
	}
	admission := leaf.Status.ManagerAdmission
	control := leaf.Status.ManagerControl
	worker := leaf.Status.WorkerControl
	drain := leaf.Status.ManagerDrain
	currentSelection, err := drainSelectionDigest(drain.Pods)
	if err != nil {
		return err
	}
	if admission.ReservationID != authority.drain.ReservationID ||
		admission.PolicyEpoch != authority.drain.PolicyEpoch || admission.ControlRevision == nil ||
		*admission.ControlRevision != authority.drain.ControlRevision ||
		control.Revision != authority.drain.ControlRevision ||
		worker.ObservedPolicyEpoch != authority.drain.PolicyEpoch ||
		worker.ObservedControlRevision != authority.drain.ControlRevision ||
		worker.ObservedWorkerConfigRevision != authority.networkWorkerRevision ||
		drain.ProtocolVersion != authority.drain.ProtocolVersion ||
		drain.SessionToken != authority.drain.SessionToken || drain.ReservationID != authority.drain.ReservationID ||
		drain.PolicyEpoch != authority.drain.PolicyEpoch || drain.ControlRevision != authority.drain.ControlRevision ||
		drain.NodeUID != authority.drain.NodeUID || currentSelection != selectionDigest {
		return errors.New("managed drain inventory binding is stale or inconsistent")
	}
	selected, err := authorizedDrainPod(drain.Pods, pod, drain.SessionToken)
	if err != nil {
		return err
	}
	if (selected.Phase != opsv1alpha1.UpgradeDrainPodTerminationObserved &&
		selected.Phase != opsv1alpha1.UpgradeDrainPodDeviceClean) ||
		selected.DeletionObservedAt == nil || selected.DeletionObservedAt.IsZero() {
		return fmt.Errorf("managed drain Pod phase %q no longer accepts inventory evidence", selected.Phase)
	}
	return nil
}

func drainSelectionDigest(pods []opsv1alpha1.UpgradeDrainPodStatus) (string, error) {
	if len(pods) == 0 || len(pods) > 32 {
		return "", errors.New("manager drain selection is empty or exceeds the bounded Pod limit")
	}
	records := make([]string, 0, len(pods))
	seenUIDs := make(map[string]struct{}, len(pods))
	seenNames := make(map[string]struct{}, len(pods))
	for i := range pods {
		pod := &pods[i]
		if pod.Namespace == "" || pod.Name == "" || pod.UID == "" {
			return "", errors.New("manager drain selection contains an incomplete identity")
		}
		if err := workloaddrain.VerifyEligibilityHash(pod); err != nil {
			return "", fmt.Errorf("manager drain selection contains invalid eligibility evidence: %w", err)
		}
		if _, exists := seenUIDs[pod.UID]; exists {
			return "", errors.New("manager drain selection contains a duplicate UID")
		}
		nameKey := pod.Namespace + "\x00" + pod.Name
		if _, exists := seenNames[nameKey]; exists {
			return "", errors.New("manager drain selection contains a duplicate namespace/name")
		}
		seenUIDs[pod.UID] = struct{}{}
		seenNames[nameKey] = struct{}{}
		record, err := drainSelectedPodDigest(pod)
		if err != nil {
			return "", err
		}
		records = append(records, record)
	}
	sort.Strings(records)
	return aggregateInventoryDigest("cvk-drain-selection-v1", records), nil
}

func drainSelectedPodDigest(pod *opsv1alpha1.UpgradeDrainPodStatus) (string, error) {
	controller, err := drainObjectReferenceDigest(&pod.Controller)
	if err != nil {
		return "", fmt.Errorf("manager drain controller reference: %w", err)
	}
	workloadController := "none"
	if pod.WorkloadController != nil {
		workloadController, err = drainObjectReferenceDigest(pod.WorkloadController)
		if err != nil {
			return "", fmt.Errorf("manager drain workload-controller reference: %w", err)
		}
	}
	if len(pod.PDBs) != 1 || pod.TerminationGracePeriodSeconds < 1 ||
		pod.TerminationGracePeriodSeconds > 600 {
		return "", errors.New("manager drain selection has incomplete PDB or termination evidence")
	}
	pdbs := make([]string, 0, len(pod.PDBs))
	seenPDBs := make(map[string]struct{}, len(pod.PDBs))
	for i := range pod.PDBs {
		pdb := &pod.PDBs[i]
		if _, exists := seenPDBs[pdb.UID]; exists {
			return "", errors.New("manager drain selection has a duplicate PDB UID")
		}
		seenPDBs[pdb.UID] = struct{}{}
		reference, err := drainObjectReferenceDigest(&pdb.UpgradeDrainObjectReference)
		if err != nil {
			return "", fmt.Errorf("manager drain PDB reference: %w", err)
		}
		pdbs = append(pdbs, framedIdentityDigest(
			reference,
			strconv.FormatInt(pdb.ObservedGeneration, 10),
			strconv.FormatInt(int64(pdb.DisruptionsAllowed), 10),
			strconv.FormatInt(int64(pdb.CurrentHealthy), 10),
			strconv.FormatInt(int64(pdb.DesiredHealthy), 10),
			strconv.FormatInt(int64(pdb.ExpectedPods), 10),
		))
	}
	return framedIdentityDigest(
		pod.Namespace,
		pod.Name,
		pod.UID,
		pod.EligibilityHash,
		controller,
		workloadController,
		aggregateInventoryDigest("cvk-drain-pdb-set-v1", pdbs),
		strconv.FormatInt(pod.TerminationGracePeriodSeconds, 10),
	), nil
}

func drainObjectReferenceDigest(ref *opsv1alpha1.UpgradeDrainObjectReference) (string, error) {
	if ref == nil || ref.APIVersion == "" || ref.Kind == "" || ref.Namespace == "" || ref.Name == "" ||
		ref.UID == "" || ref.Generation < 1 {
		return "", errors.New("object reference is incomplete")
	}
	return framedIdentityDigest(
		ref.APIVersion,
		ref.Kind,
		ref.Namespace,
		ref.Name,
		ref.UID,
		strconv.FormatInt(ref.Generation, 10),
	), nil
}

func inspectDrainInventory(
	devicePods []*corev1.Pod,
	selected []opsv1alpha1.UpgradeDrainPodStatus,
	targetUID string,
	scanErr error,
) drainInventoryObservation {
	selectedByUID := make(map[string]struct{}, len(selected))
	for i := range selected {
		selectedByUID[selected[i].UID] = struct{}{}
	}

	type observedIdentity struct {
		pod       *corev1.Pod
		valid     bool
		nameKey   string
		ambiguous bool
	}
	records := make([]observedIdentity, len(devicePods))
	uidCounts := make(map[string]int, len(devicePods))
	nameCounts := make(map[string]int, len(devicePods))
	for i, pod := range devicePods {
		record := observedIdentity{pod: pod}
		if pod != nil {
			record.valid = validDevicePodIdentity(pod)
			if record.valid {
				record.nameKey = pod.Namespace + "\x00" + pod.Name
				uidCounts[string(pod.UID)]++
				nameCounts[record.nameKey]++
			}
		}
		records[i] = record
	}

	remaining := make(map[string]struct{}, len(selected))
	var foreign, unknown int64
	for i := range records {
		record := &records[i]
		if record.pod != nil {
			uid := string(record.pod.UID)
			if _, selectedUID := selectedByUID[uid]; selectedUID {
				// An exact UID observation always means remaining, even when
				// the rest of the device identity is malformed or conflicting.
				remaining[uid] = struct{}{}
			}
		}
		if !record.valid {
			unknown++
			continue
		}
		uid := string(record.pod.UID)
		record.ambiguous = uidCounts[uid] != 1 || nameCounts[record.nameKey] != 1
		_, selectedUID := selectedByUID[uid]
		if record.ambiguous {
			unknown++
			continue
		}
		if !selectedUID {
			foreign++
		}
	}

	remainingUIDs := make([]string, 0, len(remaining))
	for uid := range remaining {
		remainingUIDs = append(remainingUIDs, uid)
	}
	sort.Strings(remainingUIDs)
	foreignCount, foreignOverflow := boundedInventoryCount(foreign)
	unknownCount, unknownOverflow := boundedInventoryCount(unknown)
	return drainInventoryObservation{
		complete:              scanErr == nil && unknown == 0 && !foreignOverflow && !unknownOverflow,
		remainingSelectedUIDs: remainingUIDs,
		foreignCount:          foreignCount,
		unknownCount:          unknownCount,
		targetPresent:         containsString(remainingUIDs, targetUID),
	}
}

func validDevicePodIdentity(pod *corev1.Pod) bool {
	if pod == nil || validation.IsDNS1123Label(pod.Namespace) != nil ||
		validation.IsDNS1123Subdomain(pod.Name) != nil {
		return false
	}
	uid := string(pod.UID)
	if uid == "" || len(uid) > 128 || strings.TrimSpace(uid) != uid || !utf8.ValidString(uid) {
		return false
	}
	for _, r := range uid {
		if unicode.IsControl(r) || unicode.IsSpace(r) {
			return false
		}
	}
	return true
}

func framedIdentityDigest(fields ...string) string {
	h := sha256.New()
	var length [8]byte
	for _, field := range fields {
		binary.BigEndian.PutUint64(length[:], uint64(len(field)))
		_, _ = h.Write(length[:])
		_, _ = h.Write([]byte(field))
	}
	return fmt.Sprintf("%x", h.Sum(nil))
}

func aggregateInventoryDigest(domain string, records []string) string {
	sort.Strings(records)
	h := sha256.New()
	_, _ = h.Write([]byte(domain))
	_, _ = h.Write([]byte{0})
	var count [8]byte
	binary.BigEndian.PutUint64(count[:], uint64(len(records)))
	_, _ = h.Write(count[:])
	for _, record := range records {
		_, _ = h.Write([]byte(record))
	}
	return fmt.Sprintf("sha256:%x", h.Sum(nil))
}

func boundedInventoryCount(count int64) (int32, bool) {
	if count > math.MaxInt32 {
		return math.MaxInt32, true
	}
	return int32(count), false
}

func containsString(sorted []string, value string) bool {
	index := sort.SearchStrings(sorted, value)
	return index < len(sorted) && sorted[index] == value
}

func drainInventoryReason(observation drainInventoryObservation, recovering bool) string {
	if !observation.complete {
		return "InventoryIncomplete"
	}
	if observation.targetPresent {
		return "SelectedUIDPresent"
	}
	if observation.foreignCount != 0 {
		if recovering {
			return "RecoveryInventoryComplete"
		}
		return "ForeignWorkloadsPresent"
	}
	return "InventoryComplete"
}

func drainInventoryMessage(observation drainInventoryObservation, recovering bool) string {
	if !observation.complete {
		return "Device inventory scan was partial or contained ambiguous workload identity."
	}
	if observation.targetPresent {
		return "Complete device inventory still contains the selected Pod UID."
	}
	if observation.foreignCount != 0 {
		if recovering {
			return "Complete recovery inventory proves the selected Pod UID is absent; attributable non-selected workloads remain."
		}
		return "Complete device inventory contains non-selected workloads."
	}
	return "Complete device inventory proves the selected Pod UID is absent."
}
