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

// Package topologyrollout contains the cluster-wide admission accounting used
// by topology-aware software rollouts. It deliberately contains no device RPC
// code: the manager reserves risk, while per-device workers execute mutations.
package topologyrollout

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"sort"
	"strings"
	"time"

	"k8s.io/apimachinery/pkg/util/validation"
)

const (
	LedgerVersion             = "v1"
	DefaultMaxActiveRecords   = 256
	DefaultMaxSerializedBytes = 256 * 1024
)

var (
	ErrLedgerIdentity       = errors.New("rollout ledger identity mismatch")
	ErrLedgerFull           = errors.New("rollout ledger capacity exhausted")
	ErrAlreadyReserved      = errors.New("physical device already has an active reservation")
	ErrBudgetExceeded       = errors.New("topology disruption budget exhausted")
	ErrTargetUnavailable    = errors.New("target device is unavailable")
	ErrInvalidTransition    = errors.New("invalid reservation transition")
	ErrStaleControlRevision = errors.New("stale reservation control revision")
)

type ReservationState string

const (
	ReservationReserved ReservationState = "Reserved"
	ReservationBound    ReservationState = "Bound"
	ReservationGranted  ReservationState = "Granted"
	ReservationRevoked  ReservationState = "Revoked"
)

// Policy is the already-validated administrator ceiling used for one admission
// decision. CampaignLimits can only tighten DomainBudgets.
type Policy struct {
	UID                          string         `json:"uid"`
	Version                      string         `json:"version"`
	Epoch                        int64          `json:"epoch"`
	GlobalMaxConcurrentTransfers int            `json:"globalMaxConcurrentTransfers"`
	GlobalMaxUnavailable         int            `json:"globalMaxUnavailable"`
	DomainTransferBudgets        map[string]int `json:"domainTransferBudgets"`
	DomainBudgets                map[string]int `json:"domainBudgets"`
	MaxActiveRecords             int            `json:"maxActiveRecords"`
	MaxSerializedBytes           int            `json:"maxSerializedBytes"`
	RequiredHealthFreshBy        time.Time      `json:"-"`
}

// Member is a current, uncached fleet-health input. Domain values must already
// be canonicalized (for hierarchical keys, use the complete flattened path).
type Member struct {
	PhysicalID     string            `json:"physicalID"`
	DeviceUID      string            `json:"deviceUID"`
	NodeUID        string            `json:"nodeUID"`
	Domains        map[string]string `json:"domains"`
	HealthKnown    bool              `json:"healthKnown"`
	Healthy        bool              `json:"healthy"`
	Maintenance    bool              `json:"maintenance"`
	HealthObserved time.Time         `json:"healthObserved"`
}

func (m Member) unavailable(policy Policy) bool {
	return !m.HealthKnown || !m.Healthy || m.Maintenance ||
		(!policy.RequiredHealthFreshBy.IsZero() && m.HealthObserved.Before(policy.RequiredHealthFreshBy))
}

type ReservationRequest struct {
	ID                             string            `json:"id"`
	CampaignUID                    string            `json:"campaignUID"`
	PlanHash                       string            `json:"planHash"`
	PolicyUID                      string            `json:"policyUID"`
	PolicyVersion                  string            `json:"policyVersion"`
	PolicyEpoch                    int64             `json:"policyEpoch"`
	TopologyLockID                 string            `json:"topologyLockID"`
	PhysicalID                     string            `json:"physicalID"`
	DeviceUID                      string            `json:"deviceUID"`
	NodeUID                        string            `json:"nodeUID"`
	ChildNamespace                 string            `json:"childNamespace"`
	ChildName                      string            `json:"childName"`
	Domains                        map[string]string `json:"domains"`
	CampaignMaxConcurrentTransfers int               `json:"-"`
	CampaignMaxUnavailable         int               `json:"-"`
	CampaignTransferLimits         map[string]int    `json:"-"`
	CampaignLimits                 map[string]int    `json:"-"`
	ControlRevision                uint64            `json:"controlRevision"`
}

type Reservation struct {
	ReservationRequest
	ChildUID string           `json:"childUID,omitempty"`
	State    ReservationState `json:"state"`
}

// ReleaseFence records the last exact topology-lock acquisition whose cleanup
// won the ConfigMap CAS. It makes otherwise no-op cleanup conflict with an
// in-flight Reserve that read the pre-fence ledger.
type ReleaseFence struct {
	ReservationID  string `json:"reservationID"`
	PolicyEpoch    int64  `json:"policyEpoch"`
	TopologyLockID string `json:"topologyLockID"`
}

type Ledger struct {
	Version          string                 `json:"version"`
	UID              string                 `json:"uid"`
	Reservations     map[string]Reservation `json:"reservations"`
	LastReleaseFence *ReleaseFence          `json:"lastReleaseFence,omitempty"`
}

