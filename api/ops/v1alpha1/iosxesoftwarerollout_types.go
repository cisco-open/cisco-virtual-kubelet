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

package v1alpha1

import (
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

const (
	// MaxIOSXESoftwareRolloutTargets is both the public API ceiling and the
	// initial controller planning ceiling. Administrator policy may lower it.
	MaxIOSXESoftwareRolloutTargets = 100
	// MaxIOSXESoftwareRolloutPlanBytes is the encoded frozen-plan ceiling. The
	// field-level CRD bounds below make the object finite; the planner must also
	// reject a canonical JSON plan larger than this exact byte limit.
	MaxIOSXESoftwareRolloutPlanBytes = 256 * 1024
	// MaxIOSXESoftwareRolloutTopologyValues matches the maximum allowlisted
	// CiscoDevice-to-Node topology projection.
	MaxIOSXESoftwareRolloutTopologyValues = 16
)

// IOSXESoftwareRollout plans and admits a bounded IOS-XE software campaign.
// The manager owns fleet planning and never opens a device session; immutable
// IOSXESoftwareUpgrade leaves remain the only device-side executors.
//
// +kubebuilder:object:root=true
// +kubebuilder:resource:scope=Namespaced,shortName=xerollout
// +kubebuilder:subresource:status
// +kubebuilder:printcolumn:name="Target",type=string,JSONPath=`.spec.plan.targetVersion`
// +kubebuilder:printcolumn:name="Phase",type=string,JSONPath=`.status.phase`
// +kubebuilder:printcolumn:name="Planned",type=integer,JSONPath=`.status.counts.planned`
// +kubebuilder:printcolumn:name="Succeeded",type=integer,JSONPath=`.status.counts.succeeded`
// +kubebuilder:printcolumn:name="Failed",type=integer,JSONPath=`.status.counts.failed`
// +kubebuilder:printcolumn:name="Age",type=date,JSONPath=`.metadata.creationTimestamp`
// +kubebuilder:validation:XValidation:rule="!has(self.spec.approval) || (has(self.status) && has(self.status.frozenPlan) && self.spec.approval.planHash == self.status.frozenPlan.hash)",message="approval must authorize the exact published frozen plan hash"
type IOSXESoftwareRollout struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitempty"`

	Spec   IOSXESoftwareRolloutSpec   `json:"spec"`
	Status IOSXESoftwareRolloutStatus `json:"status,omitempty"`
}

// IOSXESoftwareRolloutList contains IOSXESoftwareRollout objects.
//
// +kubebuilder:object:root=true
type IOSXESoftwareRolloutList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitempty"`
	Items           []IOSXESoftwareRollout `json:"items"`
}

// IOSXESoftwareRolloutSpec separates executable intent from authorization and
// runtime controls. Plan is immutable. Approval may be added exactly once and
// must name the manager-produced hash. Control is the only repeatably mutable
// part and carries a monotonic revision. Native admission policy must bind the
// three identity fields to request.userInfo and authorize approval separately.
//
// +kubebuilder:validation:XValidation:rule="self.plan == oldSelf.plan",message="plan is immutable; create a new rollout to change executable intent"
// +kubebuilder:validation:XValidation:rule="!has(oldSelf.approval) || (has(self.approval) && self.approval == oldSelf.approval)",message="approval is append-only and immutable once recorded"
type IOSXESoftwareRolloutSpec struct {
	// Plan is the immutable requested campaign input from which the manager
	// creates a canonical frozen target plan.
	// +kubebuilder:validation:Required
	Plan IOSXESoftwareRolloutPlan `json:"plan"`

	// Approval authorizes exactly status.frozenPlan.hash. Its absence means no
	// IOSXESoftwareUpgrade child may receive mutation permission.
	// +kubebuilder:validation:Optional
	Approval *IOSXESoftwareRolloutApproval `json:"approval,omitempty"`

	// Control carries pause/resume/cancel requests. Revision zero is the
	// required neutral value at creation.
	// +kubebuilder:validation:Required
	Control IOSXESoftwareRolloutControl `json:"control"`
}

// IOSXESoftwareRolloutPlan is immutable executable campaign input.
type IOSXESoftwareRolloutPlan struct {
	// RequestedBy is an audit assertion that native admission must bind to the
	// authenticated username on creation.
	// +kubebuilder:validation:Required
	// +kubebuilder:validation:MinLength=1
	// +kubebuilder:validation:MaxLength=253
	RequestedBy string `json:"requestedBy"`

	// RequestedAt records when the authenticated requester submitted the plan.
	// +kubebuilder:validation:Required
	RequestedAt metav1.Time `json:"requestedAt"`

	// Targets selects CiscoDevice objects only from the rollout namespace.
	// +kubebuilder:validation:Required
	Targets IOSXESoftwareRolloutTargetSpec `json:"targets"`

	// Source is the one artifact source supported by the Phase 2 MVP. Mirror
	// selection and prefetch are deliberately not represented by this API.
	// +kubebuilder:validation:Required
	Source IOSXESoftwareRolloutSourceSpec `json:"source"`

	// TargetVersion is the IOS-XE version accepted by the existing leaf API.
	// +kubebuilder:validation:Required
	// +kubebuilder:validation:MaxLength=128
	// +kubebuilder:validation:Pattern=`^[0-9]+(\.[0-9]+)+([a-z])?$`
	TargetVersion string `json:"targetVersion"`

	// Strategy is Reload for the Phase 2 MVP. ISSU and NoReboot do not expose
	// the durable stage/activate boundary required by this campaign contract.
	// +kubebuilder:validation:Optional
	// +kubebuilder:validation:Enum=Reload
	// +kubebuilder:default=Reload
	Strategy IOSXESoftwareRolloutStrategy `json:"strategy,omitempty"`

	// RollbackOnFailure is copied into each immutable leaf. Default true.
	// +kubebuilder:validation:Optional
	// +kubebuilder:default=true
	RollbackOnFailure *bool `json:"rollbackOnFailure,omitempty"`

	// InstallTimeoutSeconds is copied into every immutable leaf and covers
	// source resolution, transfer/install, and inventory convergence.
	// +kubebuilder:validation:Optional
	// +kubebuilder:validation:Minimum=60
	// +kubebuilder:validation:Maximum=86400
	// +kubebuilder:default=3600
	InstallTimeoutSeconds int32 `json:"installTimeoutSeconds,omitempty"`

	// RebootTimeoutSeconds is copied into every immutable leaf and covers
	// activation, reachability, verification, and an independent rollback
	// convergence deadline.
	// +kubebuilder:validation:Optional
	// +kubebuilder:validation:Minimum=60
	// +kubebuilder:validation:Maximum=86400
	// +kubebuilder:default=1800
	RebootTimeoutSeconds int32 `json:"rebootTimeoutSeconds,omitempty"`

	// MaintenanceWindow gates each not-yet-claimed device mutation. It cannot
	// cancel an RPC already accepted by the switch.
	// +kubebuilder:validation:Optional
	MaintenanceWindow *UpgradeWindow `json:"maintenanceWindow,omitempty"`

	// Canaries is an explicit, non-empty set of named hardware/capability
	// cohorts. Every distinct operations.cisco.vk/qualification-cohort value in
	// the target set must be represented, and each named device must carry the
	// matching value. Device names refer to CiscoDevices in this namespace.
	// +kubebuilder:validation:Required
	// +kubebuilder:validation:MinItems=1
	// +kubebuilder:validation:MaxItems=16
	// +listType=map
	// +listMapKey=name
	Canaries []IOSXESoftwareRolloutCanaryCohort `json:"canaries"`

	// PauseAfterCanary requires an explicit newer control revision to resume
	// after the complete canary set passes its health and soak gates.
	// +kubebuilder:validation:Optional
	// +kubebuilder:default=true
	PauseAfterCanary *bool `json:"pauseAfterCanary,omitempty"`

	// Budgets independently limit transfer pressure and disruption risk. Until
	// a durable stage boundary exists, every active leaf consumes both kinds.
	// +kubebuilder:validation:Required
	Budgets IOSXESoftwareRolloutBudgetSpec `json:"budgets"`

	// Workloads defines the safe Phase 2 behavior for device-hosted workloads.
	// +kubebuilder:validation:Required
	Workloads IOSXESoftwareRolloutWorkloadSpec `json:"workloads"`

	// Health defines freshness and soak gates for admission and progression.
	// +kubebuilder:validation:Required
	Health IOSXESoftwareRolloutHealthSpec `json:"health"`
}

