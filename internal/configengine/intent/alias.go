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

// Package intent exposes the neutral resolved-intent contract. The IOS-XE
// resolver remains the backing implementation until the common CRD/object
// contract is generalized.
package intent

import (
	"context"

	configv1alpha1 "github.com/cisco/virtual-kubelet-cisco/api/config/v1alpha1"
	iosxeintent "github.com/cisco/virtual-kubelet-cisco/internal/drivers/iosxe/configdriver/intent"
)

type (
	ConfigMapReader = iosxeintent.ConfigMapReader
	Reader          = iosxeintent.Reader
	KeyRules        = iosxeintent.KeyRules
	Resolver        = iosxeintent.Resolver
	CLIBlock        = iosxeintent.CLIBlock
	ResolvedIntent  = iosxeintent.ResolvedIntent
)

func LoadSource(ctx context.Context, r ConfigMapReader, ns, deviceName string, src configv1alpha1.ConfigurationSource) (map[string]any, error) {
	return iosxeintent.LoadSource(ctx, r, ns, deviceName, src)
}

func CanonicalHash(res *ResolvedIntent) (string, error) {
	return iosxeintent.CanonicalHash(res)
}

func Merge(dst, src any) any {
	return iosxeintent.Merge(dst, src)
}

func MergeWithRules(dst, src any, rules KeyRules) any {
	return iosxeintent.MergeWithRules(dst, src, rules)
}

func Equal(a, b any) bool {
	return iosxeintent.Equal(a, b)
}

func FixYAML11BoolKeys(v any) any {
	return iosxeintent.FixYAML11BoolKeys(v)
}

func ExpandTemplate(tpl *configv1alpha1.IOSXETemplate, values map[string]string) (map[string]any, error) {
	return iosxeintent.ExpandTemplate(tpl, values)
}

func ExpandCLITemplate(tpl *configv1alpha1.IOSXETemplate, values map[string]string) (string, error) {
	return iosxeintent.ExpandCLITemplate(tpl, values)
}
