// Copyright 2026 Cisco Systems, Inc.
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

"use client";

import { motion } from "framer-motion";
import { Server, Cloud, Container, ArrowDown, Settings } from "lucide-react";

// Material Design style router icon: flat box with straight antennas, no RF signals
function RouterIcon({ className }: { className?: string }) {
  return (
    <svg
      viewBox="0 0 24 24"
      fill="currentColor"
      className={className}
    >
      {/* Left antenna - straight vertical line */}
      <rect x="6.5" y="2" width="1.5" height="7" rx="0.75" />
      {/* Right antenna - straight vertical line */}
      <rect x="16" y="2" width="1.5" height="7" rx="0.75" />
      {/* Antenna bases - small dots */}
      <circle cx="7.25" cy="9.5" r="1.2" />
      <circle cx="16.75" cy="9.5" r="1.2" />
      {/* Router body */}
      <rect x="1" y="11" width="22" height="10" rx="2" />
      {/* LED indicators */}
      <circle cx="6" cy="16" r="1.2" opacity="0.6" fill="currentColor" />
      <circle cx="10" cy="16" r="1.2" opacity="0.6" fill="currentColor" />
      <circle cx="14" cy="16" r="1.2" opacity="0.6" fill="currentColor" />
      <circle cx="18" cy="16" r="1.2" opacity="0.6" fill="currentColor" />
    </svg>
  );
}