// IOSXESoftwareRolloutStrategy is intentionally IOS-XE- and MVP-specific.
//
// +kubebuilder:validation:Enum=Reload
type IOSXESoftwareRolloutStrategy string

const (
	IOSXESoftwareRolloutStrategyReload IOSXESoftwareRolloutStrategy = "Reload"
)

// IOSXESoftwareRolloutLabelSelector preserves the Kubernetes LabelSelector
// wire shape while adding hard collection and string bounds that the external
// metav1 type does not publish in this CRD. Controllers can convert it directly
// to metav1.LabelSelector before calling LabelSelectorAsSelector.
type IOSXESoftwareRolloutLabelSelector struct {
	// MatchLabels is an AND of equality requirements.
	// +kubebuilder:validation:Optional
	// +kubebuilder:validation:MaxProperties=32
	// +kubebuilder:validation:XValidation:rule="self.all(k, v, k.size() <= 96 && v.size() <= 63)",message="matchLabels keys and values are bounded"
	MatchLabels map[string]string `json:"matchLabels,omitempty"`

	// MatchExpressions is an AND of bounded set-based requirements.
	// +kubebuilder:validation:Optional
	// +kubebuilder:validation:MaxItems=32
	MatchExpressions []IOSXESoftwareRolloutLabelSelectorRequirement `json:"matchExpressions,omitempty"`
}

// AsLabelSelector returns the equivalent Kubernetes selector with independent
// map and slice storage for controller use.
func (s IOSXESoftwareRolloutLabelSelector) AsLabelSelector() metav1.LabelSelector {
	out := metav1.LabelSelector{}
	if s.MatchLabels != nil {
		out.MatchLabels = make(map[string]string, len(s.MatchLabels))
		for key, value := range s.MatchLabels {
			out.MatchLabels[key] = value
		}
	}
	if s.MatchExpressions != nil {
		out.MatchExpressions = make([]metav1.LabelSelectorRequirement, len(s.MatchExpressions))
		for i := range s.MatchExpressions {
			out.MatchExpressions[i] = metav1.LabelSelectorRequirement{
				Key:      s.MatchExpressions[i].Key,
				Operator: s.MatchExpressions[i].Operator,
				Values:   append([]string(nil), s.MatchExpressions[i].Values...),
			}
		}
	}
	return out
}

// IOSXESoftwareRolloutLabelSelectorRequirement is one bounded Kubernetes
// label selector requirement.
//
// +kubebuilder:validation:XValidation:rule="self.operator in ['In', 'NotIn'] ? has(self.values) && self.values.size() > 0 : !has(self.values) || self.values.size() == 0",message="In and NotIn require values; Exists and DoesNotExist forbid values"
type IOSXESoftwareRolloutLabelSelectorRequirement struct {
	// Key is a Kubernetes qualified label name.
	// +kubebuilder:validation:Required
	// +kubebuilder:validation:MinLength=1
	// +kubebuilder:validation:MaxLength=96
	Key string `json:"key"`

	// Operator is a Kubernetes set-based selector operator.
	// +kubebuilder:validation:Required
	// +kubebuilder:validation:Enum=In;NotIn;Exists;DoesNotExist
	Operator metav1.LabelSelectorOperator `json:"operator"`

	// Values is required and non-empty for In/NotIn and empty for
	// Exists/DoesNotExist.
	// +kubebuilder:validation:Optional
	// +kubebuilder:validation:MaxItems=32
	// +kubebuilder:validation:items:MaxLength=63
	// +kubebuilder:validation:items:Pattern=`^[A-Za-z0-9]([-A-Za-z0-9_.]*[A-Za-z0-9])?$`
	// +listType=set
	Values []string `json:"values,omitempty"`
}

// IOSXESoftwareRolloutTargetSpec bounds and authorizes target selection.
//
// +kubebuilder:validation:XValidation:rule="self.allowAll ? !(has(self.selector.matchLabels) && self.selector.matchLabels.size() > 0) && !(has(self.selector.matchExpressions) && self.selector.matchExpressions.size() > 0) : (has(self.selector.matchLabels) && self.selector.matchLabels.size() > 0) || (has(self.selector.matchExpressions) && self.selector.matchExpressions.size() > 0)",message="set a non-empty selector, or set allowAll=true with an empty selector"
type IOSXESoftwareRolloutTargetSpec struct {
	// Selector matches CiscoDevice metadata labels in this rollout's namespace.
	// +kubebuilder:validation:Required
	Selector IOSXESoftwareRolloutLabelSelector `json:"selector"`

	// AllowAll is the explicit acknowledgement required for an empty selector.
	// +kubebuilder:validation:Optional
	// +kubebuilder:default=false
	AllowAll bool `json:"allowAll,omitempty"`

	// MaxTargets tightens the built-in 100-target ceiling. Administrator policy
	// may impose a smaller effective value.
	// +kubebuilder:validation:Optional
	// +kubebuilder:validation:Minimum=1
	// +kubebuilder:validation:Maximum=100
	// +kubebuilder:default=100
	MaxTargets int32 `json:"maxTargets,omitempty"`
}

