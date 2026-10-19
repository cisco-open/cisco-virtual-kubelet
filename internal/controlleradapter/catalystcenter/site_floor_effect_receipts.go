// Copyright 2026 Cisco Systems Inc.
// SPDX-License-Identifier: Apache-2.0
package catalystcenter

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"sort"
	"strings"

	cvk "github.com/cisco/virtual-kubelet-cisco/api/v1alpha1"
)

const sitePrivateFloorUpdatePrefix = "site-private-floor-update:v1:"
const plannedAPFloorEffectPrefix = "planned-ap-floor-effect:v1:"

// The floor operation binds the complete related-state set before dispatch.
// Its first five fields retain the ordinary private Update's original creator,
// source/key scope and exact prior floor receipt. The sixth is the effect root.
func sitePrivateFloorUpdateParts(resource string) ([]string, bool) {
	if !strings.HasPrefix(resource, sitePrivateFloorUpdatePrefix) {
		return nil, false
	}
	parts := strings.Split(strings.TrimPrefix(resource, sitePrivateFloorUpdatePrefix), ":")
	if len(parts) != 6 || parts[0] != "floor" || !validTemplateComparisonDigest(parts[4]) || !validTemplateComparisonDigest(parts[5]) {
		return nil, false
	}
	_, ok := sitePrivateCreateParts(sitePrivateCreatePrefix + strings.Join(parts[:4], ":"))
	return parts, ok
}

type plannedAPFloorEffectReceipt struct {
	resource, id, keyID, mac, prior string
}

func decodePlannedAPFloorEffect(value string) (plannedAPFloorEffectReceipt, bool) {
	if !strings.HasPrefix(value, plannedAPFloorEffectPrefix) {
		return plannedAPFloorEffectReceipt{}, false
	}
	parts := strings.Split(strings.TrimPrefix(value, plannedAPFloorEffectPrefix), ":")
	if len(parts) != 5 || !validTemplateComparisonDigest(parts[0]) || !canonicalTemplateUUID(parts[1]) || !validTemplateComparisonDigest(parts[2]) || !validTemplateComparisonDigest(parts[3]) || !validTemplateComparisonDigest(parts[4]) {
		return plannedAPFloorEffectReceipt{}, false
	}
	return plannedAPFloorEffectReceipt{resource: "planned-ap:" + parts[0], id: parts[1], keyID: parts[2], mac: parts[3], prior: parts[4]}, true
}

func plannedAPFloorEffectRoot(effects []string) (string, error) {
	if len(effects) == 0 || len(effects) > 256 {
		return "", errConfigurationOwned
	}
	ordered := append([]string(nil), effects...)
	sort.Strings(ordered)
	keys, ids := map[string]bool{}, map[string]bool{}
	for _, effect := range ordered {
		r, ok := decodePlannedAPFloorEffect(effect)
		if !ok || len(effect) > 512 || keys[r.resource] || ids[r.id] {
			return "", errConfigurationOwned
		}
		keys[r.resource], ids[r.id] = true, true
	}
	// Only public identifiers and keyed fingerprints enter this root. Sorting
	// respects the Kubernetes set-list contract without depending on API order.
	raw, err := json.Marshal(ordered)
	if err != nil {
		return "", errConfigurationOwned
	}
	sum := sha256.Sum256(append([]byte("cvk-planned-ap-floor-effects-v1\x00"), raw...))
	return hex.EncodeToString(sum[:]), nil
}

// Receipt decoding is not native verification or checkpoint consumption. The
// future publisher must verify the prebound complete effects after the original
// task; ordinary site Updates must never carry or adopt such effects.
func validateSiteFloorEffects(state *cvk.NetworkControllerConfigurationStatus, row cvk.NetworkControllerConfigurationActionApplication) error {
	op := &row.Operation
	parts, selected := sitePrivateFloorUpdateParts(op.Resource)
	if !selected {
		if strings.HasPrefix(op.Resource, sitePrivateFloorUpdatePrefix) || len(row.EffectReceipts) != 0 {
			return errConfigurationOwned
		}
		return nil
	}
	root, err := plannedAPFloorEffectRoot(row.EffectReceipts)
	if err != nil || root != parts[5] {
		return errConfigurationOwned
	}
	anchors, err := plannedAPIdentityRecords(state)
	if err != nil {
		return err
	}
	for _, encoded := range row.EffectReceipts {
		effect, _ := decodePlannedAPFloorEffect(encoded)
		anchor, ok := anchors[effect.resource]
		base, private := plannedAPPrivateParts(anchor.receipt.Operation.Resource)
		if !ok || !private || len(base) != 3 || anchor.id != effect.id || effect.id == op.TargetID || effect.id == op.ParentID || anchor.keyID != effect.keyID || effect.keyID != parts[3] || base[1] != parts[2] || anchor.receipt.Operation.ParentID != op.TargetID || anchor.receipt.Operation.ConfigUID != op.ConfigUID || anchor.receipt.Operation.ConfigName != op.ConfigName || anchor.receipt.Operation.ConfigGeneration > op.ConfigGeneration || anchor.receipt.Operation.ControllerGeneration > op.ControllerGeneration || anchor.receipt.VerifiedAt == nil || op.SubmittedAt == nil || op.SubmittedAt.Before(anchor.receipt.VerifiedAt) {
			return errConfigurationOwned
		}
	}
	return nil
}