// NewLedger constructs an empty ledger bound to the immutable Kubernetes
// ConfigMap UID. Callers must persist this identity outside the ledger as well;
// recreating a same-name ConfigMap must not silently establish a new authority.
func NewLedger(uid string) (*Ledger, error) {
	if strings.TrimSpace(uid) == "" {
		return nil, fmt.Errorf("%w: empty UID", ErrLedgerIdentity)
	}
	return &Ledger{Version: LedgerVersion, UID: uid, Reservations: map[string]Reservation{}}, nil
}

func Decode(data []byte, expectedUID string) (*Ledger, error) {
	if strings.TrimSpace(expectedUID) == "" {
		return nil, fmt.Errorf("%w: expected UID is empty", ErrLedgerIdentity)
	}
	if len(data) > DefaultMaxSerializedBytes {
		return nil, fmt.Errorf("%w: encoded ledger is %d bytes (absolute limit %d)",
			ErrLedgerIdentity, len(data), DefaultMaxSerializedBytes)
	}
	var ledger Ledger
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&ledger); err != nil {
		return nil, fmt.Errorf("%w: decode rollout ledger: %v", ErrLedgerIdentity, err)
	}
	if err := decoder.Decode(&struct{}{}); err != io.EOF {
		return nil, fmt.Errorf("%w: decode rollout ledger: trailing JSON data", ErrLedgerIdentity)
	}
	if ledger.Version != LedgerVersion || ledger.UID != expectedUID {
		return nil, fmt.Errorf("%w: version=%q uid=%q expected uid=%q", ErrLedgerIdentity, ledger.Version, ledger.UID, expectedUID)
	}
	if err := validatePersistedLedger(&ledger); err != nil {
		return nil, err
	}
	return &ledger, nil
}

// validatePersistedLedger treats the ConfigMap as hostile persisted input.
// Admission protects normal writes, but backup/restore, old binaries, or
// direct etcd repair can still introduce malformed records. A malformed
// safety ledger must freeze admission rather than be interpreted leniently.
func validatePersistedLedger(ledger *Ledger) error {
	if ledger == nil || ledger.Version != LedgerVersion || !validBoundedIdentity(ledger.UID, 128) {
		return fmt.Errorf("%w: ledger version or UID is invalid", ErrLedgerIdentity)
	}
	if ledger.Reservations == nil {
		return fmt.Errorf("%w: reservations must be an object", ErrLedgerIdentity)
	}
	if len(ledger.Reservations) > DefaultMaxActiveRecords {
		return fmt.Errorf("%w: %d reservations exceed the absolute limit %d",
			ErrLedgerIdentity, len(ledger.Reservations), DefaultMaxActiveRecords)
	}
	if ledger.LastReleaseFence != nil {
		fence := ledger.LastReleaseFence
		if !validBoundedIdentity(fence.ReservationID, 64) || fence.PolicyEpoch < 1 ||
			!validTopologyLockID(fence.TopologyLockID) {
			return fmt.Errorf("%w: last release fence is invalid", ErrLedgerIdentity)
		}
	}

	physicalIDs := make(map[string]string, len(ledger.Reservations))
	deviceUIDs := make(map[string]string, len(ledger.Reservations))
	nodeUIDs := make(map[string]string, len(ledger.Reservations))
	children := make(map[string]string, len(ledger.Reservations))
	childUIDs := make(map[string]string, len(ledger.Reservations))
	for key, reservation := range ledger.Reservations {
		if key == "" || key != reservation.ID {
			return fmt.Errorf("%w: reservation map key %q does not match id %q", ErrLedgerIdentity, key, reservation.ID)
		}
		if err := validatePersistedReservation(reservation); err != nil {
			return fmt.Errorf("%w: reservation %q: %v", ErrLedgerIdentity, key, err)
		}
		for name, value := range map[string]string{
			"physical identity": reservation.PhysicalID,
			"device UID":        reservation.DeviceUID,
			"node UID":          reservation.NodeUID,
		} {
			var seen map[string]string
			switch name {
			case "physical identity":
				seen = physicalIDs
			case "device UID":
				seen = deviceUIDs
			default:
				seen = nodeUIDs
			}
			if prior, duplicate := seen[value]; duplicate && prior != key {
				return fmt.Errorf("%w: reservations %q and %q reuse %s %q", ErrLedgerIdentity, prior, key, name, value)
			}
			seen[value] = key
		}
		childKey := reservation.ChildNamespace + "/" + reservation.ChildName
		if prior, duplicate := children[childKey]; duplicate && prior != key {
			return fmt.Errorf("%w: reservations %q and %q reuse child %q", ErrLedgerIdentity, prior, key, childKey)
		}
		children[childKey] = key
		if reservation.ChildUID != "" {
			if prior, duplicate := childUIDs[reservation.ChildUID]; duplicate && prior != key {
				return fmt.Errorf("%w: reservations %q and %q reuse child UID %q", ErrLedgerIdentity, prior, key, reservation.ChildUID)
			}
			childUIDs[reservation.ChildUID] = key
		}
	}
	return nil
}