// IOSXESoftwareRolloutCanaryCohort names the exact devices qualifying one
// protected operations.cisco.vk/qualification-cohort value before wider
// admission.
type IOSXESoftwareRolloutCanaryCohort struct {
	// Name is the stable, low-cardinality qualification-cohort label value.
	// +kubebuilder:validation:Required
	// +kubebuilder:validation:MinLength=1
	// +kubebuilder:validation:MaxLength=63
	// +kubebuilder:validation:Pattern=`^[a-z0-9]([-a-z0-9]*[a-z0-9])?$`
	Name string `json:"name"`

	// Devices explicitly names CiscoDevices in the campaign namespace. The
	// planner rejects names outside Targets and names repeated across cohorts.
	// +kubebuilder:validation:Required
	// +kubebuilder:validation:MinItems=1
	// +kubebuilder:validation:MaxItems=100
	// +kubebuilder:validation:items:MaxLength=253
	// +kubebuilder:validation:items:Pattern=`^[a-z0-9]([-a-z0-9]*[a-z0-9])?(\.[a-z0-9]([-a-z0-9]*[a-z0-9])?)*$`
	// +listType=set
	Devices []string `json:"devices"`
}

// IOSXESoftwareRolloutSourceSpec is the singular Phase 2 artifact source.
// HTTPS relies on verified TLS. SFTP credentials and verified known-host data
// come from an endpoint-bound Secret. Redirects and URL credentials are not
// represented and must remain disabled by the resolver.
//
// +kubebuilder:validation:XValidation:rule="!has(self.urlSecretRef) || self.urlSecretRef.name.size() > 0",message="urlSecretRef.name must not be empty"
// +kubebuilder:validation:XValidation:rule="!has(self.urlSecretRef) || self.urlSecretRef.name.size() <= 253",message="urlSecretRef.name must contain at most 253 characters"
// +kubebuilder:validation:XValidation:rule="!has(self.urlSecretRef) || self.url.startsWith('sftp://')",message="urlSecretRef is supported only for sftp sources in the Phase 2 API"
// +kubebuilder:validation:XValidation:rule="!self.url.startsWith('sftp://') || has(self.urlSecretRef)",message="sftp sources require an endpoint-bound urlSecretRef"
type IOSXESoftwareRolloutSourceSpec struct {
	// URL is one HTTPS or SFTP image URI with no user information, query, or
	// fragment. Runtime endpoint and resolved-address policy remains mandatory.
	// +kubebuilder:validation:Required
	// +kubebuilder:validation:MaxLength=2048
	// +kubebuilder:validation:Pattern=`^(https|sftp)://[^/?#@]+/[^?#]+$`
	URL string `json:"url"`

	// SHA256 pins identical content throughout the campaign.
	// +kubebuilder:validation:Required
	// +kubebuilder:validation:Pattern=`^[a-f0-9]{64}$`
	SHA256 string `json:"sha256"`

	// ImageFamily is the operator-declared IOS-XE hardware image family. The
	// planner must still prove every target is compatible before approval.
	// +kubebuilder:validation:Required
	// +kubebuilder:validation:MinLength=1
	// +kubebuilder:validation:MaxLength=64
	// +kubebuilder:validation:Pattern=`^[A-Za-z0-9][A-Za-z0-9._-]*$`
	ImageFamily string `json:"imageFamily"`

	// URLSecretRef references an endpoint-bound Secret in this namespace.
	// +kubebuilder:validation:Optional
	URLSecretRef *corev1.LocalObjectReference `json:"urlSecretRef,omitempty"`
}

// IOSXESoftwareRolloutBudgetSpec keeps transfer and disruption authority
// independent even though the MVP conservatively holds both for a whole leaf.
type IOSXESoftwareRolloutBudgetSpec struct {
	// MaxConcurrentTransfers is the campaign-wide transfer ceiling.
	// +kubebuilder:validation:Optional
	// +kubebuilder:validation:Minimum=1
	// +kubebuilder:validation:Maximum=100
	// +kubebuilder:default=1
	MaxConcurrentTransfers int32 `json:"maxConcurrentTransfers,omitempty"`

	// MaxUnavailable is the campaign-wide intentional-disruption ceiling.
	// Existing unhealthy or maintained fleet members also consume policy
	// availability outside this campaign.
	// +kubebuilder:validation:Optional
	// +kubebuilder:validation:Minimum=1
	// +kubebuilder:validation:Maximum=100
	// +kubebuilder:default=1
	MaxUnavailable int32 `json:"maxUnavailable,omitempty"`

	// Domains tightens transfer and/or disruption ceilings for independent
	// topology/risk dimensions. Administrator policy may only tighten further.
	// +kubebuilder:validation:Optional
	// +kubebuilder:validation:MaxItems=16
	// +listType=map
	// +listMapKey=topologyKey
	Domains []IOSXESoftwareRolloutDomainBudget `json:"domains,omitempty"`
}

// IOSXESoftwareRolloutDomainBudget tightens one full-path or independent-risk
// label dimension.
//
// +kubebuilder:validation:XValidation:rule="has(self.maxConcurrentTransfers) || has(self.maxUnavailable)",message="at least one domain budget must be set"
type IOSXESoftwareRolloutDomainBudget struct {
	// TopologyKey is an administrator-allowlisted CiscoDevice metadata label.
	// +kubebuilder:validation:Required
	// +kubebuilder:validation:MinLength=1
	// +kubebuilder:validation:MaxLength=96
	// +kubebuilder:validation:Pattern=`^([a-z0-9]([-a-z0-9.]*[a-z0-9])?/)?[A-Za-z0-9]([-A-Za-z0-9_.]*[A-Za-z0-9])?$`
	TopologyKey string `json:"topologyKey"`

	// MaxConcurrentTransfers is the transfer ceiling for each value/domain.
	// +kubebuilder:validation:Optional
	// +kubebuilder:validation:Minimum=1
	// +kubebuilder:validation:Maximum=100
	MaxConcurrentTransfers *int32 `json:"maxConcurrentTransfers,omitempty"`

	// MaxUnavailable is the unavailable-member ceiling for each value/domain.
	// +kubebuilder:validation:Optional
	// +kubebuilder:validation:Minimum=1
	// +kubebuilder:validation:Maximum=100
	MaxUnavailable *int32 `json:"maxUnavailable,omitempty"`
}

// IOSXESoftwareRolloutWorkloadPolicy is intentionally restricted to the safe
// non-destructive Phase 2 behavior.
//
// +kubebuilder:validation:Enum=BlockIfRunning
type IOSXESoftwareRolloutWorkloadPolicy string

