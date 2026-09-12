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

package topologyrollout

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"sort"
	"strings"
	"time"

	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/labels"
	"k8s.io/apimachinery/pkg/types"
	validation "k8s.io/apimachinery/pkg/util/validation"
	"k8s.io/client-go/util/retry"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

const (
	PolicyVersion             = "v1"
	PolicyDataKey             = "policy.json"
	LedgerUIDAnnotation       = "topology.cisco.vk/ledger-uid"
	PolicyManagedAnnotation   = "topology.cisco.vk/managed-policy"
	AdmissionPrefixAnnotation = "topology.cisco.vk/admission-policy-prefix"
	DefaultMaxCampaignTargets = 100
	MaxProjectedTopologyKeys  = 16
)

// AdminPolicyConfig is intentionally stored as one JSON value so readers see
// one coherent ConfigMap resourceVersion. The ConfigMap UID/resourceVersion,
// not user-provided identity fields, become the effective policy identity.
type AdminPolicyConfig struct {
	Version                      string               `json:"version"`
	FleetSelector                metav1.LabelSelector `json:"fleetSelector"`
	RequiredTopologyKeys         []string             `json:"requiredTopologyKeys"`
	ProjectedTopologyKeys        []string             `json:"projectedTopologyKeys"`
	GlobalMaxConcurrentTransfers int                  `json:"globalMaxConcurrentTransfers"`
	GlobalMaxUnavailable         int                  `json:"globalMaxUnavailable"`
	DomainMaxConcurrentTransfers map[string]int       `json:"domainMaxConcurrentTransfers"`
	DomainMaxUnavailable         map[string]int       `json:"domainMaxUnavailable"`
	HealthFreshnessSeconds       int                  `json:"healthFreshnessSeconds"`
	MaxCampaignTargets           int                  `json:"maxCampaignTargets"`
	MaxActiveReservations        int                  `json:"maxActiveReservations"`
	MaxLedgerBytes               int                  `json:"maxLedgerBytes"`
	LedgerName                   string               `json:"ledgerName"`
}

type ParsedAdminPolicy struct {
	Config          AdminPolicyConfig
	Selector        labels.Selector
	Namespace       string
	Name            string
	LedgerUID       string
	PolicyUID       string
	ResourceVersion string
	HealthFreshness time.Duration
	AdmissionPrefix string
}

// AdminPolicyPreflight is the read-only portion of the policy contract needed
// before the manager is allowed to mutate or bind the safety ledger.
type AdminPolicyPreflight struct {
	Config          AdminPolicyConfig
	AdmissionPrefix string
}

// ReadAdminPolicyPreflight validates the complete chart-owned policy input,
// including selector syntax and admission identity, without writing either
// ConfigMap. Startup must verify native admission using this result before it
// calls BootstrapAdminPolicy.
func ReadAdminPolicyPreflight(
	ctx context.Context,
	reader client.Reader,
	key types.NamespacedName,
) (*AdminPolicyPreflight, error) {
	if reader == nil || key.Namespace == "" || key.Name == "" {
		return nil, fmt.Errorf("administrator topology policy preflight is incomplete")
	}
	var cm corev1.ConfigMap
	if err := reader.Get(ctx, key, &cm); err != nil {
		return nil, fmt.Errorf("read administrator topology policy %s: %w", key, err)
	}
	return inspectAdminPolicy(&cm)
}

func inspectAdminPolicy(cm *corev1.ConfigMap) (*AdminPolicyPreflight, error) {
	cfg, err := decodeAdminPolicyConfig(cm)
	if err != nil {
		return nil, err
	}
	if _, err := metav1.LabelSelectorAsSelector(&cfg.FleetSelector); err != nil {
		return nil, fmt.Errorf("parse fleet selector: %w", err)
	}
	prefix := strings.TrimSpace(cm.Annotations[AdmissionPrefixAnnotation])
	if problems := validation.IsDNS1123Subdomain(prefix); len(problems) > 0 {
		return nil, fmt.Errorf("administrator topology policy has invalid admission policy prefix: %s", strings.Join(problems, "; "))
	}
	return &AdminPolicyPreflight{Config: cfg, AdmissionPrefix: prefix}, nil
}