func validatePersistedReservation(reservation Reservation) error {
	for _, field := range []struct {
		name  string
		value string
		max   int
	}{
		{name: "id", value: reservation.ID, max: 64},
		{name: "campaign UID", value: reservation.CampaignUID, max: 128},
		{name: "plan hash", value: reservation.PlanHash, max: 71},
		{name: "policy UID", value: reservation.PolicyUID, max: 128},
		{name: "policy resourceVersion", value: reservation.PolicyVersion, max: 128},
		{name: "topology lock acquisition ID", value: reservation.TopologyLockID, max: 64},
		{name: "physical identity", value: reservation.PhysicalID, max: 128},
		{name: "device UID", value: reservation.DeviceUID, max: 128},
		{name: "node UID", value: reservation.NodeUID, max: 128},
		{name: "child namespace", value: reservation.ChildNamespace, max: 63},
		{name: "child name", value: reservation.ChildName, max: 253},
	} {
		if !validBoundedIdentity(field.value, field.max) {
			return fmt.Errorf("%s must contain 1-%d characters", field.name, field.max)
		}
	}
	if !validSHA256PlanHash(reservation.PlanHash) {
		return fmt.Errorf("plan hash is not a canonical SHA-256 value")
	}
	if reservation.PolicyEpoch < 1 {
		return fmt.Errorf("policy epoch must be positive")
	}
	if !validTopologyLockID(reservation.TopologyLockID) {
		return fmt.Errorf("topology lock acquisition ID must be a 128-bit lowercase hexadecimal value")
	}
	if problems := validation.IsDNS1123Label(reservation.ChildNamespace); len(problems) > 0 {
		return fmt.Errorf("child namespace is invalid: %s", strings.Join(problems, "; "))
	}
	if problems := validation.IsDNS1123Subdomain(reservation.ChildName); len(problems) > 0 {
		return fmt.Errorf("child name is invalid: %s", strings.Join(problems, "; "))
	}
	if len(reservation.Domains) == 0 || len(reservation.Domains) > 16 {
		return fmt.Errorf("domains must contain 1-16 entries")
	}
	for key, value := range reservation.Domains {
		if len(key) > 96 || len(value) > 63 || strings.TrimSpace(value) == "" {
			return fmt.Errorf("domain key/value exceeds the 96/63 character limit")
		}
		if problems := validation.IsQualifiedName(key); len(problems) > 0 {
			return fmt.Errorf("domain key %q is invalid: %s", key, strings.Join(problems, "; "))
		}
		if problems := validation.IsValidLabelValue(value); len(problems) > 0 {
			return fmt.Errorf("domain value for %q is invalid: %s", key, strings.Join(problems, "; "))
		}
	}
	switch reservation.State {
	case ReservationReserved:
		if reservation.ChildUID != "" {
			return fmt.Errorf("Reserved state must not have a child UID")
		}
	case ReservationBound, ReservationGranted, ReservationRevoked:
		if !validBoundedIdentity(reservation.ChildUID, 128) {
			return fmt.Errorf("%s state requires a bounded child UID", reservation.State)
		}
	default:
		return fmt.Errorf("unknown state %q", reservation.State)
	}
	if reservation.ControlRevision > uint64(1<<63-1) {
		return fmt.Errorf("control revision exceeds the signed campaign revision range")
	}
	return nil
}

func validBoundedIdentity(value string, maxLength int) bool {
	if value == "" || len(value) > maxLength || strings.TrimSpace(value) != value {
		return false
	}
	for _, character := range value {
		if character < '!' || character > '~' {
			return false
		}
	}
	return true
}

func validSHA256PlanHash(value string) bool {
	if len(value) != len("sha256:")+sha256.Size*2 || !strings.HasPrefix(value, "sha256:") {
		return false
	}
	encoded := strings.TrimPrefix(value, "sha256:")
	if encoded != strings.ToLower(encoded) {
		return false
	}
	_, err := hex.DecodeString(encoded)
	return err == nil
}

func validTopologyLockID(value string) bool {
	if len(value) != 32 || value != strings.ToLower(value) {
		return false
	}
	decoded, err := hex.DecodeString(value)
	return err == nil && len(decoded) == 16
}

