// Copyright 2026 Cisco Systems Inc.
// SPDX-License-Identifier: Apache-2.0
package catalystcenter

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"reflect"
	"slices"
	"strings"
	"sync/atomic"
	"time"

	configapi "github.com/cisco/virtual-kubelet-cisco/api/config/v1alpha1"
	ciskov1 "github.com/cisco/virtual-kubelet-cisco/api/v1alpha1"
)

const aaaResetInherit = "inherit"
const aaaResetUnset = "unset"

var errAAAUnsetUnavailable = errors.New("AAA empty-object unset is not native-qualified; ownership must be retained")
var errAAAInheritanceUnavailable = errors.New("AAA inheritance requires configured parent settings")

// These are explicit native operations, not interpretations of omitted NaC
// fields. Public compilation remains separate until cleanup recovery and
// atomic ownership finalization are connected to the existing journal.
type aaaResetIntent struct{ site, network, client string }

func (want aaaResetIntent) payload() (map[string]any, error) {
	if !validSitePath(want.site) {
		return nil, errConfigurationTransition
	}
	result := map[string]any{}
	for group, mode := range map[string]string{"aaaNetwork": want.network, "aaaClient": want.client} {
		switch mode {
		case aaaResetInherit:
			result[group] = nil
		case aaaResetUnset:
			result[group] = map[string]any{}
		default:
			return nil, errConfigurationTransition
		}
	}
	return result, nil
}

func validateAAAResetOwnership(config *configapi.NetworkControllerConfig, want aaaResetIntent, receipt ciskov1.NetworkControllerConfigurationCredentialApplication) error {
	if config == nil || config.Spec.Mode != configapi.NetworkControllerApplyModeApply || config.Spec.PrunePolicy != configapi.NetworkControllerRetentionPolicyDelete ||
		config.UID == "" || config.Generation < 1 || len(config.Spec.ManagedSections) != 1 || config.Spec.ManagedSections[0] != "network_settings" ||
		receipt.Section != "network_settings" || receipt.Resource != networkSettingIdentity(want.site, "aaa") || receipt.ConfigUID != string(config.UID) || !validAPIID(receipt.TargetID) ||
		!configurationOperationID.MatchString(receipt.OperationID) || len(receipt.Versions) != 2 {
		return errConfigurationOwned
	}
	if _, err := configurationCredentialRotation(receipt.Versions, receipt.Versions, aaaCredentialKind); err != nil {
		return err
	}
	for _, v := range receipt.Versions {
		if v.Key != "" {
			return errConfigurationOwned
		}
	}
	_, err := want.payload()
	if err == nil && (want.network == aaaResetUnset || want.client == aaaResetUnset) {
		return errAAAUnsetUnavailable
	}
	return err
}

func aaaResetResource(want aaaResetIntent, receipt ciskov1.NetworkControllerConfigurationCredentialApplication) string {
	versions := append([]ciskov1.NetworkControllerConfigurationCredentialVersion(nil), receipt.Versions...)
	slices.SortFunc(versions, func(a, b ciskov1.NetworkControllerConfigurationCredentialVersion) int {
		return strings.Compare(a.Path, b.Path)
	})
	raw, _ := json.Marshal(struct {
		Site, Network, Client, Owner, Target, Operation string
		Versions                                        []ciskov1.NetworkControllerConfigurationCredentialVersion
	}{want.site, want.network, want.client, receipt.ConfigUID, receipt.TargetID, receipt.OperationID, versions})
	return fmt.Sprintf("aaa-owned-reset:%x", sha256.Sum256(raw))
}

type aaaResetObservation struct {
	ancestors              []aaaResetAncestor
	id, path, parent, kind string
	groups                 map[string]map[string]string
}

type aaaResetAncestor struct {
	id, path, parent string
	groups           map[string]map[string]string
}

func (c *client) observeAAAReset(ctx context.Context, want aaaResetIntent, target string) (aaaResetObservation, error) {
	sites, err := c.ListSites(ctx)
	if err != nil {
		return aaaResetObservation{}, err
	}
	byID := map[string]Site{}
	for _, site := range sites {
		byID[site.ID] = site
	}
	for _, site := range sites {
		if site.NameHierarchy != want.site {
			continue
		}
		if site.ID != target {
			return aaaResetObservation{}, errConfigurationStale
		}
		got, err := c.readNetworkSetting(ctx, site.ID, "aaa")
		if err != nil {
			return aaaResetObservation{}, err
		}
		result := aaaResetObservation{id: site.ID, path: site.NameHierarchy, parent: site.ParentID, kind: site.Type, groups: got.aaa}
		unresolved := map[string]bool{"aaaNetwork": want.network == aaaResetInherit, "aaaClient": want.client == aaaResetInherit}
		parent := site
		for depth := 0; unresolved["aaaNetwork"] || unresolved["aaaClient"]; depth++ {
			if depth >= len(sites) || parent.Type == "global" {
				return aaaResetObservation{}, errAAAInheritanceUnavailable
			}
			next, exists := byID[parent.ParentID]
			if !exists || next.NameHierarchy != parentSitePath(parent.NameHierarchy) {
				return aaaResetObservation{}, errConfigurationStale
			}
			inherited, err := c.readNetworkSetting(ctx, next.ID, "aaa")
			if err != nil {
				return aaaResetObservation{}, err
			}
			result.ancestors = append(result.ancestors, aaaResetAncestor{next.ID, next.NameHierarchy, next.ParentID, inherited.aaa})
			for group, pending := range unresolved {
				if !pending {
					continue
				}
				fields, exists := inherited.aaa[group]
				if !exists {
					return aaaResetObservation{}, errInvalidResponse
				}
				if fields == nil {
					continue
				}
				if len(fields) == 0 {
					return aaaResetObservation{}, errAAAInheritanceUnavailable
				}
				unresolved[group] = false
			}
			parent = next
		}
		return result, nil
	}
	return aaaResetObservation{}, errConfigurationStale
}