func ParseAdminPolicy(cm *corev1.ConfigMap) (*ParsedAdminPolicy, error) {
	preflight, err := inspectAdminPolicy(cm)
	if err != nil {
		return nil, err
	}
	cfg := preflight.Config
	ledgerUID := strings.TrimSpace(cm.Annotations[LedgerUIDAnnotation])
	if ledgerUID == "" {
		return nil, fmt.Errorf("administrator topology policy has no bound ledger UID")
	}
	selector, err := metav1.LabelSelectorAsSelector(&cfg.FleetSelector)
	if err != nil {
		return nil, fmt.Errorf("parse fleet selector: %w", err)
	}
	return &ParsedAdminPolicy{
		Config:          cfg,
		Selector:        selector,
		Namespace:       cm.Namespace,
		Name:            cm.Name,
		LedgerUID:       ledgerUID,
		PolicyUID:       string(cm.UID),
		ResourceVersion: cm.ResourceVersion,
		HealthFreshness: time.Duration(cfg.HealthFreshnessSeconds) * time.Second,
		AdmissionPrefix: preflight.AdmissionPrefix,
	}, nil
}

// BootstrapAdminPolicy binds a chart-created policy to the immutable UID of
// its chart-created ledger. Helm cannot know either UID at render time. This
// bootstrap is intentionally one-way: once the annotation exists, a missing
// or recreated ledger is an identity failure and never establishes a fresh
// admission authority.
//
// The ledger is initialized before the policy annotation is published. A
// crash between those writes is safe: the next attempt verifies the already
// initialized ledger against its Kubernetes UID and completes the binding.
func BootstrapAdminPolicy(
	ctx context.Context,
	writer client.Client,
	reader client.Reader,
	key types.NamespacedName,
) (*ParsedAdminPolicy, error) {
	if writer == nil || key.Namespace == "" || key.Name == "" {
		return nil, fmt.Errorf("administrator topology policy bootstrap is incomplete")
	}
	if reader == nil {
		reader = writer
	}

	var result *ParsedAdminPolicy
	err := retry.RetryOnConflict(retry.DefaultBackoff, func() error {
		var policyCM corev1.ConfigMap
		if err := reader.Get(ctx, key, &policyCM); err != nil {
			return fmt.Errorf("read administrator topology policy %s: %w", key, err)
		}
		preflight, err := inspectAdminPolicy(&policyCM)
		if err != nil {
			return err
		}
		cfg := preflight.Config

		ledgerKey := types.NamespacedName{Namespace: key.Namespace, Name: cfg.LedgerName}
		var ledgerCM corev1.ConfigMap
		if err := reader.Get(ctx, ledgerKey, &ledgerCM); err != nil {
			return fmt.Errorf("read rollout ledger %s: %w", ledgerKey, err)
		}
		if ledgerCM.UID == "" || ledgerCM.ResourceVersion == "" {
			return fmt.Errorf("%w: rollout ledger has no Kubernetes identity", ErrLedgerIdentity)
		}

		boundUID := strings.TrimSpace(policyCM.Annotations[LedgerUIDAnnotation])
		if boundUID != "" && boundUID != string(ledgerCM.UID) {
			return fmt.Errorf("%w: policy binds %q but ledger object is %q", ErrLedgerIdentity, boundUID, ledgerCM.UID)
		}

		rawLedger := strings.TrimSpace(ledgerCM.Data[LedgerDataKey])
		if rawLedger == "" {
			if boundUID != "" {
				return fmt.Errorf("%w: bound ledger is empty", ErrLedgerIdentity)
			}
			ledger, err := NewLedger(string(ledgerCM.UID))
			if err != nil {
				return err
			}
			encoded, err := Encode(ledger, cfg.MaxLedgerBytes)
			if err != nil {
				return err
			}
			before := ledgerCM.DeepCopy()
			if ledgerCM.Data == nil {
				ledgerCM.Data = map[string]string{}
			}
			ledgerCM.Data[LedgerDataKey] = string(encoded)
			if err := writer.Patch(ctx, &ledgerCM, client.MergeFromWithOptions(before, client.MergeFromWithOptimisticLock{})); err != nil {
				if apierrors.IsConflict(err) {
					return err
				}
				return fmt.Errorf("initialize rollout ledger %s: %w", ledgerKey, err)
			}
		} else if _, err := Decode([]byte(rawLedger), string(ledgerCM.UID)); err != nil {
			return err
		}

		if boundUID == "" {
			before := policyCM.DeepCopy()
			if policyCM.Annotations == nil {
				policyCM.Annotations = map[string]string{}
			}
			policyCM.Annotations[LedgerUIDAnnotation] = string(ledgerCM.UID)
			if err := writer.Patch(ctx, &policyCM, client.MergeFromWithOptions(before, client.MergeFromWithOptimisticLock{})); err != nil {
				if apierrors.IsConflict(err) {
					return err
				}
				return fmt.Errorf("bind rollout policy to ledger UID: %w", err)
			}
		}

		parsed, err := ParseAdminPolicy(&policyCM)
		if err != nil {
			return err
		}
		result = parsed
		return nil
	})
	if err != nil {
		return nil, err
	}
	return result, nil
}