const (
	IOSXESoftwareRolloutWorkloadBlockIfRunning IOSXESoftwareRolloutWorkloadPolicy = "BlockIfRunning"
)

// IOSXESoftwareRolloutWorkloadSpec controls workload handling before a leaf
// mutation. Phase 2 never evicts workloads.
type IOSXESoftwareRolloutWorkloadSpec struct {
	// Policy blocks admission when any workload is running on the bound Node.
	// +kubebuilder:validation:Optional
	// +kubebuilder:validation:Enum=BlockIfRunning
	// +kubebuilder:default=BlockIfRunning
	Policy IOSXESoftwareRolloutWorkloadPolicy `json:"policy,omitempty"`
}

// IOSXESoftwareRolloutHealthSpec defines freshness and soak gates. Missing,
// stale, unknown, or unhealthy observations block new admission. Rollouts
// always require explicit manager observations for NodeIdentityReady,
// TopologyReady, and GNOIConfigurationReady; this API does not accept
// arbitrary conditions without a trusted producer.
type IOSXESoftwareRolloutHealthSpec struct {
	// MaxObservationAgeSeconds is the maximum age accepted for device, Node,
	// and protected-domain health evidence.
	// +kubebuilder:validation:Optional
	// +kubebuilder:validation:Minimum=30
	// +kubebuilder:validation:Maximum=86400
	// +kubebuilder:default=300
	MaxObservationAgeSeconds int32 `json:"maxObservationAgeSeconds,omitempty"`

	// CanarySoakSeconds is the required healthy interval after each canary
	// cohort settles.
	// +kubebuilder:validation:Optional
	// +kubebuilder:validation:Minimum=0
	// +kubebuilder:validation:Maximum=604800
	// +kubebuilder:default=600
	CanarySoakSeconds int32 `json:"canarySoakSeconds,omitempty"`

	// WaveSoakSeconds is the healthy interval before admitting the next
	// non-canary wave.
	// +kubebuilder:validation:Optional
	// +kubebuilder:validation:Minimum=0
	// +kubebuilder:validation:Maximum=604800
	// +kubebuilder:default=300
	WaveSoakSeconds int32 `json:"waveSoakSeconds,omitempty"`
}

// IOSXESoftwareRolloutApproval is append-only authorization for one exact
// frozen plan hash.
type IOSXESoftwareRolloutApproval struct {
	// PlanHash must equal status.frozenPlan.hash at admission time.
	// +kubebuilder:validation:Required
	// +kubebuilder:validation:Pattern=`^sha256:[a-f0-9]{64}$`
	PlanHash string `json:"planHash"`

	// ApprovedBy must be bound to request.userInfo.username by native admission
	// and authorized with the dedicated approve verb.
	// +kubebuilder:validation:Required
	// +kubebuilder:validation:MinLength=1
	// +kubebuilder:validation:MaxLength=253
	ApprovedBy string `json:"approvedBy"`

	// ApprovedAt records when the exact plan was authorized.
	// +kubebuilder:validation:Required
	ApprovedAt metav1.Time `json:"approvedAt"`
}

// IOSXESoftwareRolloutControl is the mutable, monotonic campaign control.
//
// +kubebuilder:validation:XValidation:rule="self.revision >= oldSelf.revision",message="control revision cannot decrease"
// +kubebuilder:validation:XValidation:rule="self == oldSelf || self.revision > oldSelf.revision",message="a control change requires a larger revision"
// +kubebuilder:validation:XValidation:rule="!has(oldSelf.cancel) || !oldSelf.cancel || (has(self.cancel) && self.cancel)",message="cancellation is terminal"
// +kubebuilder:validation:XValidation:rule="self.revision == 0 ? ((!has(self.pause) || !self.pause) && (!has(self.cancel) || !self.cancel) && !has(self.requestedBy) && !has(self.requestedAt) && !has(self.reason)) : (has(self.requestedBy) && self.requestedBy.size() > 0 && has(self.requestedAt))",message="revision zero is neutral; non-zero controls require requester identity and time"
type IOSXESoftwareRolloutControl struct {
	// Revision is increased for every pause, resume, or cancellation request.
	// +kubebuilder:validation:Required
	// +kubebuilder:validation:Minimum=0
	Revision int64 `json:"revision"`

	// Pause prevents new leaf mutation claims. Already claimed work remains
	// observable and retains reservations.
	// +kubebuilder:validation:Optional
	Pause bool `json:"pause,omitempty"`

	// Cancel terminally revokes future claims and requests safe settlement.
	// +kubebuilder:validation:Optional
	Cancel bool `json:"cancel,omitempty"`

	// RequestedBy must be bound to request.userInfo.username on control writes.
	// +kubebuilder:validation:Optional
	// +kubebuilder:validation:MinLength=1
	// +kubebuilder:validation:MaxLength=253
	RequestedBy string `json:"requestedBy,omitempty"`

	// RequestedAt records the control request time.
	// +kubebuilder:validation:Optional
	RequestedAt *metav1.Time `json:"requestedAt,omitempty"`

	// Reason is a bounded human-readable audit explanation.
	// +kubebuilder:validation:Optional
	// +kubebuilder:validation:MaxLength=256
	Reason string `json:"reason,omitempty"`
}

// IOSXESoftwareRolloutPhase is the coarse manager campaign phase.
//
// +kubebuilder:validation:Enum=AwaitingApproval;Paused;Executing;Soaking;Cancelling;Succeeded;Failed;Cancelled
type IOSXESoftwareRolloutPhase string

const (
	IOSXESoftwareRolloutPhaseAwaitingApproval IOSXESoftwareRolloutPhase = "AwaitingApproval"
	IOSXESoftwareRolloutPhasePaused           IOSXESoftwareRolloutPhase = "Paused"
	IOSXESoftwareRolloutPhaseExecuting        IOSXESoftwareRolloutPhase = "Executing"
	IOSXESoftwareRolloutPhaseSoaking          IOSXESoftwareRolloutPhase = "Soaking"
	IOSXESoftwareRolloutPhaseCancelling       IOSXESoftwareRolloutPhase = "Cancelling"
	IOSXESoftwareRolloutPhaseSucceeded        IOSXESoftwareRolloutPhase = "Succeeded"
	IOSXESoftwareRolloutPhaseFailed           IOSXESoftwareRolloutPhase = "Failed"
	IOSXESoftwareRolloutPhaseCancelled        IOSXESoftwareRolloutPhase = "Cancelled"
)

