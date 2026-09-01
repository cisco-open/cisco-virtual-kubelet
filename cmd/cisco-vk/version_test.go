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

package main

import (
	"bytes"
	"testing"

	"github.com/spf13/cobra"
)

func TestVersionSurfacesReportInjectedProvenance(t *testing.T) {
	oldVersion, oldCommit, oldBuildTime := Version, GitCommit, BuildTime
	Version = "v2026.9.0"
	GitCommit = "0123456789abcdef"
	BuildTime = "2026-09-01T07:00:00Z"
	t.Cleanup(func() {
		Version, GitCommit, BuildTime = oldVersion, oldCommit, oldBuildTime
	})

	const want = "cisco-vk v2026.9.0 (commit=0123456789abcdef, built=2026-09-01T07:00:00Z)\n"
	for _, args := range [][]string{{"version"}, {"--version"}} {
		root := &cobra.Command{Use: "cisco-vk"}
		configureVersionSurface(root)
		var output bytes.Buffer
		root.SetOut(&output)
		root.SetErr(&output)
		root.SetArgs(args)
		if err := root.Execute(); err != nil {
			t.Fatalf("cisco-vk %v: %v", args, err)
		}
		if got := output.String(); got != want {
			t.Fatalf("cisco-vk %v output=%q, want %q", args, got, want)
		}
	}
}

func TestVersionCommandRejectsArguments(t *testing.T) {
	command := newVersionCommand()
	command.SetArgs([]string{"extra"})
	if err := command.Execute(); err == nil {
		t.Fatal("version command accepted an argument")
	}
}
