// Copyright 2026 Cisco Systems Inc.
// SPDX-License-Identifier: Apache-2.0
package catalystcenter

import (
	"context"
	"encoding/json"
	"reflect"
	"slices"

	configapi "github.com/cisco/virtual-kubelet-cisco/api/config/v1alpha1"
	cvk "github.com/cisco/virtual-kubelet-cisco/api/v1alpha1"
	"github.com/cisco/virtual-kubelet-cisco/internal/controlleradapter"
	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	ctrlclient "sigs.k8s.io/controller-runtime/pkg/client"
)

const discoveryCredentialUnsubmittedPrefix = "credential-discovery-unsubmitted:v1:"

// An exact, never-submitted first preparation is retained as cancellation
// evidence, not as a creator or credential-delivery receipt. Missing journal
// history alone never authorizes this path.
func discoveryCredentialUnsubmittedIdentity(nc *cvk.NetworkController, c *configapi.NetworkControllerConfig, op *cvk.NetworkControllerConfigurationOperation) (*discoveryOwnershipRetirement, error) {
	if nc == nil || c == nil || !discoveryCredentialCleanupObservationShape(c) || nc.Status.Configuration == nil || op == nil || op.Phase != "Prepared" || op.TaskID != "" || op.SubmittedAt != nil || !configurationOperationID.MatchString(op.ID) || discoveryCredentialInputScopePrefix(op.Resource) == "" || controlleradapter.ValidateConfigMetadataContract(nc, c) != nil {
		return nil, errConfigurationTransition
	}
	if _, ancestry, ok := discoveryCredentialInputParts(op.Resource); !ok || ancestry != "" {
		return nil, errConfigurationOwned
	}
	scope := discoveryCredentialScopeForContext(nc, c, op, true)
	if scope == nil || configurationOwnersMatch(nc.Status.Configuration, c) != nil {
		return nil, errConfigurationOwned
	}
	selected, cohort, err := discoveryCredentialUnsubmittedEvidence(nc.Status.Configuration, op)
	if err != nil {
		return nil, err
	}
	closed := nc.DeepCopy()
	closed.Status.Configuration.Operation = nil
	proof := discoveryCompletionIdentity(closed, c)
	proof.SourceUID, proof.SourceVersion, proof.ModelDigest = scope.SourceUID, scope.SourceVersion, scope.ModelDigest
	proof.ScopeDigest = featureIdentityDigest(scope)
	proof.CredentialDigest = discoveryCredentialUnsubmittedPrefix + featureIdentityDigest(selected)
	retired, err := discoveryRetiredRows(closed, "")
	if err != nil {
		return nil, err
	}
	return &discoveryOwnershipRetirement{ConfigName: c.Name, Completion: proof, RetainedDigest: featureIdentityDigest(selected), RetiredDigest: featureIdentityDigest(retired), CredentialHistoryDigest: discoveryCredentialHistorySealPrefix + cohort.allDigest, UnsubmittedOperation: op.DeepCopy()}, nil
}

// Pure metadata validation used by recovery and the existing manager callback.
// The canonical record must include the original Prepared operation; a digest
// or an empty ledger without that witness cannot release a finalizer.
func discoveryCredentialUnsubmittedRecord(nc *cvk.NetworkController, c *configapi.NetworkControllerConfig) (*discoveryOwnershipRetirement, error) {
	if nc == nil || nc.Status.Configuration == nil || nc.Status.Configuration.Operation != nil {
		return nil, errConfigurationBusy
	}
	cond := meta.FindStatusCondition(nc.Status.Conditions, discoveryRetirementCondition)
	if cond == nil || cond.Status != metav1.ConditionTrue || cond.Reason != "DiscoveryConfigClosed" || cond.ObservedGeneration != nc.Generation {
		return nil, errConfigurationOwned
	}
	var record discoveryOwnershipRetirement
	if json.Unmarshal([]byte(cond.Message), &record) != nil {
		return nil, errConfigurationOwned
	}
	expected, err := discoveryCredentialUnsubmittedIdentity(nc, c, record.UnsubmittedOperation)
	if err != nil {
		return nil, err
	}
	if record.CredentialHistoryDigest == "" {
		// Preserve the original manager-only format for genuinely empty history.
		cohort, err := discoveryCredentialHistoryCohort(nc.Status.Configuration, string(c.UID))
		if err != nil || len(cohort.archived) != 0 || len(cohort.current.creators) != 0 {
			return nil, errConfigurationOwned
		}
		expected.CredentialHistoryDigest = ""
	} else if _, _, err := discoveryCredentialSealedUnsubmittedRecord(nc); err != nil {
		return nil, err
	}
	raw, _ := json.Marshal(expected)
	if string(raw) != cond.Message {
		return nil, errConfigurationOwned
	}
	return expected, nil
}