// IOSXESoftwareRolloutStatus is a bounded manager-owned audit and progress
// surface. Detailed history belongs in retained immutable leaves, Events, and
// traces rather than an ever-growing campaign status.
//
// +kubebuilder:validation:XValidation:rule="!has(self.conditions) || self.conditions.all(c, c.type.size() <= 63 && c.reason.size() <= 128 && c.message.size() <= 512)",message="condition type, reason, and message fields must remain bounded"
// +kubebuilder:validation:XValidation:rule="!has(oldSelf.frozenPlan) || (has(self.frozenPlan) && self.frozenPlan == oldSelf.frozenPlan)",message="frozen plan cannot be removed or replaced once published"
// +kubebuilder:validation:XValidation:rule="!has(oldSelf.effectivePolicy) || (has(self.effectivePolicy) && self.effectivePolicy.epoch >= oldSelf.effectivePolicy.epoch)",message="effective policy epoch cannot decrease or be removed"
// +kubebuilder:validation:XValidation:rule="!has(self.policyTransition) || (has(self.effectivePolicy) && self.policyTransition.epoch == self.effectivePolicy.epoch + 1)",message="policy transition must target the next effective epoch"
type IOSXESoftwareRolloutStatus struct {
	// ObservedGeneration is the spec generation reconciled by the manager.
	// +kubebuilder:validation:Optional
	// +kubebuilder:validation:Minimum=0
	ObservedGeneration int64 `json:"observedGeneration,omitempty"`

	// Phase is the coarse campaign phase.
	// +kubebuilder:validation:Optional
	Phase IOSXESoftwareRolloutPhase `json:"phase,omitempty"`

	// FrozenPlan is the immutable, canonical manager plan awaiting exact-hash
	// approval. Membership never expands or shrinks after it is recorded.
	// +kubebuilder:validation:Optional
	FrozenPlan *IOSXESoftwareRolloutFrozenPlanStatus `json:"frozenPlan,omitempty"`

	// EffectivePolicy is the durable admission epoch currently allowed to
	// create or grant unclaimed leaves. Its numeric ceilings only become more
	// restrictive over the lifetime of this rollout.
	// +kubebuilder:validation:Optional
	EffectivePolicy *IOSXESoftwareRolloutEffectivePolicyStatus `json:"effectivePolicy,omitempty"`

	// PolicyTransition is persisted before any leaf from the preceding epoch is
	// fenced. While present, all new reservations and grants are denied.
	// +kubebuilder:validation:Optional
	PolicyTransition *IOSXESoftwareRolloutPolicyTransitionStatus `json:"policyTransition,omitempty"`

	// Approval reports manager validation of spec.approval.
	// +kubebuilder:validation:Optional
	Approval *IOSXESoftwareRolloutApprovalStatus `json:"approval,omitempty"`

	// Control distinguishes a requested pause/cancel from the effective fence.
	// +kubebuilder:validation:Optional
	Control *IOSXESoftwareRolloutControlStatus `json:"control,omitempty"`

	// Counts is a bounded aggregate over the frozen target set.
	// +kubebuilder:validation:Optional
	Counts IOSXESoftwareRolloutCounts `json:"counts,omitempty"`

	// Targets contains one bounded current summary per frozen target.
	// +kubebuilder:validation:Optional
	// +kubebuilder:validation:MaxItems=100
	// +listType=map
	// +listMapKey=deviceUID
	Targets []IOSXESoftwareRolloutTargetStatus `json:"targets,omitempty"`

	// Conditions carries a bounded set of standard status conditions.
	// +kubebuilder:validation:Optional
	// +kubebuilder:validation:MaxItems=16
	// +patchMergeKey=type
	// +patchStrategy=merge
	// +listType=map
	// +listMapKey=type
	Conditions []metav1.Condition `json:"conditions,omitempty"`

	// Message is a bounded human-readable campaign summary.
	// +kubebuilder:validation:Optional
	// +kubebuilder:validation:MaxLength=512
	Message string `json:"message,omitempty"`
}

// IOSXESoftwareRolloutEffectivePolicyStatus is the published safety epoch used
// by reservations, manager admissions, worker acknowledgements, and claims.
type IOSXESoftwareRolloutEffectivePolicyStatus struct {
	// Epoch increases exactly once after every converged semantic policy change.
	// +kubebuilder:validation:Required
	// +kubebuilder:validation:Minimum=1
	Epoch int64 `json:"epoch"`

	// Policy contains the monotonic effective ceilings and the identity of the
	// latest same-UID administrator policy observed for this epoch.
	// +kubebuilder:validation:Required
	Policy IOSXESoftwareRolloutPolicySnapshot `json:"policy"`

	// UpdatedAt records publication of this effective epoch.
	// +kubebuilder:validation:Required
	UpdatedAt metav1.Time `json:"updatedAt"`
}

// IOSXESoftwareRolloutPolicyTransitionStatus is a durable admission barrier.
// The manager clears it only after every zero-claim leaf from the preceding
// epoch is revoked and its reservation is released.
type IOSXESoftwareRolloutPolicyTransitionStatus struct {
	// Epoch is the next epoch and must be greater than the published one.
	// +kubebuilder:validation:Required
	// +kubebuilder:validation:Minimum=2
	Epoch int64 `json:"epoch"`

	// Policy is the next monotonic effective policy to publish after fencing.
	// +kubebuilder:validation:Required
	Policy IOSXESoftwareRolloutPolicySnapshot `json:"policy"`

	// StartedAt is stable across crash recovery.
	// +kubebuilder:validation:Required
	StartedAt metav1.Time `json:"startedAt"`
}

// IOSXESoftwareRolloutFrozenPlanStatus is immutable once published.
//
// +kubebuilder:validation:XValidation:rule="self == oldSelf",message="frozen plan is immutable"
type IOSXESoftwareRolloutFrozenPlanStatus struct {
	// Hash is the SHA-256 of canonical executable intent, target/source
	// snapshots, and administrator policy identity/version.
	// +kubebuilder:validation:Required
	// +kubebuilder:validation:Pattern=`^sha256:[a-f0-9]{64}$`
	Hash string `json:"hash"`

	// EncodedSizeBytes records the canonical JSON byte size checked by the
	// planner before publishing this plan.
	// +kubebuilder:validation:Required
	// +kubebuilder:validation:Minimum=1
	// +kubebuilder:validation:Maximum=262144
	EncodedSizeBytes int32 `json:"encodedSizeBytes"`

	// CreatedAt is when the manager froze the target and policy snapshot.
	// +kubebuilder:validation:Required
	CreatedAt metav1.Time `json:"createdAt"`

	// CampaignGeneration is the rollout generation included in the plan hash.
	// +kubebuilder:validation:Required
	// +kubebuilder:validation:Minimum=1
	CampaignGeneration int64 `json:"campaignGeneration"`

	// Policy binds the administrator-owned policy object and version.
	// +kubebuilder:validation:Required
	Policy IOSXESoftwareRolloutPolicySnapshot `json:"policy"`

	// Source is the endpoint- and Secret-identity-bound source snapshot.
	// +kubebuilder:validation:Required
	Source IOSXESoftwareRolloutSourceSnapshot `json:"source"`

	// Targets contains the complete immutable target/topology snapshot.
	// +kubebuilder:validation:Required
	// +kubebuilder:validation:MinItems=1
	// +kubebuilder:validation:MaxItems=100
	// +listType=map
	// +listMapKey=deviceUID
	Targets []IOSXESoftwareRolloutPlannedTarget `json:"targets"`
}

