// Copyright © 2026 Cisco Systems Inc.
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//     http://www.apache.org/licenses/LICENSE-2.0

// Command terraform-provider-iosxeconfig is the Hashicorp-Terraform
// plugin entrypoint. It is intentionally tiny — every interesting
// shape lives under internal/provider, mirroring the
// terraform-plugin-framework convention.
//
// This binary publishes one resource, iosxeconfig_config, that
// creates / updates / deletes IOSXEConfig CRs against a target
// cluster. The controller-side driver continues to do the device
// work; this provider is only an authoring surface.
package main

import (
	"context"
	"flag"
	"log"

	"github.com/hashicorp/terraform-plugin-framework/providerserver"

	provider "github.com/cisco-open/terraform-provider-iosxeconfig/internal/provider"
)

const providerAddress = "registry.terraform.io/cisco-open/iosxeconfig"

func main() {
	debug := flag.Bool("debug", false,
		"set to true to run the provider with support for debuggers like delve")
	flag.Parse()

	if err := providerserver.Serve(context.Background(), provider.New, providerserver.ServeOpts{
		Address: providerAddress,
		Debug:   *debug,
	}); err != nil {
		log.Fatalf("serve provider: %v", err)
	}
}