// Validate current source/projections and every declared name twice before
// atomically retaining the witness and clearing the unsubmitted slot. Committed
// proof recovery uses metadata/native observation and never gains a permit.
func (a *adapter) publishUnsubmittedDiscoveryCredentialCompletion(ctx context.Context, c *configapi.NetworkControllerConfig) error {
	nc, err := a.currentController(ctx)
	if err != nil {
		return err
	}
	if nc.Status.Configuration == nil {
		return errConfigurationOwned
	}
	op := nc.Status.Configuration.Operation.DeepCopy()
	if op == nil {
		return a.publishRetainedUnsubmittedDiscoveryCredentialCompletion(ctx, c)
	}
	history, err := a.discoveryCredentialOperationHistory(ctx, nc, op)
	if err != nil {
		return err
	}
	if len(history.creators) != 0 {
		return errConfigurationOwned
	}
	record, err := discoveryCredentialUnsubmittedIdentity(nc, c, op)
	if err != nil {
		return err
	}
	var source *configSourceSnapshot
	var secrets *configurationSecretSnapshot
	for range 2 {
		var fresh configapi.NetworkControllerConfig
		if err := a.statusReader.Get(ctx, ctrlclient.ObjectKeyFromObject(c), &fresh); err != nil {
			return err
		}
		if fresh.UID != c.UID || fresh.Generation != c.Generation || fresh.ResourceVersion != c.ResourceVersion || !reflect.DeepEqual(fresh.Spec, c.Spec) || !reflect.DeepEqual(fresh.DeletionTimestamp, c.DeletionTimestamp) || !reflect.DeepEqual(fresh.Finalizers, c.Finalizers) {
			return errConfigurationStale
		}
		if controlleradapter.ValidateConfigContract(nc, &fresh, a.intentSecretPath) != nil {
			return errConfigurationSecret
		}
		source, err = loadControllerSource(ctx, a.statusReader, &fresh)
		if err != nil {
			return err
		}
		if string(source.uid) != op.SourceUID || source.version != op.SourceVersion || featureIdentityDigest(source.raw) != record.Completion.ModelDigest {
			return errConfigurationStale
		}
		fresh.DeletionTimestamp = nil
		projection, err := a.prepareDiscoveryCredentialProjection(nc, &fresh, source)
		if err != nil {
			return err
		}
		if secrets == nil {
			secrets = source.secrets
		}
		if secrets == nil {
			if discoveryCredentialInputScopePrefix(op.Resource) != discoveryCredentialMetadataScopePrefix || !projection.metadataLifecycle || len(fresh.Spec.SecretRefs) != 0 {
				return errConfigurationSecret
			}
		} else if err := secrets.unchanged(nc, &fresh); err != nil {
			return err
		}
		if len(projection.intents) == 0 {
			return errConfigurationOwned
		}
		var globals map[string]map[string]globalCredentialMetadata
		for _, intent := range projection.intents {
			if len(intent.base.credentials) > 0 {
				globals, err = a.client.globalCredentials(ctx)
				if err != nil {
					return err
				}
				break
			}
		}
		for index, intent := range projection.intents {
			if !projection.tracks(intent) {
				return errConfigurationOwned
			}
			request, input, err := projection.bindRequest(nc, &fresh, source, index, globals)
			if err != nil {
				return err
			}
			if err := discoveryCredentialSuccessorVersions(history, discoveryPublicDigest(intent.base.name), request.versions); err != nil {
				return err
			}
			// With no current-owner receipts, the original planner must have selected the
			// first declaration. Do not relabel another input as this preparation.
			if index == 0 && (input != op.Resource || !slices.Equal(request.versions, op.CredentialVersions)) {
				return errConfigurationOwned
			}
		}
		actual, err := a.client.discoveries(ctx)
		if err != nil {
			return err
		}
		if err := history.absentRetired(actual); err != nil {
			return err
		}
		for _, intent := range projection.intents {
			if !projection.tracks(intent) {
				return errConfigurationOwned
			}
			if _, exists := actual[intent.base.name]; exists {
				return errConfigurationOwned
			}
		}
		current, err := a.currentController(ctx)
		if err != nil {
			return err
		}
		if current.ResourceVersion != nc.ResourceVersion || !reflect.DeepEqual(current.Status, nc.Status) {
			return errConfigurationStale
		}
	}
	// Re-read the source/config after the final native read as well. No mutable
	// observation can change between a GET and retirement without invalidation.
	var fresh configapi.NetworkControllerConfig
	if err := a.statusReader.Get(ctx, ctrlclient.ObjectKeyFromObject(c), &fresh); err != nil {
		return err
	}
	if fresh.ResourceVersion != c.ResourceVersion || fresh.UID != c.UID || fresh.Generation != c.Generation || !reflect.DeepEqual(fresh.Spec, c.Spec) || !reflect.DeepEqual(fresh.Finalizers, c.Finalizers) || !reflect.DeepEqual(fresh.DeletionTimestamp, c.DeletionTimestamp) {
		return errConfigurationStale
	}
	last, err := loadControllerSource(ctx, a.statusReader, &fresh)
	if err != nil {
		return err
	}
	if last.uid != source.uid || last.version != source.version || !reflect.DeepEqual(last.raw, source.raw) {
		return errConfigurationStale
	}
	if controlleradapter.ValidateConfigContract(nc, &fresh, a.intentSecretPath) != nil {
		return errConfigurationSecret
	}
	fresh.DeletionTimestamp = nil
	projection, err := a.prepareDiscoveryCredentialProjection(nc, &fresh, last)
	if err != nil {
		return err
	}
	if secrets == nil {
		if discoveryCredentialInputScopePrefix(op.Resource) != discoveryCredentialMetadataScopePrefix || !projection.metadataLifecycle || len(fresh.Spec.SecretRefs) != 0 {
			return errConfigurationSecret
		}
	} else if err := secrets.unchanged(nc, &fresh); err != nil {
		return err
	}
	if nc.Status.Configuration.Operation != nil {
		// Revalidate active custody immediately before replacing it with the
		// new sealed no-submission closure. Recovery uses that durable closure.
		if _, err := a.discoveryCredentialOperationHistory(ctx, nc, op); err != nil {
			return err
		}
		nc.Status.Configuration.Operation = nil
		raw, _ := json.Marshal(record)
		meta.SetStatusCondition(&nc.Status.Conditions, condition(discoveryRetirementCondition, true, "DiscoveryConfigClosed", string(raw), nc.Generation))
		if err := a.statusWriter.Status().Update(ctx, nc); err != nil {
			return err
		}
	}
	return a.publishObservedDiscoveryConfigCompletion(ctx, c, nc, &record.Completion)
}