func Encode(ledger *Ledger, maxBytes int) ([]byte, error) {
	if ledger == nil || ledger.Version != LedgerVersion || strings.TrimSpace(ledger.UID) == "" {
		return nil, fmt.Errorf("%w: incomplete ledger", ErrLedgerIdentity)
	}
	if maxBytes <= 0 || maxBytes > DefaultMaxSerializedBytes {
		maxBytes = DefaultMaxSerializedBytes
	}
	data, err := json.Marshal(ledger)
	if err != nil {
		return nil, fmt.Errorf("encode rollout ledger: %w", err)
	}
	if len(data) > maxBytes {
		return nil, fmt.Errorf("%w: encoded ledger is %d bytes (limit %d)", ErrLedgerFull, len(data), maxBytes)
	}
	return data, nil
}

// Reserve atomically evaluates all applicable dimensions against one snapshot
// and mutates the in-memory ledger only when every check succeeds. Persistence
// must use a resourceVersion CAS on the single backing ConfigMap.
func Reserve(ledger *Ledger, policy Policy, members []Member, request ReservationRequest) error {
	if err := validateInputs(ledger, policy, request); err != nil {
		return err
	}
	if releaseFenceMatches(ledger.LastReleaseFence, request.ID, request.PolicyEpoch, request.TopologyLockID) {
		return fmt.Errorf("%w: topology-lock acquisition was durably released", ErrStaleControlRevision)
	}
	if existing, ok := ledger.Reservations[request.ID]; ok {
		if sameRequest(existing.ReservationRequest, request) {
			return nil
		}
		return fmt.Errorf("%w: reservation ID %q has different identity", ErrAlreadyReserved, request.ID)
	}

	canonicalMembers, err := canonicalizeMembers(members)
	if err != nil {
		return err
	}
	target, ok := canonicalMembers[request.PhysicalID]
	if !ok || target.DeviceUID != request.DeviceUID || target.NodeUID != request.NodeUID {
		return fmt.Errorf("%w: target identity is absent or changed", ErrTargetUnavailable)
	}
	if target.unavailable(policy) {
		return ErrTargetUnavailable
	}
	checkedDomains := make(map[string]struct{}, len(policy.DomainBudgets)+len(policy.DomainTransferBudgets))
	for key := range policy.DomainBudgets {
		checkedDomains[key] = struct{}{}
	}
	for key := range policy.DomainTransferBudgets {
		checkedDomains[key] = struct{}{}
	}
	for key := range checkedDomains {
		if request.Domains[key] == "" || request.Domains[key] != target.Domains[key] {
			return fmt.Errorf("invalid or stale domain %q for target", key)
		}
	}
	for id, reservation := range ledger.Reservations {
		if reservation.PhysicalID == request.PhysicalID {
			return fmt.Errorf("%w: %s holds %s", ErrAlreadyReserved, id, reservation.State)
		}
	}

	activeLimit := policy.MaxActiveRecords
	if activeLimit <= 0 {
		activeLimit = DefaultMaxActiveRecords
	}
	if len(ledger.Reservations) >= activeLimit {
		return fmt.Errorf("%w: active records reached %d", ErrLedgerFull, activeLimit)
	}

	global := unavailablePhysicalIDs(canonicalMembers, ledger, policy, "", "")
	global[request.PhysicalID] = struct{}{}
	if len(global) > policy.GlobalMaxUnavailable {
		return fmt.Errorf("%w: administrator global unavailable=%d limit=%d", ErrBudgetExceeded, len(global), policy.GlobalMaxUnavailable)
	}
	if campaignUnavailable := activeReservationsForCampaign(ledger, request.CampaignUID) + 1; campaignUnavailable > effectiveLimit(policy.GlobalMaxUnavailable, request.CampaignMaxUnavailable) {
		return fmt.Errorf("%w: campaign unavailable=%d limit=%d", ErrBudgetExceeded, campaignUnavailable,
			effectiveLimit(policy.GlobalMaxUnavailable, request.CampaignMaxUnavailable))
	}
	if globalTransfers := len(ledger.Reservations) + 1; globalTransfers > policy.GlobalMaxConcurrentTransfers {
		return fmt.Errorf("%w: administrator global transfers=%d limit=%d", ErrBudgetExceeded, globalTransfers, policy.GlobalMaxConcurrentTransfers)
	}
	if campaignTransfers := activeReservationsForCampaign(ledger, request.CampaignUID) + 1; campaignTransfers > effectiveLimit(policy.GlobalMaxConcurrentTransfers, request.CampaignMaxConcurrentTransfers) {
		return fmt.Errorf("%w: campaign transfers=%d limit=%d", ErrBudgetExceeded, campaignTransfers,
			effectiveLimit(policy.GlobalMaxConcurrentTransfers, request.CampaignMaxConcurrentTransfers))
	}

	keys := make([]string, 0, len(policy.DomainBudgets)+len(policy.DomainTransferBudgets))
	for key := range policy.DomainBudgets {
		keys = append(keys, key)
	}
	for key := range policy.DomainTransferBudgets {
		if _, exists := policy.DomainBudgets[key]; !exists {
			keys = append(keys, key)
		}
	}
	sort.Strings(keys)
	for _, key := range keys {
		value := request.Domains[key]
		if unavailableCeiling, applies := policy.DomainBudgets[key]; applies {
			unavailable := unavailablePhysicalIDs(canonicalMembers, ledger, policy, key, value)
			unavailable[request.PhysicalID] = struct{}{}
			if len(unavailable) > unavailableCeiling {
				return fmt.Errorf("%w: administrator %s=%s unavailable=%d limit=%d", ErrBudgetExceeded, key, value, len(unavailable), unavailableCeiling)
			}
			campaignLimit := effectiveLimit(unavailableCeiling, request.CampaignLimits[key])
			campaignUnavailable := activeReservationsForCampaignInDomain(ledger, request.CampaignUID, key, value) + 1
			if campaignUnavailable > campaignLimit {
				return fmt.Errorf("%w: campaign %s=%s unavailable=%d limit=%d", ErrBudgetExceeded, key, value, campaignUnavailable, campaignLimit)
			}
		}
		if transferCeiling, applies := policy.DomainTransferBudgets[key]; applies {
			transfers := activeReservationsInDomain(ledger, key, value) + 1
			if transfers > transferCeiling {
				return fmt.Errorf("%w: administrator %s=%s transfers=%d limit=%d", ErrBudgetExceeded, key, value, transfers, transferCeiling)
			}
			campaignLimit := effectiveLimit(transferCeiling, request.CampaignTransferLimits[key])
			campaignTransfers := activeReservationsForCampaignInDomain(ledger, request.CampaignUID, key, value) + 1
			if campaignTransfers > campaignLimit {
				return fmt.Errorf("%w: campaign %s=%s transfers=%d limit=%d", ErrBudgetExceeded, key, value, campaignTransfers, campaignLimit)
			}
		}
	}

	request.Domains = cloneStringMap(request.Domains)
	request.CampaignLimits = nil
	request.CampaignTransferLimits = nil
	request.CampaignMaxConcurrentTransfers = 0
	request.CampaignMaxUnavailable = 0
	ledger.Reservations[request.ID] = Reservation{ReservationRequest: request, State: ReservationReserved}
	if err := validatePersistedLedger(ledger); err != nil {
		delete(ledger.Reservations, request.ID)
		return err
	}
	if _, err := Encode(ledger, policy.MaxSerializedBytes); err != nil {
		delete(ledger.Reservations, request.ID)
		return err
	}
	return nil
}

