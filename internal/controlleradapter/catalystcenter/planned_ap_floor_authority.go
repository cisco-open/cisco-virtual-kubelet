// Copyright 2026 Cisco Systems Inc.
// SPDX-License-Identifier: Apache-2.0
package catalystcenter

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"strings"

	cvk "github.com/cisco/virtual-kubelet-cisco/api/v1alpha1"
)

const plannedAPFloorUpdatePrefix = "planned-ap-floor-update:v1:"

type plannedAPFloorAuthority struct {
	effect plannedAPFloorEffectReceipt
	floor  cvk.NetworkControllerConfigurationActionApplication
	digest string
}

func plannedAPFloorAuthorities(state *cvk.NetworkControllerConfigurationStatus) (map[string]plannedAPFloorAuthority, error) {
	rows, err := siteAppliedRecords(state)
	if err != nil {
		return nil, err
	}
	out := map[string]plannedAPFloorAuthority{}
	for _, row := range rows {
		if _, ok := sitePrivateFloorUpdateParts(row.Operation.Resource); !ok {
			continue
		}
		for _, encoded := range row.EffectReceipts {
			effect, ok := decodePlannedAPFloorEffect(encoded)
			if !ok {
				return nil, errConfigurationOwned
			}
			if _, duplicate := out[effect.resource]; duplicate {
				return nil, errConfigurationOwned
			}
			raw, _ := json.Marshal([]string{siteReceiptDigest(row), encoded})
			sum := sha256.Sum256(append([]byte("cvk-planned-ap-floor-authority-v1\x00"), raw...))
			out[effect.resource] = plannedAPFloorAuthority{effect: effect, floor: *row.DeepCopy(), digest: hex.EncodeToString(sum[:])}
		}
	}
	return out, nil
}

// Select authority by an explicit floor operation identity, never by timestamp
// ordering. A later floor checkpoint supersedes the previous AP latest row;
// corrections within that epoch advance only their own AP latest receipt.
func plannedAPCurrentAuthority(key string, anchor plannedAPIdentityRecord, applied map[string]cvk.NetworkControllerConfigurationActionApplication, floors map[string]plannedAPFloorAuthority) (mac, prior, epoch string, err error) {
	row, hasApplied := applied[key]
	floor, hasFloor := floors[key]
	if hasFloor {
		if floor.effect.id != anchor.id || floor.effect.keyID != anchor.keyID || floor.floor.Operation.ParentID == anchor.id || floor.floor.Operation.TargetID != anchor.receipt.Operation.ParentID {
			return "", "", "", errConfigurationOwned
		}
		epoch = floor.floor.Operation.ID
		if !configurationOperationID.MatchString(epoch) || !validTemplateComparisonDigest(floor.digest) {
			return "", "", "", errConfigurationOwned
		}
		if parts, ok := plannedAPPrivateParts(row.Operation.Resource); !hasApplied || !ok || len(parts) != 6 || parts[5] != epoch {
			return floor.effect.mac, floor.digest, epoch, nil
		}
	} else if hasApplied && strings.HasPrefix(row.Operation.Resource, plannedAPFloorUpdatePrefix) {
		// Do not downgrade a floor-bound correction when its checkpoint is lost.
		return "", "", "", errConfigurationOwned
	}
	if hasApplied {
		parts := strings.Split(strings.TrimPrefix(row.Resource, plannedAPAppliedPrefix), ":")
		if len(parts) != 6 || !validTemplateComparisonDigest(parts[4]) {
			return "", "", "", errConfigurationOwned
		}
		return parts[4], siteReceiptDigest(row), epoch, nil
	}
	return anchor.mac, siteReceiptDigest(anchor.receipt), epoch, nil
}