export default function Architecture() {
  return (
    <section id="architecture" className="py-24 relative">
      <div className="absolute inset-0 bg-gradient-to-b from-background via-surface/30 to-background" />

      <div className="max-w-7xl mx-auto px-4 sm:px-6 lg:px-8 relative">
        {/* Section header */}
        <motion.div
          initial={{ opacity: 0, y: 20 }}
          whileInView={{ opacity: 1, y: 0 }}
          viewport={{ once: true, margin: "-100px" }}
          transition={{ duration: 0.6 }}
          className="text-center mb-16"
        >
          <h2 className="text-3xl sm:text-4xl md:text-5xl font-bold mb-6">
            How It <span className="gradient-text">Works</span>
          </h2>
          <p className="text-lg text-text-muted max-w-2xl mx-auto">
            A Kubernetes controller watches CiscoDevice custom resources and
            automatically deploys Virtual Kubelet providers that bridge your
            cluster to Cisco network devices via RESTCONF.
          </p>
        </motion.div>

        {/* Architecture diagram */}
        <motion.div
          initial={{ opacity: 0, y: 30 }}
          whileInView={{ opacity: 1, y: 0 }}
          viewport={{ once: true, margin: "-50px" }}
          transition={{ duration: 0.8 }}
          className="max-w-4xl mx-auto"
        >
          {/* Kubernetes Cluster */}
          <div className="relative">
            {/* K8s API Server */}
            <div className="arch-box flex items-center justify-center gap-3 py-5 mb-2 bg-gradient-to-r from-surface to-surface-light border-primary/30">
              <Cloud className="w-6 h-6 text-primary" />
              <div>
                <div className="text-lg font-semibold text-foreground">
                  Kubernetes API Server
                </div>
                <div className="text-sm text-text-muted">
                  Standard Kubernetes control plane
                </div>
              </div>
            </div>

            {/* Connector */}
            <div className="flex justify-center py-2">
              <div className="flex flex-col items-center">
                <div className="w-px h-8 bg-gradient-to-b from-primary to-accent" />
                <ArrowDown className="w-5 h-5 text-primary -mt-1" />
              </div>
            </div>

            {/* Controller Manager */}
            <div className="arch-box flex items-center justify-center gap-3 py-4 mb-2 bg-gradient-to-r from-surface to-surface-light border-accent/30">
              <Settings className="w-5 h-5 text-accent" />
              <div>
                <div className="text-base font-semibold text-foreground">
                  Controller Manager
                </div>
                <div className="text-xs text-text-muted">
                  Watches CiscoDevice CRDs &mdash; creates VK Deployments + ConfigMaps
                </div>
              </div>
            </div>

            {/* Connector */}
            <div className="flex justify-center py-2">
              <div className="flex flex-col items-center">
                <div className="w-px h-8 bg-gradient-to-b from-accent to-accent" />
                <ArrowDown className="w-5 h-5 text-accent -mt-1" />
              </div>
            </div>

            {/* VK Providers row */}
            <div className="grid grid-cols-1 md:grid-cols-3 gap-4 mb-2">
              {[1, 2, "N"].map((num) => (
                <motion.div
                  key={num}
                  initial={{ opacity: 0, scale: 0.9 }}
                  whileInView={{ opacity: 1, scale: 1 }}
                  viewport={{ once: true }}
                  transition={{
                    duration: 0.5,
                    delay: typeof num === "number" ? num * 0.1 : 0.3,
                  }}
                  className="arch-box flex items-center justify-center gap-3 py-4 border-accent/30"
                >
                  <Server className="w-5 h-5 text-accent" />
                  <div>
                    <div className="font-semibold text-foreground text-sm">
                      VK Provider
                    </div>
                    <div className="text-xs text-text-muted">Device {num}</div>
                  </div>
                </motion.div>
              ))}
            </div>

            {/* RESTCONF connectors */}
            <div className="grid grid-cols-3 gap-4 mb-2">
              {[0, 1, 2].map((i) => (
                <div key={i} className="flex flex-col items-center py-1">
                  <div className="w-px h-6 bg-gradient-to-b from-accent to-success" />
                  <div className="px-3 py-1 rounded-full bg-surface border border-border text-xs text-primary font-mono">
                    RESTCONF
                  </div>
                  <div className="w-px h-6 bg-gradient-to-b from-success to-accent" />
                  <ArrowDown className="w-4 h-4 text-success -mt-1" />
                </div>
              ))}
            </div>

            {/* Cisco Devices row */}
            <div className="grid grid-cols-1 md:grid-cols-3 gap-4">
              {[
                { name: "Cisco C8Kv1", ip: "192.0.2.24" },
                { name: "Cisco C8Kv2", ip: "192.0.2.25" },
                { name: "Cisco C8KvN", ip: "192.0.2.x" },
              ].map((device, i) => (
                <motion.div
                  key={device.name}
                  initial={{ opacity: 0, y: 20 }}
                  whileInView={{ opacity: 1, y: 0 }}
                  viewport={{ once: true }}
                  transition={{ duration: 0.5, delay: i * 0.15 }}
                  className="arch-box border-success/30 py-4"
                >
                  <div className="flex items-center justify-center gap-2 mb-3">
                    <RouterIcon className="w-5 h-5 text-success" />
                    <span className="font-semibold text-foreground text-sm">
                      {device.name}
                    </span>
                  </div>
                  <div className="inline-flex items-center gap-2 px-3 py-1.5 rounded-lg bg-success/10 border border-success/20">
                    <Container className="w-4 h-4 text-success" />
                    <span className="text-xs text-success font-mono">
                      Container
                    </span>
                  </div>
                  <div className="text-xs text-text-muted mt-2 font-mono">
                    {device.ip}
                  </div>
                </motion.div>
              ))}
            </div>
          </div>
        </motion.div>

        {/* How it works steps */}
        <motion.div
          initial={{ opacity: 0, y: 30 }}
          whileInView={{ opacity: 1, y: 0 }}
          viewport={{ once: true, margin: "-50px" }}
          transition={{ duration: 0.6, delay: 0.2 }}
          className="mt-20 grid grid-cols-1 md:grid-cols-3 gap-8"
        >
          <div className="relative">
            <div className="text-6xl font-bold text-primary/10 absolute -top-6 -left-2">
              01
            </div>
            <div className="relative pt-12">
              <h3 className="text-xl font-semibold mb-3 text-foreground">
                Define a CiscoDevice
              </h3>
              <p className="text-text-muted leading-relaxed">
                Create a CiscoDevice custom resource for each network device.
                The controller automatically provisions a Virtual Kubelet node
                that registers with the cluster.
              </p>
            </div>
          </div>
          <div className="relative">
            <div className="text-6xl font-bold text-primary/10 absolute -top-6 -left-2">
              02
            </div>
            <div className="relative pt-12">
              <h3 className="text-xl font-semibold mb-3 text-foreground">
                Schedule with kubectl
              </h3>
              <p className="text-text-muted leading-relaxed">
                Use standard kubectl commands to schedule pods. The VK node
                translates pod specs into RESTCONF API calls for Cisco IOS-XE
                App-Hosting.
              </p>
            </div>
          </div>
          <div className="relative">
            <div className="text-6xl font-bold text-primary/10 absolute -top-6 -left-2">
              03
            </div>
            <div className="relative pt-12">
              <h3 className="text-xl font-semibold mb-3 text-foreground">
                Edge Deployment
              </h3>
              <p className="text-text-muted leading-relaxed">
                Containers are deployed directly on Cisco Catalyst devices.
                The reconciler manages the full app-hosting lifecycle with
                automatic IP discovery and status reporting.
              </p>
            </div>
          </div>
        </motion.div>
      </div>
    </section>
  );
}
