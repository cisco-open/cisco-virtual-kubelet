// Copyright © 2026 Cisco Systems, Inc.
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
	"os"

	"github.com/spf13/cobra"
)

var rootCmd = &cobra.Command{
	Use:   "cisco-vk",
	Short: "Cisco Virtual Kubelet",
	Long: `Cisco Virtual Kubelet provides a Kubelet implementation backed by
Cisco AppHosting, along with a Kubernetes controller manager for
CiscoDevice custom resources.

Available subcommands:
  run       Start the Virtual Kubelet provider
  manager   Start the CRD controller manager`,
}

func init() {
	rootCmd.AddCommand(runCmd)
	rootCmd.AddCommand(managerCmd)
}

func main() {
	if err := rootCmd.Execute(); err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		os.Exit(1)
	}
}
