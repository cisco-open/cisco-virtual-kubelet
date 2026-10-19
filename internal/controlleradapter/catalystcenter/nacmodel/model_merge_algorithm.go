// Copyright © 2022 Cisco Systems, Inc. and its affiliates.
// All rights reserved.
//
// Licensed under the Mozilla Public License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//     https://mozilla.org/MPL/2.0/
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.
//
// SPDX-License-Identifier: MPL-2.0

package nacmodel

import (
	"sort"
)

// Adapted from utils v2.0.0, a383caa57652edc7952f3bd596f9a75719480643.
// OrderedMap branches and unused standalone helpers are omitted; the existing
// CVK strict decoder supplies string-keyed maps. No merge semantics are changed.
//
// modelMergeMapGet retrieves a value from a decoded string-keyed map.
func modelMergeMapGet(m any, key string) (any, bool) {
	switch v := m.(type) {
	case map[string]any:
		val, ok := v[key]
		return val, ok
	}
	return nil, false
}

// modelMergeMapSet sets a value in a decoded string-keyed map.
func modelMergeMapSet(m any, key string, value any) {
	switch v := m.(type) {
	case map[string]any:
		v[key] = value
	}
}

// modelMergeMapForEach visits independent map fields; output key order is canonicalized by JSON encoding.
func modelMergeMapForEach(m any, fn func(key string, value any)) {
	switch v := m.(type) {
	case map[string]any:
		for k, val := range v {
			fn(k, val)
		}
	}
}

// modelMergeMapLen returns the number of entries.
func modelMergeMapLen(m any) int {
	switch v := m.(type) {
	case map[string]any:
		return len(v)
	}
	return 0
}

// mergeModelMaps merges source fields into the decoded destination map.
func mergeModelMaps(src, dst any, deduplicate bool) any {
	modelMergeMapForEach(src, func(key string, sValue any) {
		if sValue == nil {
			return
		}
		dValue, exists := modelMergeMapGet(dst, key)
		if !exists || dValue == nil {
			modelMergeMapSet(dst, key, sValue)
		} else {
			srcMap, srcIsMap := modelMergeAsMap(sValue)
			dstMap, dstIsMap := modelMergeAsMap(dValue)
			if srcIsMap && dstIsMap {
				modelMergeMapSet(dst, key, mergeModelMaps(srcMap, dstMap, deduplicate))
				return
			}

			if sv, ok := sValue.([]any); ok {
				if dv, ok := dValue.([]any); ok {
					if deduplicate {
						if len(sv) == 0 || len(dv) == 0 {
							modelMergeMapSet(dst, key, append(dv, sv...))
						} else if modelMergeHasDuplicatesInList(sv) || modelMergeHasDuplicatesInList(dv) {
							modelMergeMapSet(dst, key, append(dv, sv...))
						} else {
							merged := dv
							modelMergeMergeListItemsIndexed(sv, &merged, deduplicate)
							modelMergeMapSet(dst, key, merged)
						}
					} else {
						modelMergeMapSet(dst, key, append(dv, sv...))
					}
					return
				}
			}

			modelMergeMapSet(dst, key, sValue)
		}
	})
	return dst
}

// modelMergeAsMap recognizes a decoded string-keyed map.
func modelMergeAsMap(v any) (any, bool) {
	switch v.(type) {
	case map[string]any:
		return v, true
	}
	return nil, false
}

// modelMergeKvPair represents a key-value pair used as an inverted index key
type modelMergeKvPair struct {
	key   string
	value any
}

// modelMergeIsPrimitive returns true if the value is not a map or slice
func modelMergeIsPrimitive(v any) bool {
	switch v.(type) {
	case map[string]any, []any:
		return false
	default:
		return true
	}
}

// modelMergeExtractPrimitives returns only primitive key-value pairs from a map-like value.
// The result is always map[string]any (used for inverted index lookups only).
func modelMergeExtractPrimitives(m any) map[string]any {
	size := modelMergeMapLen(m)
	result := make(map[string]any, size)
	modelMergeMapForEach(m, func(k string, v any) {
		if modelMergeIsPrimitive(v) {
			result[k] = v
		}
	})
	return result
}

