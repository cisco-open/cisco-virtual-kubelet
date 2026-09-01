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
	"fmt"

	"github.com/spf13/cobra"
)

// Version, GitCommit, and BuildTime are populated by release builds with
// -ldflags. Development builds identify themselves explicitly instead of
// reporting stale release provenance.
var (
	Version   = "devel"
	GitCommit = "unknown"
	BuildTime = "unknown"
)

func buildProvenance() string {
	return fmt.Sprintf("%s (commit=%s, built=%s)", Version, GitCommit, BuildTime)
}

func configureVersionSurface(root *cobra.Command) {
	root.Version = buildProvenance()
	root.SetVersionTemplate("{{.Name}} {{.Version}}\n")
	root.AddCommand(newVersionCommand())
}

func newVersionCommand() *cobra.Command {
	return &cobra.Command{
		Use:   "version",
		Short: "Print build provenance",
		Args:  cobra.NoArgs,
		Run: func(cmd *cobra.Command, _ []string) {
			fmt.Fprintf(cmd.OutOrStdout(), "cisco-vk %s\n", buildProvenance())
		},
	}
}
