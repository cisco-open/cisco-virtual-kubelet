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

package writers

import (
	"context"

	"github.com/cisco/virtual-kubelet-cisco/internal/drivers/iosxe/configdriver"
	"github.com/cisco/virtual-kubelet-cisco/internal/drivers/iosxe/configdriver/transport"
)

// skeleton is the Phase-0 stand-in for a real SectionWriter. It satisfies
// the interface, exposes a sensible (but unimplemented) error from the
// write path, and keeps the per-family file down to a one-line
// registration so adding a family in subsequent phases is a targeted
// edit rather than a boilerplate transplant.
//
// Once a family gains a real implementation, its file replaces the
// skeleton entry with a concrete type — no other file in the package
// needs to change.
type skeleton struct {
	family string
	paths  []string
}

func (s skeleton) Family() string      { return s.family }
func (s skeleton) YANGPaths() []string { return append([]string(nil), s.paths...) }

func (s skeleton) Fetch(context.Context, configdriver.TransportClient) (any, error) {
	return nil, ErrNotImplemented
}

func (s skeleton) Diff(desired, observed any) ([]transport.Op, error) {
	return nil, ErrNotImplemented
}

func (s skeleton) Apply(context.Context, configdriver.TransportClient, []transport.Op) error {
	return ErrNotImplemented
}

// registerSkeleton is a shorthand used from per-family init() functions.
func registerSkeleton(family string, paths ...string) {
	Register(skeleton{family: family, paths: paths})
}