func activeReservationsInDomain(ledger *Ledger, key, value string) int {
	count := 0
	for _, reservation := range ledger.Reservations {
		if reservation.Domains[key] == value {
			count++
		}
	}
	return count
}

func activeReservationsForCampaign(ledger *Ledger, campaignUID string) int {
	count := 0
	for _, reservation := range ledger.Reservations {
		if reservation.CampaignUID == campaignUID {
			count++
		}
	}
	return count
}

func activeReservationsForCampaignInDomain(ledger *Ledger, campaignUID, key, value string) int {
	count := 0
	for _, reservation := range ledger.Reservations {
		if reservation.CampaignUID == campaignUID && reservation.Domains[key] == value {
			count++
		}
	}
	return count
}

func BindChild(ledger *Ledger, reservationID, childUID string) error {
	reservation, ok := ledger.Reservations[reservationID]
	if !ok || strings.TrimSpace(childUID) == "" {
		return fmt.Errorf("%w: reservation or child UID missing", ErrInvalidTransition)
	}
	if reservation.ChildUID == childUID && (reservation.State == ReservationBound || reservation.State == ReservationGranted || reservation.State == ReservationRevoked) {
		return nil
	}
	if reservation.State != ReservationReserved || reservation.ChildUID != "" {
		return fmt.Errorf("%w: cannot bind child in state %s", ErrInvalidTransition, reservation.State)
	}
	reservation.ChildUID = childUID
	reservation.State = ReservationBound
	ledger.Reservations[reservationID] = reservation
	return nil
}

