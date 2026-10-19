// Copyright © 2026 Cisco Systems Inc.
// SPDX-License-Identifier: Apache-2.0
package catalystcenter

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"sort"
	"strings"

	cvk "github.com/cisco/virtual-kubelet-cisco/api/v1alpha1"
)

// Matches the pinned provider 0.6.1 create shard size, not an asserted API limit.
const anycastBulkRequestLimit = 20
const anycastBulkChunkPrefix = "anycast-gateway-bulk:v2:"

func isAnycastBulkResource(resource string) bool {
	return strings.HasPrefix(resource, "anycast-gateway-bulk:")
}

func canonicalAnycastMembers(desired []fabricAnycastIntent) []fabricAnycastIntent {
	wants := append([]fabricAnycastIntent(nil), desired...)
	sort.Slice(wants, func(i, j int) bool {
		if wants[i].path != wants[j].path {
			return wants[i].path < wants[j].path
		}
		return wants[i].pool < wants[j].pool
	})
	return wants
}

func anycastCohortDigest(desired []fabricAnycastIntent) (string, error) {
	type declaration struct {
		Path, Parent, Pool, Network string
		Secondary                   bool
		Payload                     map[string]any
	}
	rows := []declaration{}
	for _, want := range canonicalAnycastMembers(desired) {
		rows = append(rows, declaration{want.path, want.parent, want.pool, want.network, want.needsSecondaryPools, want.payload})
	}
	raw, err := json.Marshal(rows)
	if err != nil {
		return "", errInvalidResponse
	}
	return fmt.Sprintf("%x", sha256.Sum256(raw)), nil
}

func anycastChunkResource(start, total int, cohortDigest, legacy string) (string, error) {
	parts := strings.Split(strings.TrimPrefix(legacy, anycastBulkPrefix), ":")
	if !strings.HasPrefix(legacy, anycastBulkPrefix) || len(parts) != 3 {
		return "", errConfigurationTransition
	}
	return fmt.Sprintf("%s%d:%d:%s:%s", anycastBulkChunkPrefix, start, total, strings.Join(parts, ":"), cohortDigest), nil
}

// Decode the original source offset/count, never a slice of remaining missing
// members. Legacy acknowledged v1 batches retain their original whole cohort.
func (c *client) verifyAnycastGatewayChunk(ctx context.Context, desired []fabricAnycastIntent, op *cvk.NetworkControllerConfigurationOperation) (bool, error) {
	if op == nil {
		return false, errConfigurationTransition
	}
	receipt, err := decodeAnycastBulkReceipt(op.Resource)
	if err != nil || receipt.version != 2 || receipt.total != len(desired) {
		return false, errConfigurationTransition
	}
	start, count := receipt.start, receipt.count

	digest, err := anycastCohortDigest(desired)
	if err != nil {
		return false, err
	}
	if digest != receipt.cohort {
		return false, errConfigurationStale
	}
	wants := canonicalAnycastMembers(desired)
	legacy := op.DeepCopy()
	legacy.Resource = receipt.legacy()
	return c.verifyAnycastGatewayBulk(ctx, wants[start:start+count], legacy)
}
