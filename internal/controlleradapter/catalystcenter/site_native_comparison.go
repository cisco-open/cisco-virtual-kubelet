// Copyright 2026 Cisco Systems Inc.
// SPDX-License-Identifier: Apache-2.0
package catalystcenter

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"net/url"
	"reflect"
	"strings"

	configapi "github.com/cisco/virtual-kubelet-cisco/api/config/v1alpha1"
	cvk "github.com/cisco/virtual-kubelet-cisco/api/v1alpha1"
)

const siteComparisonScope = "/sites"
const sitePrivateCreatePrefix = "site-private-create:v1:"
const sitePrivateCreatedPrefix = "site-created:v2:"

func siteComparisonConfig(config *configapi.NetworkControllerConfig) bool {
	return config != nil && len(config.Spec.ManagedSections) == 1 && config.Spec.ManagedSections[0] == "sites" && len(config.Spec.SecretRefs) == 1 && config.Spec.SecretRefs[0].Use == "PrivateComparisonKey" && config.Spec.SecretRefs[0].Section == "sites" && config.Spec.SecretRefs[0].Path == siteComparisonScope
}

func sitePathDigest(path string) string {
	sum := sha256.Sum256([]byte(path))
	return hex.EncodeToString(sum[:])
}

func sitePrivateCreateResource(site desiredSite, config *configapi.NetworkControllerConfig, comparison *templateInputComparison) string {
	mode := "i"
	if config.Spec.Source.ConfigMapRef != nil {
		mode = "c"
	}
	return sitePrivateCreatePrefix + site.kind + ":" + sitePathDigest(site.path) + ":" + mode + ":" + comparison.keyID
}

func sitePrivateCreateParts(resource string) ([]string, bool) {
	if !strings.HasPrefix(resource, sitePrivateCreatePrefix) {
		return nil, false
	}
	parts := strings.Split(strings.TrimPrefix(resource, sitePrivateCreatePrefix), ":")
	return parts, len(parts) == 4 && (parts[0] == "area" || parts[0] == "building" || parts[0] == "floor") && validTemplateComparisonDigest(parts[1]) && (parts[2] == "i" || parts[2] == "c") && validTemplateComparisonDigest(parts[3])
}

func siteDesiredOperation(desired []desiredSite, resource string) (*desiredSite, bool) {
	parts, private := sitePrivateOperationParts(resource)
	for index := range desired {
		want := &desired[index]
		if resource == want.path || private && parts[0] == want.kind && parts[1] == sitePathDigest(want.path) {
			return want, true
		}
	}
	return nil, false
}

// Canonical JSON keeps unknown fields and exact numbers. The full native values
// stay in memory; only a domain-separated keyed MAC may enter a receipt.
type siteNativeObject map[string]any

func (s *siteNativeObject) UnmarshalJSON(raw []byte) error {
	value, err := decodeTemplateNativeJSON(raw)
	if err != nil {
		return err
	}
	body, ok := value.(map[string]any)
	if !ok || body == nil {
		return errInvalidResponse
	}
	*s = body
	return nil
}

func (c *client) siteNativeObjects(ctx context.Context, actual []Site) (map[string]siteNativeObject, error) {
	rows, err := listInventoryQueryWithGet(ctx, func(ctx context.Context, path string, query url.Values) ([]byte, error) {
		return c.get(ctx, path, query, 200)
	}, sitesPath, url.Values{}, func(row siteNativeObject) string { id, _ := row["id"].(string); return id })
	if err != nil {
		return nil, err
	}
	if len(rows) != len(actual) {
		return nil, errConfigurationStale
	}
	byID := map[string]siteNativeObject{}
	for _, row := range rows {
		id, _ := row["id"].(string)
		byID[id] = row
	}
	byPath := map[string]siteNativeObject{}
	for _, site := range actual {
		row, exists := byID[site.ID]
		if !exists {
			return nil, errConfigurationStale
		}
		raw, err := json.Marshal(row)
		if err != nil {
			return nil, errInvalidResponse
		}
		var typed Site
		if json.Unmarshal(raw, &typed) != nil || !reflect.DeepEqual(typed, site) {
			return nil, errConfigurationStale
		}
		byPath[site.NameHierarchy] = row
	}
	return byPath, nil
}

func (c *client) siteNativeFootprint(ctx context.Context, actual []Site, path string, fullAncestors bool) ([]byte, error) {
	if _, err := siteDispatchFootprint(actual, path); err != nil {
		return nil, err
	}
	byPath, err := c.siteNativeObjects(ctx, actual)
	if err != nil {
		return nil, err
	}
	return siteNativeView(byPath, path, fullAncestors)
}