func aaaResetMatches(want aaaResetIntent, got aaaResetObservation) bool {
	for group, mode := range map[string]string{"aaaNetwork": want.network, "aaaClient": want.client} {
		if mode != aaaResetInherit && mode != aaaResetUnset {
			return false
		}
		value, present := got.groups[group]
		if !present || mode == aaaResetInherit && value != nil || mode == aaaResetUnset && (value == nil || len(value) != 0) {
			return false
		}
	}
	return true
}

// Receipt evidence is necessary but is not dispatch authority. The caller must
// guard current source, owner, receipt and any required physical admission.
// No credential bytes, fabricated versions or registration writes are used.
func (c *client) planOwnedAAAReset(ctx context.Context, config *configapi.NetworkControllerConfig, want aaaResetIntent, receipt ciskov1.NetworkControllerConfigurationCredentialApplication, guard func(context.Context) error) (*configurationWrite, error) {
	if err := validateAAAResetOwnership(config, want, receipt); err != nil {
		return nil, err
	}
	if guard == nil {
		return nil, errConfigurationTransition
	}
	config = config.DeepCopy()
	receipt.Versions = append([]ciskov1.NetworkControllerConfigurationCredentialVersion(nil), receipt.Versions...)
	if err := guard(ctx); err != nil {
		return nil, err
	}
	observed, err := c.observeAAAReset(ctx, want, receipt.TargetID)
	if err != nil {
		return nil, err
	}
	if err := guard(ctx); err != nil {
		return nil, err
	}
	if aaaResetMatches(want, observed) {
		return nil, nil
	}
	payload, _ := want.payload()
	body, err := json.Marshal(payload)
	if err != nil {
		return nil, err
	}
	var used atomic.Bool
	return &configurationWrite{resource: aaaResetResource(want, receipt), action: "Update", identity: configurationResourceIdentity{target: receipt.TargetID}, submit: func(ctx context.Context) (Task, error) {
		if !used.CompareAndSwap(false, true) {
			return Task{}, errConfigurationTransition
		}
		if err := validateAAAResetOwnership(config, want, receipt); err != nil {
			return Task{}, err
		}
		if err := guard(ctx); err != nil {
			return Task{}, err
		}
		fresh, err := c.observeAAAReset(ctx, want, receipt.TargetID)
		if err != nil {
			return Task{}, err
		}
		if !reflect.DeepEqual(fresh, observed) {
			return Task{}, errConfigurationStale
		}
		if err := guard(ctx); err != nil {
			return Task{}, err
		}
		raw, err := c.mutate(ctx, http.MethodPut, sitesPath+"/"+receipt.TargetID+"/aaaSettings", body)
		if err != nil {
			return Task{}, err
		}
		var ack struct {
			Response Task `json:"response"`
		}
		if json.Unmarshal(raw, &ack) != nil || !validAPIID(ack.Response.ID) {
			return Task{}, errInvalidResponse
		}
		return Task{ID: ack.Response.ID}, nil
	}}, nil
}

// Reconstruct observation from the retained receipt and acknowledged operation;
// a successful task alone never proves either reset representation.
func (c *client) verifyOwnedAAAReset(ctx context.Context, config *configapi.NetworkControllerConfig, want aaaResetIntent, receipt ciskov1.NetworkControllerConfigurationCredentialApplication, op *ciskov1.NetworkControllerConfigurationOperation, guard func(context.Context) error, now time.Time) (bool, error) {
	if err := validateAAAResetOwnership(config, want, receipt); err != nil {
		return false, err
	}
	if guard == nil || op == nil || (op.Phase != "Accepted" && op.Phase != "Verifying") || op.ConfigName != config.Name || op.ConfigGeneration != config.Generation || op.Section != "network_settings" || op.ConfigUID != string(config.UID) || op.Resource != aaaResetResource(want, receipt) ||
		op.TargetID != receipt.TargetID || op.Action != "Update" || op.ParentID != "" || op.TargetSerial != "" || op.DependencyVersions != nil || op.PhysicalGroup != nil ||
		len(op.CredentialVersions) != 0 || op.SubmittedAt == nil || !validAPIID(op.TaskID) {
		return false, errConfigurationTransition
	}
	if err := guard(ctx); err != nil {
		return false, err
	}
	completed, err := c.configurationTaskTreeCompletion(ctx, op.TaskID, op.SubmittedAt.Time, now)
	if err != nil || completed.IsZero() || completed.After(now) {
		return false, err
	}
	first, err := c.observeAAAReset(ctx, want, receipt.TargetID)
	if err != nil {
		return false, err
	}
	if err := guard(ctx); err != nil {
		return false, err
	}
	second, err := c.observeAAAReset(ctx, want, receipt.TargetID)
	if err != nil {
		return false, err
	}
	if !reflect.DeepEqual(first, second) {
		return false, errConfigurationStale
	}
	if err := guard(ctx); err != nil {
		return false, err
	}
	final, err := c.configurationTaskTreeCompletion(ctx, op.TaskID, op.SubmittedAt.Time, now)
	if err != nil {
		return false, err
	}
	if !final.Equal(completed) {
		return false, errConfigurationStale
	}
	return aaaResetMatches(want, second), nil
}
