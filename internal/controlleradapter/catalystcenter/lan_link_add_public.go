// Copyright 2026 Cisco Systems Inc.
// SPDX-License-Identifier: Apache-2.0
package catalystcenter

import (
	"context"
	"reflect"
	"sort"
	"time"

	configapi "github.com/cisco/virtual-kubelet-cisco/api/config/v1alpha1"
	cvk "github.com/cisco/virtual-kubelet-cisco/api/v1alpha1"
	contract "github.com/cisco/virtual-kubelet-cisco/internal/controllerconfiguration"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// New link intent uses the same journal, manager-created group reservations and
// one-use permit as other CC group operations. No preparation implies dispatch
// authority. Retained acknowledged and terminal work precede this new-work path.
func (r *configReconciler) evaluateLANLinkApply(ctx context.Context, nc *cvk.NetworkController, config *configapi.NetworkControllerConfig, status *configapi.NetworkControllerConfigStatus) (*configSourceSnapshot, bool, string, string) {
	status.Phase = configapi.NetworkControllerConfigPhaseWaiting
	status.SectionStatus = []configapi.NetworkControllerSectionStatus{{Name: "lan_automation", State: configapi.NetworkControllerSectionStateApplying}}
	source, err := loadControllerSource(ctx, r.a.statusReader, config)
	if err != nil {
		return nil, true, "SourceUnavailable", "Cannot read LAN link intent source"
	}
	lifecycle, err := r.a.publicLANLinkLifecycle(source)
	if err != nil {
		status.Phase = configapi.NetworkControllerConfigPhaseFailed
		status.SectionStatus[0].State = configapi.NetworkControllerSectionStateApplyError
		return source, false, "UnsupportedIntent", "LAN Apply requires a complete supported link model; discovery, conflicting ports or unresolved input cannot be discarded"
	}
	if !r.a.configurationGroupAdmissionReady() {
		return source, true, "GroupAdmissionUnavailable", "LAN link Apply requires the installed all-member configuration runtime"
	}
	work, cancel := context.WithTimeout(ctx, 90*time.Second)
	defer cancel()
	if nc.Status.Configuration != nil && nc.Status.Configuration.Operation != nil {
		op := nc.Status.Configuration.Operation
		if op.Phase != "Prepared" || op.ConfigName != config.Name || op.ConfigUID != string(config.UID) || op.Section != "lan_automation" || contract.ValidateGroupJournalIdentity(op) != nil || !lifecycle.valid(op.Resource, op.Action) {
			return source, true, "GroupOperationPending", "The retained operation must settle or recover before another LAN link can be prepared"
		}
		observed, _, err := lifecycle.observe(work, config, source)
		if err != nil {
			return source, true, "GroupPreparationPending", "Retained LAN preparation awaits the complete unchanged source and all native/managed prerequisites"
		}
		selected := ""
		for name, observation := range observed {
			if !observation.owned && observation.resource == op.Resource && reflect.DeepEqual(observation.targets, op.PhysicalGroup.Targets) {
				if selected != "" {
					return source, true, "GroupPreparationPending", "Retained LAN preparation has an ambiguous source binding"
				}
				selected = name
			}
		}
		if selected == "" {
			cancelled, err := r.a.cancelStaleLANLinkPreparation(work, config, source, op, observed)
			if err != nil {
				return source, true, "GroupReplanningPending", "Stale LAN preparation remains fenced until unchanged scope, complete actors and cancellation authority can be verified"
			}
			if cancelled {
				return source, true, "GroupCancellationPending", "Unused stale LAN preparation is durably cancelling; original member cleanup and settlement must finish before replanning"
			}
			return source, true, "GroupPreparationPending", "Retained LAN preparation no longer matches the complete source or admitted endpoint identities"
		}
		if _, _, err := r.a.revalidateLANLinkMutation(work, config, source, selected, op, "Prepared", lifecycle); err != nil {
			return source, true, "GroupPreparationPending", "Retained LAN preparation requires its original source, controller and physical group"
		}
		permit, err := r.a.claimConfigurationGroup(work)
		if err != nil {
			return source, true, "GroupAdmissionPending", "Waiting for both original device workers, maintenance guards and valid all-member grants"
		}
		if _, err := r.a.dispatchLANLinkMutation(work, config, source, selected, permit, lifecycle); err != nil {
			return source, true, "GroupSubmissionPending", "LAN submission remains journaled; current phase and acknowledgement determine recovery without replay"
		}
		return source, true, "ApplyProgressing", "LAN link update acknowledged; task, device verification and group settlement follow"
	}
	observed, wants, err := lifecycle.observe(work, config, source)
	if err != nil {
		return source, true, "GroupPreparationPending", "Every declared LAN link requires complete native, managed identity and action-specific ownership or prerequisite evidence before preparation"
	}
	names := make([]string, 0, len(observed))
	for name := range observed {
		names = append(names, name)
	}
	sort.Slice(names, func(i, j int) bool {
		left, right := wants[names[i]], wants[names[j]]
		if left.action != right.action {
			return left.action == "DELETE_LINK"
		}
		return names[i] < names[j]
	})
	for _, name := range names {
		if observed[name].owned {
			continue
		}
		if _, err := r.a.prepareLANLinkMutation(work, config, source, name, lifecycle); err != nil {
			return source, true, "GroupPreparationPending", "LAN preparation requires unchanged complete model and native/managed observations"
		}
		return source, true, "GroupPreparationPending", "LAN group journal prepared; waiting for all-member device reservation admission"
	}
	// Current positive native/result equality plus the original independent
	// completion receipt establish idempotency. This is not fresh ISIS attestation.
	checked := metav1.Now()
	status.LastControllerCheck = &checked
	status.ControllerAPIVersion = "v1"
	status.Phase = configapi.NetworkControllerConfigPhaseInSync
	status.SectionStatus[0].State = configapi.NetworkControllerSectionStateInSync
	status.SectionStatus[0].ResourceCount = int64(len(observed))
	return source, true, "InSync", "Declared LAN links retain verified application receipts and match current native configuration"
}