func siteNativeView(byPath map[string]siteNativeObject, path string, fullAncestors bool) ([]byte, error) {
	if !validSitePath(path) || path == "Global" {
		return nil, errInvalidResponse
	}
	chain := []siteNativeObject{}
	for at := path; at != ""; at = parentSitePath(at) {
		if row, exists := byPath[at]; exists {
			if at != path && !fullAncestors {
				// Historical object state depends on ancestor identity, not on
				// unrelated ancestor settings. Parent updates must not require
				// silently rebasing every child's ownership evidence.
				identity := siteNativeObject{}
				for _, key := range []string{"id", "name", "nameHierarchy", "type", "parentId"} {
					if value, ok := row[key]; ok {
						identity[key] = value
					}
				}
				chain = append(chain, identity)
			} else {
				chain = append(chain, row)
			}
		} else if at != path {
			return nil, errInvalidResponse
		}
		if at == "Global" {
			break
		}
	}
	return json.Marshal(struct {
		Path  string
		Sites []siteNativeObject
	}{path, chain})
}

func siteNativeMAC(comparison *templateInputComparison, resource string, footprint []byte) (string, error) {
	parts, valid := sitePrivateOperationParts(resource)
	if !valid {
		return "", errConfigurationTransition
	}
	if comparison == nil || len(comparison.key) != 32 || parts[3] != comparison.keyID || comparison.check == nil {
		return "", errConfigurationSecret
	}
	mac := hmac.New(sha256.New, comparison.key)
	mac.Write([]byte("cvk-site-native-state-v1\x00" + strings.Join(parts, ":") + "\x00"))
	mac.Write(footprint)
	return hex.EncodeToString(mac.Sum(nil)), nil
}

// Compare the latest applied footprint while preserving historical creator
// evidence. Only an acknowledged verified Update can replace the current row.
func (e *siteConfigurationExecutor) checkPrivateBaselines(ctx context.Context, actual []Site) error {
	if e.comparison == nil {
		for _, record := range e.creators {
			if record.keyID != "" {
				return errConfigurationSecret
			}
		}
		return nil
	}
	if len(e.comparison.key) != 32 || !validTemplateComparisonDigest(e.comparison.keyID) || e.comparison.check == nil {
		return errConfigurationSecret
	}
	var native map[string]siteNativeObject
	for _, desired := range e.desired {
		var record *siteCreatorRecord
		for key, candidate := range e.creators {
			if want, ok := siteDesiredOperation([]desiredSite{desired}, key); ok && want != nil {
				copy := candidate
				record = &copy
				break
			}
		}
		if err := e.comparison.check(ctx); err != nil {
			return err
		}
		present := false
		for _, site := range actual {
			if site.NameHierarchy == desired.path {
				present = true
			}
		}
		if !present && record == nil {
			continue
		}
		if record == nil || record.keyID != e.comparison.keyID {
			return errConfigurationOwned
		}
		if _, err := siteDispatchFootprint(actual, desired.path); err != nil {
			return err
		}
		if native == nil {
			var err error
			native, err = e.client.siteNativeObjects(ctx, actual)
			if err != nil {
				return err
			}
		}
		view, err := siteNativeView(native, desired.path, false)
		if err != nil {
			return err
		}
		mac, err := siteNativeMAC(e.comparison, record.receipt.Operation.Resource, view)
		if err != nil {
			return err
		}
		expected := record.mac
		if latest, ok := e.applied[sitePathDigest(desired.path)]; ok {
			parts := strings.Split(strings.TrimPrefix(latest.Resource, siteAppliedPrefix), ":")
			expected = parts[5]
		}
		if !hmac.Equal([]byte(mac), []byte(expected)) {
			return errConfigurationStale
		}
		if err := e.comparison.check(ctx); err != nil {
			return err
		}
	}
	return nil
}

func sitePrivateRequired(state *cvk.NetworkControllerConfigurationStatus) bool {
	if state == nil {
		return false
	}
	if state.Operation != nil && (strings.HasPrefix(state.Operation.Resource, sitePrivateCreatePrefix) || siteAppliedSelected(state.Operation)) {
		return true
	}
	for _, r := range state.ActionApplications {
		if strings.HasPrefix(r.Resource, sitePrivateCreatedPrefix) || strings.HasPrefix(r.Operation.Resource, sitePrivateCreatePrefix) || strings.HasPrefix(r.Resource, siteAppliedPrefix) || siteAppliedSelected(&r.Operation) {
			return true
		}
	}
	return false
}

func siteResourcePathDigest(resource string) string {
	if validSitePath(resource) {
		return sitePathDigest(resource)
	}
	parts, ok := sitePrivateOperationParts(resource)
	if ok {
		return parts[1]
	}
	return ""
}