func Grant(ledger *Ledger, reservationID, ledgerUID, childUID string, controlRevision uint64) error {
	if ledger == nil || ledger.UID != ledgerUID {
		return ErrLedgerIdentity
	}
	reservation, ok := ledger.Reservations[reservationID]
	if ok && reservation.State == ReservationGranted && reservation.ChildUID == childUID {
		if controlRevision < reservation.ControlRevision {
			return ErrStaleControlRevision
		}
		// A ledger CAS may succeed before the corresponding leaf-status patch.
		// Permit a later, fully revalidated manager attempt to advance the same
		// grant to a newer control revision without replaying the reservation.
		reservation.ControlRevision = controlRevision
		ledger.Reservations[reservationID] = reservation
		return nil
	}
	if !ok || reservation.ChildUID == "" || reservation.ChildUID != childUID || reservation.State != ReservationBound {
		return fmt.Errorf("%w: reservation is not bound to child", ErrInvalidTransition)
	}
	// Revision zero is the neutral initial campaign control and is a valid
	// first grant. Once granted, idempotence is handled above; later changes
	// remain strictly monotonic through Revoke.
	if controlRevision < reservation.ControlRevision {
		return ErrStaleControlRevision
	}
	reservation.ControlRevision = controlRevision
	reservation.State = ReservationGranted
	ledger.Reservations[reservationID] = reservation
	return nil
}

func Revoke(ledger *Ledger, reservationID string, controlRevision uint64) error {
	reservation, ok := ledger.Reservations[reservationID]
	if !ok {
		return fmt.Errorf("%w: reservation missing", ErrInvalidTransition)
	}
	if reservation.State == ReservationRevoked && controlRevision == reservation.ControlRevision {
		return nil
	}
	if controlRevision <= reservation.ControlRevision {
		return ErrStaleControlRevision
	}
	switch reservation.State {
	case ReservationReserved, ReservationBound, ReservationGranted, ReservationRevoked:
	default:
		return fmt.Errorf("%w: cannot revoke state %s", ErrInvalidTransition, reservation.State)
	}
	reservation.ControlRevision = controlRevision
	reservation.State = ReservationRevoked
	ledger.Reservations[reservationID] = reservation
	return nil
}

// Settle removes a revoked or granted reservation only after the caller has
// proven the physical outcome and post-operation health/soak gate. There is no
// timer-based release path. The exact acquisition fence is recorded even when
// settlement is replayed after the reservation was already removed.
func Settle(
	ledger *Ledger,
	reservationID, topologyLockID, childUID string,
	policyEpoch int64,
	outcomeResolved, healthGatePassed bool,
) error {
	reservation, ok := ledger.Reservations[reservationID]
	if !ok {
		// Settlement is idempotent across a successful ledger CAS followed by a
		// manager crash before the leaf status acknowledgement is persisted.
		return recordReleaseFence(ledger, reservationID, policyEpoch, topologyLockID)
	}
	if reservation.PolicyEpoch != policyEpoch || reservation.TopologyLockID != topologyLockID {
		return ErrStaleControlRevision
	}
	if reservation.ChildUID != childUID || !outcomeResolved || !healthGatePassed {
		return fmt.Errorf("%w: settlement evidence incomplete", ErrInvalidTransition)
	}
	if reservation.State != ReservationGranted && reservation.State != ReservationRevoked {
		return fmt.Errorf("%w: cannot settle state %s", ErrInvalidTransition, reservation.State)
	}
	if err := recordReleaseFence(ledger, reservationID, policyEpoch, topologyLockID); err != nil {
		return err
	}
	delete(ledger.Reservations, reservationID)
	return nil
}

func validateInputs(ledger *Ledger, policy Policy, request ReservationRequest) error {
	if ledger == nil || ledger.Version != LedgerVersion || ledger.UID == "" {
		return ErrLedgerIdentity
	}
	if policy.UID == "" || policy.Version == "" || policy.Epoch < 1 || request.PolicyUID != policy.UID ||
		request.PolicyVersion != policy.Version || request.PolicyEpoch != policy.Epoch {
		return fmt.Errorf("%w: policy identity changed", ErrLedgerIdentity)
	}
	if !validTopologyLockID(request.TopologyLockID) {
		return fmt.Errorf("%w: topology lock acquisition identity is invalid", ErrLedgerIdentity)
	}
	if policy.GlobalMaxConcurrentTransfers < 1 || policy.GlobalMaxUnavailable < 1 {
		return fmt.Errorf("administrator global transfer and unavailable budgets must be positive")
	}
	for name, value := range map[string]string{
		"reservation ID": request.ID, "campaign UID": request.CampaignUID, "plan hash": request.PlanHash,
		"physical ID": request.PhysicalID, "device UID": request.DeviceUID, "node UID": request.NodeUID,
		"child namespace": request.ChildNamespace, "child name": request.ChildName,
	} {
		if strings.TrimSpace(value) == "" {
			return fmt.Errorf("%s is required", name)
		}
	}
	for key := range policy.DomainBudgets {
		if request.Domains[key] == "" {
			return fmt.Errorf("reservation domain %q is required", key)
		}
	}
	for key := range policy.DomainTransferBudgets {
		if request.Domains[key] == "" {
			return fmt.Errorf("reservation transfer domain %q is required", key)
		}
	}
	return nil
}