func decodeAdminPolicyConfig(cm *corev1.ConfigMap) (AdminPolicyConfig, error) {
	if cm == nil || cm.UID == "" || cm.ResourceVersion == "" {
		return AdminPolicyConfig{}, fmt.Errorf("administrator topology policy has no Kubernetes identity")
	}
	if cm.Annotations[PolicyManagedAnnotation] != "true" {
		return AdminPolicyConfig{}, fmt.Errorf("administrator topology policy is not marked managed")
	}
	var cfg AdminPolicyConfig
	decoder := json.NewDecoder(bytes.NewReader([]byte(cm.Data[PolicyDataKey])))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&cfg); err != nil {
		return AdminPolicyConfig{}, fmt.Errorf("decode administrator topology policy: %w", err)
	}
	if err := decoder.Decode(&struct{}{}); err != io.EOF {
		return AdminPolicyConfig{}, fmt.Errorf("decode administrator topology policy: trailing JSON data")
	}
	if err := validateAdminPolicyConfig(&cfg); err != nil {
		return AdminPolicyConfig{}, err
	}
	return cfg, nil
}

func (p *ParsedAdminPolicy) AdmissionPolicy(now time.Time) Policy {
	return Policy{
		UID:                          p.PolicyUID,
		Version:                      p.ResourceVersion,
		Epoch:                        1,
		GlobalMaxConcurrentTransfers: p.Config.GlobalMaxConcurrentTransfers,
		GlobalMaxUnavailable:         p.Config.GlobalMaxUnavailable,
		DomainTransferBudgets:        cloneIntMap(p.Config.DomainMaxConcurrentTransfers),
		DomainBudgets:                cloneIntMap(p.Config.DomainMaxUnavailable),
		MaxActiveRecords:             p.Config.MaxActiveReservations,
		MaxSerializedBytes:           p.Config.MaxLedgerBytes,
		RequiredHealthFreshBy:        now.Add(-p.HealthFreshness),
	}
}

// AdminPolicyHashes returns stable hashes for the complete policy semantics
// and for the subset whose alteration invalidates an already-frozen target
// plan. Kubernetes metadata, including resourceVersion, is deliberately not
// included. Collections whose ordering has no policy meaning are canonicalized
// before hashing so a formatting-only policy rewrite does not fence a rollout.
func AdminPolicyHashes(cfg AdminPolicyConfig) (semantic, structural string, err error) {
	if err := validateAdminPolicyConfig(&cfg); err != nil {
		return "", "", err
	}
	canonicalizePolicyCollections(&cfg)
	semantic, err = hashCanonicalJSON(cfg)
	if err != nil {
		return "", "", err
	}
	structure := struct {
		Version               string               `json:"version"`
		FleetSelector         metav1.LabelSelector `json:"fleetSelector"`
		RequiredTopologyKeys  []string             `json:"requiredTopologyKeys"`
		ProjectedTopologyKeys []string             `json:"projectedTopologyKeys"`
		TransferDomainKeys    []string             `json:"transferDomainKeys"`
		UnavailableDomainKeys []string             `json:"unavailableDomainKeys"`
		LedgerName            string               `json:"ledgerName"`
	}{
		Version: cfg.Version, FleetSelector: cfg.FleetSelector,
		RequiredTopologyKeys: cfg.RequiredTopologyKeys, ProjectedTopologyKeys: cfg.ProjectedTopologyKeys,
		TransferDomainKeys:    sortedIntMapKeys(cfg.DomainMaxConcurrentTransfers),
		UnavailableDomainKeys: sortedIntMapKeys(cfg.DomainMaxUnavailable), LedgerName: cfg.LedgerName,
	}
	structural, err = hashCanonicalJSON(structure)
	return semantic, structural, err
}

func canonicalizePolicyCollections(cfg *AdminPolicyConfig) {
	sort.Strings(cfg.RequiredTopologyKeys)
	sort.Strings(cfg.ProjectedTopologyKeys)
	for i := range cfg.FleetSelector.MatchExpressions {
		sort.Strings(cfg.FleetSelector.MatchExpressions[i].Values)
	}
	sort.Slice(cfg.FleetSelector.MatchExpressions, func(i, j int) bool {
		left, _ := json.Marshal(cfg.FleetSelector.MatchExpressions[i])
		right, _ := json.Marshal(cfg.FleetSelector.MatchExpressions[j])
		return string(left) < string(right)
	})
}