// modelMergeBuildInvertedIndex builds a mapping from (key, value) pairs to item indices
func modelMergeBuildInvertedIndex(primsList []map[string]any) map[modelMergeKvPair][]int {
	index := make(map[modelMergeKvPair][]int)
	for i, prims := range primsList {
		for k, v := range prims {
			pair := modelMergeKvPair{key: k, value: v}
			index[pair] = append(index[pair], i)
		}
	}
	return index
}

// modelMergeHasDuplicatesInList checks if a list contains duplicate dict items using an inverted index
func modelMergeHasDuplicatesInList(items []any) bool {
	// Only check dict items for duplicates, precompute primitives
	var indices []int
	var primsList []map[string]any
	for i, item := range items {
		if _, ok := modelMergeAsMap(item); ok {
			indices = append(indices, i)
			primsList = append(primsList, modelMergeExtractPrimitives(item))
		}
	}

	if len(indices) < 2 {
		return false
	}

	// Build inverted index
	index := modelMergeBuildInvertedIndex(primsList)

	// Check candidate pairs from buckets with 2+ entries
	type intPair [2]int
	checked := make(map[intPair]bool)
	for _, bucket := range index {
		if len(bucket) < 2 {
			continue
		}
		for bi := 0; bi < len(bucket); bi++ {
			for bj := bi + 1; bj < len(bucket); bj++ {
				pair := intPair{bucket[bi], bucket[bj]}
				if checked[pair] {
					continue
				}
				checked[pair] = true
				i, j := pair[0], pair[1]
				// Intersect primitive key sets, verify all shared keys match
				match := false
				allMatch := true
				for k, v1 := range primsList[i] {
					if v2, ok := primsList[j][k]; ok {
						match = true
						if v1 != v2 {
							allMatch = false
							break
						}
					}
				}
				if match && allMatch {
					return true
				}
			}
		}
	}

	return false
}

// modelMergeMergeListItemsIndexed merges source items into destination using an inverted index
func modelMergeMergeListItemsIndexed(sourceItems []any, dst *[]any, deduplicate bool) {
	// Build inverted index over destination's dict items
	destPrimitives := make([]map[string]any, len(*dst))
	for i, item := range *dst {
		if _, ok := modelMergeAsMap(item); ok {
			destPrimitives[i] = modelMergeExtractPrimitives(item)
		}
	}

	index := make(map[modelMergeKvPair][]int)
	for i, prims := range destPrimitives {
		if prims == nil {
			continue
		}
		for k, v := range prims {
			pair := modelMergeKvPair{key: k, value: v}
			index[pair] = append(index[pair], i)
		}
	}

	for _, srcItem := range sourceItems {
		srcMapVal, isMap := modelMergeAsMap(srcItem)
		if !isMap {
			*dst = append(*dst, srcItem)
			continue
		}

		srcPrims := modelMergeExtractPrimitives(srcMapVal)
		if len(srcPrims) == 0 {
			*dst = append(*dst, srcItem)
			continue
		}

		// Collect candidate dest indices
		candidateSet := make(map[int]bool)
		for k, v := range srcPrims {
			pair := modelMergeKvPair{key: k, value: v}
			if indices, exists := index[pair]; exists {
				for _, idx := range indices {
					candidateSet[idx] = true
				}
			}
		}

		// Check candidates in destination order (first-match semantics)
		candidates := make([]int, 0, len(candidateSet))
		for idx := range candidateSet {
			candidates = append(candidates, idx)
		}
		sort.Ints(candidates)

		matched := false
		for _, ci := range candidates {
			dp := destPrimitives[ci]
			if dp == nil {
				continue
			}
			// Intersect primitive key sets, verify all shared keys match
			hasShared := false
			allMatch := true
			for k, sv := range srcPrims {
				if dv, ok := dp[k]; ok {
					hasShared = true
					if sv != dv {
						allMatch = false
						break
					}
				}
			}
			if hasShared && allMatch {
				mergeModelMaps(srcMapVal, (*dst)[ci], deduplicate)
				// Update primitives cache after merge
				destPrimitives[ci] = modelMergeExtractPrimitives((*dst)[ci])
				matched = true
				break
			}
		}

		if !matched {
			// Append and update index so later source items can match
			newIdx := len(*dst)
			*dst = append(*dst, srcItem)
			destPrimitives = append(destPrimitives, srcPrims)
			for k, v := range srcPrims {
				pair := modelMergeKvPair{key: k, value: v}
				index[pair] = append(index[pair], newIdx)
			}
		}
	}
}