// ReleaseUnclaimedAtEpoch adds an exact policy-epoch fence to ReleaseUnclaimed.
// It prevents a delayed cleanup from deleting a reservation that a newer
// policy epoch or topology-lock acquisition re-established under the same
// deterministic reservation ID. It records the release fence even when the
// reservation is absent, forcing an in-flight Reserve to lose its ConfigMap
// resourceVersion CAS and rerun cross-object authorization.
func ReleaseUnclaimedAtEpoch(
	ledger *Ledger,
	reservationID, topologyLockID, childUID string,
	controlRevision uint64,
	policyEpoch int64,
) error {
	if policyEpoch < 1 {
		return ErrStaleControlRevision
	}
	return releaseUnclaimed(ledger, reservationID, topologyLockID, childUID, controlRevision, policyEpoch)
}

// RevokeUnclaimedAtEpoch converts an exact child-bound reservation into the
// durable Revoked state after its caller has freshly proven that the bound leaf
// is manager-revoked and has no mutation claims. Bound and Granted are kept out
// of ReleaseUnclaimedAtEpoch so a generic cleanup path can never erase live
// mutation authority. Equal control revisions are valid: policy/source fences
// can revoke an unclaimed grant without changing campaign control.
func RevokeUnclaimedAtEpoch(
	ledger *Ledger,
	reservationID, topologyLockID, childUID string,
	controlRevision uint64,
	policyEpoch int64,
) error {
	if policyEpoch < 1 {
		return ErrStaleControlRevision
	}
	reservation, ok := ledger.Reservations[reservationID]
	if !ok {
		return fmt.Errorf("%w: reservation missing", ErrInvalidTransition)
	}
	if reservation.PolicyEpoch != policyEpoch || reservation.TopologyLockID != topologyLockID {
		return ErrStaleControlRevision
	}
	if strings.TrimSpace(childUID) == "" || reservation.ChildUID != childUID {
		return fmt.Errorf("%w: reservation is not bound to the proven child", ErrInvalidTransition)
	}
	if controlRevision < reservation.ControlRevision {
		return ErrStaleControlRevision
	}
	switch reservation.State {
	case ReservationBound, ReservationGranted, ReservationRevoked:
	default:
		return fmt.Errorf("%w: cannot revoke unclaimed state %s", ErrInvalidTransition, reservation.State)
	}
	reservation.ControlRevision = controlRevision
	reservation.State = ReservationRevoked
	ledger.Reservations[reservationID] = reservation
	return nil
}

func releaseUnclaimed(
	ledger *Ledger,
	reservationID, topologyLockID, childUID string,
	controlRevision uint64,
	policyEpoch int64,
) error {
	reservation, ok := ledger.Reservations[reservationID]
	if !ok {
		return recordReleaseFence(ledger, reservationID, policyEpoch, topologyLockID)
	}
	if reservation.PolicyEpoch != policyEpoch || reservation.TopologyLockID != topologyLockID {
		return ErrStaleControlRevision
	}
	if controlRevision < reservation.ControlRevision {
		return ErrStaleControlRevision
	}
	switch reservation.State {
	case ReservationReserved:
		if reservation.ChildUID != "" {
			return fmt.Errorf("%w: reserved entry has an unexpected child binding", ErrInvalidTransition)
		}
		// A cancellation tombstone may win the leaf CAS before a manager ever
		// bound its UID into the ledger. The caller supplies that exact retained
		// leaf UID as evidence; an empty UID remains valid for the pre-create
		// cancellation case.
		if err := recordReleaseFence(ledger, reservationID, policyEpoch, topologyLockID); err != nil {
			return err
		}
		delete(ledger.Reservations, reservationID)
		return nil
	case ReservationRevoked:
		// Continue below and require the exact durable child fence.
	default:
		return fmt.Errorf("%w: cannot release state %s before an exact unclaimed revocation", ErrInvalidTransition, reservation.State)
	}
	if strings.TrimSpace(childUID) == "" || reservation.ChildUID != childUID {
		return fmt.Errorf("%w: reservation is not bound to the proven child", ErrInvalidTransition)
	}
	if err := recordReleaseFence(ledger, reservationID, policyEpoch, topologyLockID); err != nil {
		return err
	}
	delete(ledger.Reservations, reservationID)
	return nil
}