// IOSXESoftwareRolloutPolicySnapshot binds approval to administrator policy.
type IOSXESoftwareRolloutPolicySnapshot struct {
	// Name is the policy ConfigMap name configured for this manager fleet.
	// +kubebuilder:validation:Required
	// +kubebuilder:validation:MinLength=1
	// +kubebuilder:validation:MaxLength=253
	Name string `json:"name"`

	// Namespace is the policy ConfigMap namespace.
	// +kubebuilder:validation:Required
	// +kubebuilder:validation:MinLength=1
	// +kubebuilder:validation:MaxLength=63
	Namespace string `json:"namespace"`

	// UID rejects a deleted and recreated same-name policy.
	// +kubebuilder:validation:Required
	// +kubebuilder:validation:MinLength=1
	// +kubebuilder:validation:MaxLength=128
	UID string `json:"uid"`

	// ResourceVersion is the exact effective policy version included in Hash.
	// +kubebuilder:validation:Required
	// +kubebuilder:validation:MinLength=1
	// +kubebuilder:validation:MaxLength=128
	ResourceVersion string `json:"resourceVersion"`

	// SemanticHash covers the validated administrator policy payload but no
	// Kubernetes metadata. It distinguishes a real policy edit from metadata-
	// only resourceVersion churn.
	// +kubebuilder:validation:Required
	// +kubebuilder:validation:Pattern=`^sha256:[a-f0-9]{64}$`
	SemanticHash string `json:"semanticHash"`

	// StructuralHash covers selector, required/projected topology, domain-key
	// sets, policy format, and ledger identity. A change requires a new plan.
	// +kubebuilder:validation:Required
	// +kubebuilder:validation:Pattern=`^sha256:[a-f0-9]{64}$`
	StructuralHash string `json:"structuralHash"`

	// MaxTargets is the administrator ceiling frozen into this approval. The
	// effective limit is the minimum of this value, the immutable campaign
	// request, and the current policy value.
	// +kubebuilder:validation:Required
	// +kubebuilder:validation:Minimum=1
	// +kubebuilder:validation:Maximum=100
	MaxTargets int32 `json:"maxTargets"`

	// MaxConcurrentTransfers is the frozen fleet-wide transfer ceiling.
	// +kubebuilder:validation:Required
	// +kubebuilder:validation:Minimum=1
	// +kubebuilder:validation:Maximum=100
	MaxConcurrentTransfers int32 `json:"maxConcurrentTransfers"`

	// MaxUnavailable is the frozen fleet-wide unavailable-member ceiling.
	// +kubebuilder:validation:Required
	// +kubebuilder:validation:Minimum=1
	// +kubebuilder:validation:Maximum=100
	MaxUnavailable int32 `json:"maxUnavailable"`

	// Domains is the bounded frozen set of administrator transfer and
	// unavailable ceilings. Runtime admission applies the strictest value from
	// this snapshot, the current administrator policy, and the campaign plan.
	// +kubebuilder:validation:Optional
	// +kubebuilder:validation:MaxItems=16
	// +listType=map
	// +listMapKey=topologyKey
	Domains []IOSXESoftwareRolloutDomainBudget `json:"domains,omitempty"`

	// HealthFreshnessSeconds is the maximum age of fleet-health evidence in the
	// approved administrator policy.
	// +kubebuilder:validation:Required
	// +kubebuilder:validation:Minimum=30
	// +kubebuilder:validation:Maximum=86400
	HealthFreshnessSeconds int32 `json:"healthFreshnessSeconds"`

	// LedgerNamespace is the namespace of the single authoritative reservation
	// ConfigMap.
	// +kubebuilder:validation:Required
	// +kubebuilder:validation:MinLength=1
	// +kubebuilder:validation:MaxLength=63
	// +kubebuilder:validation:Pattern=`^[a-z0-9]([-a-z0-9]*[a-z0-9])?$`
	LedgerNamespace string `json:"ledgerNamespace"`

	// LedgerName is the name of the single authoritative reservation ConfigMap.
	// +kubebuilder:validation:Required
	// +kubebuilder:validation:MinLength=1
	// +kubebuilder:validation:MaxLength=253
	// +kubebuilder:validation:Pattern=`^[a-z0-9]([-a-z0-9]*[a-z0-9])?(\.[a-z0-9]([-a-z0-9]*[a-z0-9])?)*$`
	LedgerName string `json:"ledgerName"`

	// LedgerUID freezes the ConfigMap incarnation. A missing or recreated
	// ledger blocks all admission and recovery rather than establishing empty
	// authority.
	// +kubebuilder:validation:Required
	// +kubebuilder:validation:MinLength=1
	// +kubebuilder:validation:MaxLength=128
	LedgerUID string `json:"ledgerUID"`

	// MaxActiveReservations is the frozen administrator ledger-record ceiling.
	// +kubebuilder:validation:Required
	// +kubebuilder:validation:Minimum=1
	// +kubebuilder:validation:Maximum=256
	MaxActiveReservations int32 `json:"maxActiveReservations"`

	// MaxLedgerSizeBytes is the frozen encoded-ledger ceiling, capped below the
	// Kubernetes ConfigMap object limit.
	// +kubebuilder:validation:Required
	// +kubebuilder:validation:Minimum=4096
	// +kubebuilder:validation:Maximum=262144
	MaxLedgerSizeBytes int32 `json:"maxLedgerSizeBytes"`
}

