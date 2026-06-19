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

// Package transport contains the IOS-XE concrete RESTCONF, NETCONF, and gNMI
// adapters. The public contract types are aliases to the platform-neutral
// configengine transport package so IOS-XE and NX-OS share one runtime
// interface.
package transport

import configtransport "github.com/cisco/virtual-kubelet-cisco/internal/configengine/transport"

type (
	Kind               = configtransport.Kind
	Capabilities       = configtransport.Capabilities
	Op                 = configtransport.Op
	PathElement        = configtransport.PathElement
	Verb               = configtransport.Verb
	SubscribeMode      = configtransport.SubscribeMode
	SubscribeEvent     = configtransport.SubscribeEvent
	SubscribeCapable   = configtransport.SubscribeCapable
	TxFetcher          = configtransport.TxFetcher
	TxHandle           = configtransport.TxHandle
	ConfirmedCommitter = configtransport.ConfirmedCommitter
	DiagnosticExecer   = configtransport.DiagnosticExecer
	CommandResult      = configtransport.CommandResult
	Interface          = configtransport.Interface
	RetryPolicy        = configtransport.RetryPolicy
)

const (
	KindREST     Kind = configtransport.KindREST
	KindRESTCONF Kind = configtransport.KindRESTCONF
	KindNETCONF  Kind = configtransport.KindNETCONF
	KindGNMI     Kind = configtransport.KindGNMI
	KindNXAPI    Kind = configtransport.KindNXAPI

	VerbReplace Verb = configtransport.VerbReplace
	VerbMerge   Verb = configtransport.VerbMerge
	VerbDelete  Verb = configtransport.VerbDelete
	VerbCLI     Verb = configtransport.VerbCLI

	SubscribeOnChange SubscribeMode = configtransport.SubscribeOnChange
	SubscribeSample   SubscribeMode = configtransport.SubscribeSample
)

var ErrUnsupported = configtransport.ErrUnsupported