// FenceUnboundAcquisition retires a lock that was durably published but whose
// Reserve did not complete. Once the device lock is Releasing, a concurrently
// in-flight Reserve can only leave an unbound Reserved record; this mutation
// either removes that record or conflicts its write and forces revalidation.
func FenceUnboundAcquisition(
	ledger *Ledger,
	reservationID string,
	policyEpoch int64,
	topologyLockID string,
) error {
	reservation, ok := ledger.Reservations[reservationID]
	if ok {
		if reservation.PolicyEpoch != policyEpoch || reservation.TopologyLockID != topologyLockID {
			return ErrStaleControlRevision
		}
		if reservation.State != ReservationReserved || reservation.ChildUID != "" {
			return fmt.Errorf("%w: topology-lock acquisition is already bound", ErrInvalidTransition)
		}
	}
	if err := recordReleaseFence(ledger, reservationID, policyEpoch, topologyLockID); err != nil {
		return err
	}
	delete(ledger.Reservations, reservationID)
	return nil
}

func recordReleaseFence(ledger *Ledger, reservationID string, policyEpoch int64, topologyLockID string) error {
	if ledger == nil || !validBoundedIdentity(reservationID, 64) || policyEpoch < 1 ||
		!validTopologyLockID(topologyLockID) {
		return fmt.Errorf("%w: release-fence identity is invalid", ErrLedgerIdentity)
	}
	ledger.LastReleaseFence = &ReleaseFence{
		ReservationID:  reservationID,
		PolicyEpoch:    policyEpoch,
		TopologyLockID: topologyLockID,
	}
	return nil
}

func releaseFenceMatches(fence *ReleaseFence, reservationID string, policyEpoch int64, topologyLockID string) bool {
	return fence != nil && fence.ReservationID == reservationID && fence.PolicyEpoch == policyEpoch &&
		fence.TopologyLockID == topologyLockID
}

// HasReleaseFence reports whether a Store.Mutate CAS committed cleanup for the
// exact acquisition. Callers must prove this before clearing its device lock.
func HasReleaseFence(ledger *Ledger, reservationID string, policyEpoch int64, topologyLockID string) bool {
	return ledger != nil && releaseFenceMatches(ledger.LastReleaseFence, reservationID, policyEpoch, topologyLockID)
}

func canonicalizeMembers(members []Member) (map[string]Member, error) {
	out := make(map[string]Member, len(members))
	for _, member := range members {
		if member.PhysicalID == "" || member.DeviceUID == "" || member.NodeUID == "" {
			return nil, fmt.Errorf("fleet member has incomplete identity")
		}
		if previous, ok := out[member.PhysicalID]; ok {
			if previous.DeviceUID != member.DeviceUID || previous.NodeUID != member.NodeUID || !equalStringMap(previous.Domains, member.Domains) {
				return nil, fmt.Errorf("duplicate physical identity %q has conflicting membership", member.PhysicalID)
			}
			// Duplicate observations never make health more optimistic.
			previous.HealthKnown = previous.HealthKnown && member.HealthKnown
			previous.Healthy = previous.Healthy && member.Healthy
			previous.Maintenance = previous.Maintenance || member.Maintenance
			if member.HealthObserved.Before(previous.HealthObserved) {
				previous.HealthObserved = member.HealthObserved
			}
			out[member.PhysicalID] = previous
			continue
		}
		member.Domains = cloneStringMap(member.Domains)
		out[member.PhysicalID] = member
	}
	return out, nil
}

func unavailablePhysicalIDs(members map[string]Member, ledger *Ledger, policy Policy, domainKey, domainValue string) map[string]struct{} {
	result := map[string]struct{}{}
	for id, member := range members {
		if domainKey != "" && member.Domains[domainKey] != domainValue {
			continue
		}
		if member.unavailable(policy) {
			result[id] = struct{}{}
		}
	}
	for _, reservation := range ledger.Reservations {
		if domainKey != "" && reservation.Domains[domainKey] != domainValue {
			continue
		}
		result[reservation.PhysicalID] = struct{}{}
	}
	return result
}

func effectiveLimit(admin, campaign int) int {
	if campaign > 0 && campaign < admin {
		return campaign
	}
	return admin
}

func sameRequest(a, b ReservationRequest) bool {
	clearCampaignLimits := func(request *ReservationRequest) {
		request.CampaignMaxConcurrentTransfers = 0
		request.CampaignMaxUnavailable = 0
		request.CampaignTransferLimits = nil
		request.CampaignLimits = nil
	}
	clearCampaignLimits(&a)
	clearCampaignLimits(&b)
	left, _ := json.Marshal(a)
	right, _ := json.Marshal(b)
	return string(left) == string(right)
}

func cloneStringMap(in map[string]string) map[string]string {
	out := make(map[string]string, len(in))
	for key, value := range in {
		out[key] = value
	}
	return out
}

func equalStringMap(a, b map[string]string) bool {
	if len(a) != len(b) {
		return false
	}
	for key, value := range a {
		if b[key] != value {
			return false
		}
	}
	return true
}