// IOSXESoftwareRolloutSourceSnapshot freezes the concrete singular endpoint,
// digest, and optional endpoint-bound Secret incarnation. Credential and
// known-host material may rotate in place; its endpoint authorization is
// revalidated against URL whenever the source is admitted or used.
//
// +kubebuilder:validation:XValidation:rule="self.url.startsWith('sftp://') ? (has(self.secretName) && has(self.secretUID)) : (!has(self.secretName) && !has(self.secretUID))",message="sftp snapshots require an exact Secret incarnation; HTTPS snapshots must not carry deferred HTTP credentials"
type IOSXESoftwareRolloutSourceSnapshot struct {
	// URL is copied from the immutable plan after canonical validation.
	// +kubebuilder:validation:Required
	// +kubebuilder:validation:MaxLength=2048
	// +kubebuilder:validation:Pattern=`^(https|sftp)://[^/?#@]+/[^?#]+$`
	URL string `json:"url"`

	// SHA256 is the pinned lowercase digest.
	// +kubebuilder:validation:Required
	// +kubebuilder:validation:Pattern=`^[a-f0-9]{64}$`
	SHA256 string `json:"sha256"`

	// SecretName is empty only for an anonymous verified-HTTPS source.
	// +kubebuilder:validation:Optional
	// +kubebuilder:validation:MinLength=1
	// +kubebuilder:validation:MaxLength=253
	SecretName string `json:"secretName,omitempty"`

	// SecretUID binds credentials to an exact Secret incarnation.
	// +kubebuilder:validation:Optional
	// +kubebuilder:validation:MinLength=1
	// +kubebuilder:validation:MaxLength=128
	SecretUID string `json:"secretUID,omitempty"`
}

// IOSXESoftwareRolloutTopologyValue is one bounded, sorted effective label
// included in a target snapshot.
type IOSXESoftwareRolloutTopologyValue struct {
	// Key is an administrator-allowlisted topology or risk-domain label key.
	// +kubebuilder:validation:Required
	// +kubebuilder:validation:MinLength=1
	// +kubebuilder:validation:MaxLength=96
	// +kubebuilder:validation:Pattern=`^([a-z0-9]([-a-z0-9.]*[a-z0-9])?/)?[A-Za-z0-9]([-A-Za-z0-9_.]*[A-Za-z0-9])?$`
	Key string `json:"key"`

	// Value is the normalized label value.
	// +kubebuilder:validation:Required
	// +kubebuilder:validation:MinLength=1
	// +kubebuilder:validation:MaxLength=63
	// +kubebuilder:validation:Pattern=`^[A-Za-z0-9]([-A-Za-z0-9_.]*[A-Za-z0-9])?$`
	Value string `json:"value"`
}

// IOSXESoftwareRolloutPlannedTarget freezes one exact device/Node identity and
// topology snapshot. Physical identity is mandatory for managed rollout mode.
type IOSXESoftwareRolloutPlannedTarget struct {
	// DeviceName is the CiscoDevice name in the rollout namespace.
	// +kubebuilder:validation:Required
	// +kubebuilder:validation:MinLength=1
	// +kubebuilder:validation:MaxLength=253
	DeviceName string `json:"deviceName"`

	// DeviceUID rejects delete/recreate identity substitution.
	// +kubebuilder:validation:Required
	// +kubebuilder:validation:MinLength=1
	// +kubebuilder:validation:MaxLength=128
	DeviceUID string `json:"deviceUID"`

	// DeviceGeneration binds every executable CiscoDevice spec field,
	// including driver and management endpoint configuration, to the approved
	// plan. Secret contents may rotate independently without changing it.
	// +kubebuilder:validation:Required
	// +kubebuilder:validation:Minimum=1
	DeviceGeneration int64 `json:"deviceGeneration"`

	// PhysicalIdentity is the verified stable serial or equivalent identifier.
	// +kubebuilder:validation:Required
	// +kubebuilder:validation:MinLength=1
	// +kubebuilder:validation:MaxLength=128
	PhysicalIdentity string `json:"physicalIdentity"`

	// NodeName is the controller-resolved Node identity.
	// +kubebuilder:validation:Required
	// +kubebuilder:validation:MinLength=1
	// +kubebuilder:validation:MaxLength=63
	// +kubebuilder:validation:Pattern=`^[a-z0-9]([-a-z0-9]*[a-z0-9])?(\.[a-z0-9]([-a-z0-9]*[a-z0-9])?)*$`
	NodeName string `json:"nodeName"`

	// NodeUID rejects a foreign or recreated same-name Node.
	// +kubebuilder:validation:Required
	// +kubebuilder:validation:MinLength=1
	// +kubebuilder:validation:MaxLength=128
	NodeUID string `json:"nodeUID"`

	// Driver must be XE for this IOS-XE-specific public campaign API.
	// +kubebuilder:validation:Required
	// +kubebuilder:validation:Enum=XE
	Driver string `json:"driver"`

	// ImageFamily is the target capability family validated against the source.
	// +kubebuilder:validation:Required
	// +kubebuilder:validation:MinLength=1
	// +kubebuilder:validation:MaxLength=64
	ImageFamily string `json:"imageFamily"`

	// QualificationCohort is the protected operator-declared hardware and
	// lifecycle-capability cohort that this exact image must qualify. Every
	// distinct cohort in a frozen plan has at least one explicit canary.
	// +kubebuilder:validation:Required
	// +kubebuilder:validation:MinLength=1
	// +kubebuilder:validation:MaxLength=63
	// +kubebuilder:validation:Pattern=`^[A-Za-z0-9]([-A-Za-z0-9_.]*[A-Za-z0-9])?$`
	QualificationCohort string `json:"qualificationCohort"`

	// WorkerProtocolVersion is the manager/worker protocol proven before plan
	// approval; older workers cannot receive a managed leaf.
	// +kubebuilder:validation:Required
	// +kubebuilder:validation:MinLength=1
	// +kubebuilder:validation:MaxLength=32
	WorkerProtocolVersion string `json:"workerProtocolVersion"`

	// ProjectionHash binds this target to the effective topology projection.
	// +kubebuilder:validation:Required
	// +kubebuilder:validation:Pattern=`^sha256:[a-f0-9]{64}$`
	ProjectionHash string `json:"projectionHash"`

	// Topology contains the sorted effective topology and risk-domain values.
	// +kubebuilder:validation:Required
	// +kubebuilder:validation:MinItems=1
	// +kubebuilder:validation:MaxItems=16
	// +listType=map
	// +listMapKey=key
	Topology []IOSXESoftwareRolloutTopologyValue `json:"topology"`

	// CanaryCohort is non-empty for explicitly selected canaries.
	// +kubebuilder:validation:Optional
	// +kubebuilder:validation:MaxLength=63
	CanaryCohort string `json:"canaryCohort,omitempty"`

	// Wave is a deterministic zero-based admission wave; canaries use wave zero.
	// +kubebuilder:validation:Required
	// +kubebuilder:validation:Minimum=0
	// +kubebuilder:validation:Maximum=100
	Wave int32 `json:"wave"`

	// ChildName is the deterministic retained IOSXESoftwareUpgrade name.
	// +kubebuilder:validation:Required
	// +kubebuilder:validation:MinLength=1
	// +kubebuilder:validation:MaxLength=253
	ChildName string `json:"childName"`
}