func sortedIntMapKeys(values map[string]int) []string {
	keys := make([]string, 0, len(values))
	for key := range values {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	return keys
}

func hashCanonicalJSON(value any) (string, error) {
	encoded, err := json.Marshal(value)
	if err != nil {
		return "", fmt.Errorf("encode administrator policy hash input: %w", err)
	}
	digest := sha256.Sum256(encoded)
	return "sha256:" + hex.EncodeToString(digest[:]), nil
}

func validateAdminPolicyConfig(cfg *AdminPolicyConfig) error {
	if cfg.Version != PolicyVersion {
		return fmt.Errorf("unsupported administrator topology policy version %q", cfg.Version)
	}
	if len(cfg.FleetSelector.MatchLabels) == 0 && len(cfg.FleetSelector.MatchExpressions) == 0 {
		return fmt.Errorf("fleetSelector must be non-empty")
	}
	if len(cfg.FleetSelector.MatchLabels) > 32 || len(cfg.FleetSelector.MatchExpressions) > 32 {
		return fmt.Errorf("fleetSelector supports at most 32 matchLabels and 32 matchExpressions")
	}
	for key, value := range cfg.FleetSelector.MatchLabels {
		if len(key) > 96 || len(value) > 63 {
			return fmt.Errorf("fleetSelector matchLabels keys and values must be at most 96 and 63 characters")
		}
		if !strings.HasPrefix(key, "topology.cisco.vk/") {
			return fmt.Errorf("fleetSelector key %q must use the admission-protected topology.cisco.vk/ prefix", key)
		}
	}
	for _, requirement := range cfg.FleetSelector.MatchExpressions {
		if len(requirement.Key) > 96 || len(requirement.Values) > 32 {
			return fmt.Errorf("fleetSelector expressions support 96-character keys and at most 32 values")
		}
		if !strings.HasPrefix(requirement.Key, "topology.cisco.vk/") {
			return fmt.Errorf("fleetSelector key %q must use the admission-protected topology.cisco.vk/ prefix", requirement.Key)
		}
		for _, value := range requirement.Values {
			if len(value) > 63 {
				return fmt.Errorf("fleetSelector expression values must be at most 63 characters")
			}
		}
	}
	if _, err := metav1.LabelSelectorAsSelector(&cfg.FleetSelector); err != nil {
		return fmt.Errorf("fleetSelector is invalid: %w", err)
	}
	if cfg.GlobalMaxUnavailable < 1 || cfg.GlobalMaxUnavailable > DefaultMaxCampaignTargets {
		return fmt.Errorf("globalMaxUnavailable must be between 1 and %d", DefaultMaxCampaignTargets)
	}
	if cfg.GlobalMaxConcurrentTransfers < 1 || cfg.GlobalMaxConcurrentTransfers > DefaultMaxCampaignTargets {
		return fmt.Errorf("globalMaxConcurrentTransfers must be between 1 and %d", DefaultMaxCampaignTargets)
	}
	if cfg.MaxCampaignTargets == 0 {
		cfg.MaxCampaignTargets = DefaultMaxCampaignTargets
	}
	if cfg.MaxCampaignTargets < 1 || cfg.MaxCampaignTargets > DefaultMaxCampaignTargets {
		return fmt.Errorf("maxCampaignTargets must be between 1 and %d", DefaultMaxCampaignTargets)
	}
	if cfg.MaxActiveReservations == 0 {
		cfg.MaxActiveReservations = DefaultMaxActiveRecords
	}
	if cfg.MaxActiveReservations < 1 || cfg.MaxActiveReservations > DefaultMaxActiveRecords {
		return fmt.Errorf("maxActiveReservations must be between 1 and %d", DefaultMaxActiveRecords)
	}
	if cfg.MaxLedgerBytes == 0 {
		cfg.MaxLedgerBytes = DefaultMaxSerializedBytes
	}
	if cfg.MaxLedgerBytes < 4096 || cfg.MaxLedgerBytes > DefaultMaxSerializedBytes {
		return fmt.Errorf("maxLedgerBytes must be between 4096 and %d", DefaultMaxSerializedBytes)
	}
	if cfg.HealthFreshnessSeconds < 30 || cfg.HealthFreshnessSeconds > 3600 {
		return fmt.Errorf("healthFreshnessSeconds must be between 30 and 3600")
	}
	if problems := validation.IsDNS1123Subdomain(cfg.LedgerName); len(problems) > 0 {
		return fmt.Errorf("ledgerName is invalid: %s", strings.Join(problems, "; "))
	}
	if len(cfg.RequiredTopologyKeys) == 0 || len(cfg.RequiredTopologyKeys) > MaxProjectedTopologyKeys {
		return fmt.Errorf("requiredTopologyKeys must contain between 1 and %d entries", MaxProjectedTopologyKeys)
	}
	if len(cfg.ProjectedTopologyKeys) > MaxProjectedTopologyKeys {
		return fmt.Errorf("projectedTopologyKeys exceeds %d entries", MaxProjectedTopologyKeys)
	}
	required, err := validateTopologyKeys("requiredTopologyKeys", cfg.RequiredTopologyKeys)
	if err != nil {
		return err
	}
	projected, err := validateTopologyKeys("projectedTopologyKeys", cfg.ProjectedTopologyKeys)
	if err != nil {
		return err
	}
	for key := range projected {
		if strings.HasPrefix(key, "operations.cisco.vk/") {
			return fmt.Errorf("operational key %q cannot be projected to scheduler-visible Node labels", key)
		}
		if _, ok := required[key]; !ok {
			return fmt.Errorf("projected topology key %q must also be required", key)
		}
	}
	if len(cfg.DomainMaxUnavailable) == 0 || len(cfg.DomainMaxConcurrentTransfers) == 0 {
		return fmt.Errorf("domainMaxUnavailable and domainMaxConcurrentTransfers must be non-empty")
	}
	if len(cfg.DomainMaxUnavailable) > MaxProjectedTopologyKeys || len(cfg.DomainMaxConcurrentTransfers) > MaxProjectedTopologyKeys {
		return fmt.Errorf("domain budget maps may each contain at most %d entries", MaxProjectedTopologyKeys)
	}
	domainKeys := make(map[string]struct{}, len(cfg.DomainMaxUnavailable)+len(cfg.DomainMaxConcurrentTransfers))
	for key, value := range cfg.DomainMaxUnavailable {
		domainKeys[key] = struct{}{}
		if _, ok := required[key]; !ok {
			return fmt.Errorf("budget key %q must be present in requiredTopologyKeys", key)
		}
		if value < 1 || value > DefaultMaxCampaignTargets {
			return fmt.Errorf("domainMaxUnavailable[%q] must be between 1 and %d", key, DefaultMaxCampaignTargets)
		}
	}
	for key, value := range cfg.DomainMaxConcurrentTransfers {
		domainKeys[key] = struct{}{}
		if _, ok := required[key]; !ok {
			return fmt.Errorf("transfer budget key %q must be present in requiredTopologyKeys", key)
		}
		if value < 1 || value > DefaultMaxCampaignTargets {
			return fmt.Errorf("domainMaxConcurrentTransfers[%q] must be between 1 and %d", key, DefaultMaxCampaignTargets)
		}
	}
	if len(domainKeys) > MaxProjectedTopologyKeys {
		return fmt.Errorf("combined domain budgets may contain at most %d topology keys", MaxProjectedTopologyKeys)
	}
	return nil
}

func validateTopologyKeys(field string, keys []string) (map[string]struct{}, error) {
	seen := make(map[string]struct{}, len(keys))
	for _, key := range keys {
		if len(key) > 96 {
			return nil, fmt.Errorf("%s contains key %q longer than 96 characters", field, key)
		}
		if _, ok := seen[key]; ok {
			return nil, fmt.Errorf("%s contains duplicate %q", field, key)
		}
		if key != corev1.LabelTopologyRegion && key != corev1.LabelTopologyZone &&
			!strings.HasPrefix(key, "topology.cisco.vk/") && !strings.HasPrefix(key, "operations.cisco.vk/") {
			return nil, fmt.Errorf("%s contains unsupported key %q", field, key)
		}
		if problems := validation.IsQualifiedName(key); len(problems) > 0 {
			return nil, fmt.Errorf("%s contains invalid key %q: %s", field, key, strings.Join(problems, "; "))
		}
		seen[key] = struct{}{}
	}
	return seen, nil
}

// CanonicalPolicyJSON is used by Helm/rendering tests and bootstrap tooling.
// It never includes the ledger UID, which is bound as a protected annotation
// after both ConfigMaps have Kubernetes UIDs.
func CanonicalPolicyJSON(cfg AdminPolicyConfig) (string, error) {
	if err := validateAdminPolicyConfig(&cfg); err != nil {
		return "", err
	}
	data, err := json.Marshal(cfg)
	if err != nil {
		return "", err
	}
	return string(data), nil
}

func cloneIntMap(in map[string]int) map[string]int {
	out := make(map[string]int, len(in))
	for key, value := range in {
		out[key] = value
	}
	return out
}
