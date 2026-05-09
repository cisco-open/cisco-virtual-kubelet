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

package mapper

import (
	"sort"
	"strings"

	configv1alpha1 "github.com/cisco/virtual-kubelet-cisco/api/config/v1alpha1"
)

type AliasResolver struct {
	aliases []pathAlias
}

type pathAlias struct {
	prefix string
	rename string
}

func NewAliasResolver(in []configv1alpha1.PathAlias) AliasResolver {
	aliases := make([]pathAlias, 0, len(in))
	for _, a := range in {
		if a.Prefix == "" || a.Rename == "" {
			continue
		}
		aliases = append(aliases, pathAlias{
			prefix: normalizeCanonicalPath(a.Prefix),
			rename: strings.TrimSuffix(a.Rename, "/"),
		})
	}
	sort.SliceStable(aliases, func(i, j int) bool {
		return len(aliases[i].prefix) > len(aliases[j].prefix)
	})
	return AliasResolver{aliases: aliases}
}

func (r AliasResolver) Resolve(canonicalPath string) string {
	canonicalPath = normalizeCanonicalPath(canonicalPath)
	for _, alias := range r.aliases {
		if canonicalPath == alias.prefix {
			return alias.rename
		}
		if strings.HasPrefix(canonicalPath, alias.prefix+"/") {
			return alias.rename + strings.TrimPrefix(canonicalPath, alias.prefix)
		}
	}
	return canonicalPath
}