// IOSXESoftwareRolloutApprovalStatus reports exact-plan approval validation.
type IOSXESoftwareRolloutApprovalStatus struct {
	// PlanHash is the hash whose approval was evaluated.
	// +kubebuilder:validation:Required
	// +kubebuilder:validation:Pattern=`^sha256:[a-f0-9]{64}$`
	PlanHash string `json:"planHash"`

	// Valid is true only after native authorization and hash equality succeed.
	// +kubebuilder:validation:Required
	Valid bool `json:"valid"`

	// Reason is a bounded machine-readable validation reason.
	// +kubebuilder:validation:Required
	// +kubebuilder:validation:MinLength=1
	// +kubebuilder:validation:MaxLength=128
	Reason string `json:"reason"`

	// ValidatedAt records the latest manager evaluation.
	// +kubebuilder:validation:Required
	ValidatedAt metav1.Time `json:"validatedAt"`
}

// IOSXESoftwareRolloutControlStatus separates requested from effective
// campaign control and makes pause/cancellation races visible.
type IOSXESoftwareRolloutControlStatus struct {
	// RequestedRevision is the latest spec.control revision observed.
	// +kubebuilder:validation:Required
	// +kubebuilder:validation:Minimum=0
	RequestedRevision int64 `json:"requestedRevision"`

	// EffectiveRevision is fenced across every not-yet-claimed leaf mutation.
	// +kubebuilder:validation:Required
	// +kubebuilder:validation:Minimum=0
	EffectiveRevision int64 `json:"effectiveRevision"`

	// PausePending is true while some leaf has not acknowledged the requested
	// pause revision.
	PausePending bool `json:"pausePending,omitempty"`

	// Paused is true when the requested pause is effective for future claims.
	Paused bool `json:"paused,omitempty"`

	// CancellationPending is true while claims/outcomes are unresolved.
	CancellationPending bool `json:"cancellationPending,omitempty"`

	// Cancelled is true only after future claims are revoked and all accepted
	// work has reached a reconciled outcome.
	Cancelled bool `json:"cancelled,omitempty"`
}

// IOSXESoftwareRolloutCounts is bounded by the frozen target ceiling.
type IOSXESoftwareRolloutCounts struct {
	// +kubebuilder:validation:Minimum=0
	// +kubebuilder:validation:Maximum=100
	Total int32 `json:"total,omitempty"`
	// +kubebuilder:validation:Minimum=0
	// +kubebuilder:validation:Maximum=100
	Planned int32 `json:"planned,omitempty"`
	// +kubebuilder:validation:Minimum=0
	// +kubebuilder:validation:Maximum=100
	Admitted int32 `json:"admitted,omitempty"`
	// +kubebuilder:validation:Minimum=0
	// +kubebuilder:validation:Maximum=100
	InProgress int32 `json:"inProgress,omitempty"`
	// +kubebuilder:validation:Minimum=0
	// +kubebuilder:validation:Maximum=100
	Succeeded int32 `json:"succeeded,omitempty"`
	// +kubebuilder:validation:Minimum=0
	// +kubebuilder:validation:Maximum=100
	Failed int32 `json:"failed,omitempty"`
	// +kubebuilder:validation:Minimum=0
	// +kubebuilder:validation:Maximum=100
	Blocked int32 `json:"blocked,omitempty"`
	// +kubebuilder:validation:Minimum=0
	// +kubebuilder:validation:Maximum=100
	Cancelled int32 `json:"cancelled,omitempty"`
}

// IOSXESoftwareRolloutTargetPhase is a bounded per-target summary state.
//
// +kubebuilder:validation:Enum=Planned;WaitingForAdmission;Admitted;Running;Soaking;Succeeded;Failed;Blocked;Cancelling;Cancelled
type IOSXESoftwareRolloutTargetPhase string

const (
	IOSXESoftwareRolloutTargetPlanned             IOSXESoftwareRolloutTargetPhase = "Planned"
	IOSXESoftwareRolloutTargetWaitingForAdmission IOSXESoftwareRolloutTargetPhase = "WaitingForAdmission"
	IOSXESoftwareRolloutTargetAdmitted            IOSXESoftwareRolloutTargetPhase = "Admitted"
	IOSXESoftwareRolloutTargetRunning             IOSXESoftwareRolloutTargetPhase = "Running"
	IOSXESoftwareRolloutTargetSoaking             IOSXESoftwareRolloutTargetPhase = "Soaking"
	IOSXESoftwareRolloutTargetSucceeded           IOSXESoftwareRolloutTargetPhase = "Succeeded"
	IOSXESoftwareRolloutTargetFailed              IOSXESoftwareRolloutTargetPhase = "Failed"
	IOSXESoftwareRolloutTargetBlocked             IOSXESoftwareRolloutTargetPhase = "Blocked"
	IOSXESoftwareRolloutTargetCancelling          IOSXESoftwareRolloutTargetPhase = "Cancelling"
	IOSXESoftwareRolloutTargetCancelled           IOSXESoftwareRolloutTargetPhase = "Cancelled"
)

// IOSXESoftwareRolloutTargetStatus summarizes one retained leaf.
type IOSXESoftwareRolloutTargetStatus struct {
	// DeviceName is the planned CiscoDevice name.
	// +kubebuilder:validation:Required
	// +kubebuilder:validation:MinLength=1
	// +kubebuilder:validation:MaxLength=253
	DeviceName string `json:"deviceName"`

	// DeviceUID is the list-map identity and exact target incarnation.
	// +kubebuilder:validation:Required
	// +kubebuilder:validation:MinLength=1
	// +kubebuilder:validation:MaxLength=128
	DeviceUID string `json:"deviceUID"`

	// LeafName is the deterministic IOSXESoftwareUpgrade name.
	// +kubebuilder:validation:Optional
	// +kubebuilder:validation:MaxLength=253
	LeafName string `json:"leafName,omitempty"`

	// LeafUID binds progress to the created child incarnation.
	// +kubebuilder:validation:Optional
	// +kubebuilder:validation:MaxLength=128
	LeafUID string `json:"leafUID,omitempty"`

	// Phase is the current bounded target summary.
	// +kubebuilder:validation:Required
	Phase IOSXESoftwareRolloutTargetPhase `json:"phase"`

	// Reason is a bounded machine-readable wait or failure reason.
	// +kubebuilder:validation:Optional
	// +kubebuilder:validation:MaxLength=128
	Reason string `json:"reason,omitempty"`

	// Message is a bounded human-readable target summary.
	// +kubebuilder:validation:Optional
	// +kubebuilder:validation:MaxLength=512
	Message string `json:"message,omitempty"`

	// LastTransitionTime is the latest target summary phase transition.
	// +kubebuilder:validation:Required
	LastTransitionTime metav1.Time `json:"lastTransitionTime"`
}

func init() {
	SchemeBuilder.Register(&IOSXESoftwareRollout{}, &IOSXESoftwareRolloutList{})
}
