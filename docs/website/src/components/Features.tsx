// Copyright 2026 Cisco Systems Inc.
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
import {
  Box,
  Network,
  Shield,
  Activity,
  Layers,
  LineChart,
  FileCode,
  ArrowUpCircle,
  Terminal,
} from "lucide-react";

const features = [
  {
    icon: Box,
    title: "Native Kubernetes Integration",
    description:
      "Deploy containers to Cisco devices using standard kubectl commands. No new tools to learn — just your familiar Kubernetes workflow.",
    color: "from-primary to-primary-dark",
    glowColor: "primary",
  },
  {
    icon: Layers,
    title: "Driver-Based Architecture",
    description:
      "Extensible driver pattern with IOS-XE (Catalyst 8000V, Catalyst 9000) available today and Beta support for Cisco Nexus (NX-OS). Add new device types through a clean driver interface.",
    color: "from-accent to-accent-light",
    glowColor: "accent",
    beta: true,
  },
  {
    icon: Activity,
    title: "Full App-Hosting Lifecycle",
    description:
      "Create, monitor, and delete containers via RESTCONF on IOS-XE or NX-API CLI on NX-OS, with an automatic recovery loop that reprocesses stuck pods using exponential backoff.",
    color: "from-success to-emerald-400",
    glowColor: "success",
  },
  {
    icon: FileCode,
    title: "Network as Code",
    description:
      "Declare device configuration in Kubernetes with IOSXEConfig and NXOSConfig — shared drift detection and verification, with revisions and transactional apply available on IOS XE.",
    color: "from-primary to-accent",
    glowColor: "primary",
    beta: true,
  },
  {
    icon: Network,
    title: "Controller Extension API",
    description:
      "Alpha, report-only contracts for future controller adapters. The current scaffold ships with zero product adapters and cannot apply, prune, or remotely delete controller state.",
    color: "from-primary to-accent",
    glowColor: "primary",
    beta: true,
    maturity: "Alpha",
  },
  {
    icon: ArrowUpCircle,
    title: "Software Lifecycle",
    description:
      "Upgrade or downgrade IOS-XE through IOSXESoftwareUpgrade: transfer a verified image, activate it, and verify recovery. Software mutation is explicitly gated and disabled by default.",
    color: "from-accent to-accent-light",
    glowColor: "accent",
    beta: true,
  },
  {
    icon: Network,
    title: "Native Topology Placement",
    description:
      "Project protected site and failure-domain labels onto Nodes for native Kubernetes affinity and topology spread. Managed mode requires Kubernetes 1.35+; no third-party scheduler is needed.",
    color: "from-primary to-accent",
    glowColor: "primary",
    beta: true,
    maturity: "Opt-in",
  },
  {
    icon: ArrowUpCircle,
    title: "Topology-Aware Rollouts",
    description:
      "Approve immutable IOSXESoftwareRollout plans with canaries and domain budgets. Freeze each target's topology-selected image URL, digest and Secret identity, then track combined activation and recovery.",
    color: "from-accent to-accent-light",
    glowColor: "accent",
    beta: true,
    maturity: "Alpha",
  },
  {
    icon: Shield,
    title: "Secure IOS-XE gNOI",
    description:
      "Use verified TLS, secure-password metadata and opt-in CSR-based OS-service certificate provisioning. Trust is isolated to gNOI; read-only OS.Verify never installs certificates.",
    color: "from-success to-teal-400",
    glowColor: "success",
    beta: true,
  },
  {
    icon: Layers,
    title: "PDB-Aware Drain",
    description:
      "Opt into Kubernetes Eviction for the documented eligible workloads before reload, with device-clean and recovery checks. Disabled by default; not general evacuation or a zero-downtime guarantee.",
    color: "from-primary to-accent",
    glowColor: "primary",
    beta: true,
    maturity: "Preview",
  },
  {
    icon: Terminal,
    title: "Device Operations",
    description:
      "Run auditable show commands and read-only gNOI probes from Kubernetes via the DeviceOperation CRD — no SSH sessions required.",
    color: "from-primary-light to-primary",
    glowColor: "primary",
    beta: true,
  },
  {
    icon: Network,
    title: "Flexible Networking",
    description:
      "DHCP or static allocation across VirtualPortGroup, AppGigabitEthernet (access and trunk with VLAN), and Management interfaces.",
    color: "from-accent-light to-accent",
    glowColor: "accent",
  },
  {
    icon: Shield,
    title: "Secure by Design",
    description:
      "Keep credentials in Secrets. Managed mode separates shared app-hosting and network-management ServiceAccounts with RO/RW role options, native admission and exact worker binding. RBAC alone is not per-device authorization.",
    color: "from-success to-teal-400",
    glowColor: "success",
  },
  {
    icon: LineChart,
    title: "Telemetry & Observability",
    description:
      "MDT-over-gNMI subscriptions emit OpenTelemetry metrics, logs, and traces, plus Prometheus device/interface health and CDP/OSPF topology with hosted apps.",
    color: "from-primary-light to-primary",
    glowColor: "primary",
    beta: true,
  },
];

const containerVariants = {
  hidden: {},
  visible: {
    transition: {
      staggerChildren: 0.1,
    },
  },
};

const itemVariants = {
  hidden: { opacity: 0, y: 30 },
  visible: {
    opacity: 1,
    y: 0,
    transition: { duration: 0.6, ease: "easeOut" as const },
  },
};

export default function Features() {
  return (
    <section id="features" className="py-24 relative overflow-hidden">
      {/* Background effects */}
      <div className="absolute top-0 left-0 w-[400px] h-[400px] bg-primary/3 rounded-full blur-3xl" />
      <div className="absolute bottom-0 right-0 w-[400px] h-[400px] bg-accent/3 rounded-full blur-3xl" />

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
            Bring{" "}
            <span className="gradient-text">Kubernetes to the Network</span>
            <br />
            Apps, Configuration and Upgrades
          </h2>
          <p className="text-lg text-text-muted max-w-2xl mx-auto">
            Built on the Virtual Kubelet framework, Cisco Virtual Kubelet brings
            cloud-native workload placement, configuration and opt-in IOS-XE
            software campaigns to your network infrastructure.
          </p>
          <p className="text-sm text-text-muted max-w-3xl mx-auto mt-5 leading-relaxed">
            October adds topology-aware campaigns and secure gNOI provisioning.
            Declared domains do not prove healthy redundant paths, link headroom
            or critical-service availability. Independent staging, persistent
            image caching and automatic network-path health gates remain roadmap work.
            Optional Kubernetes 1.37 TAS is an experimental scheduler lane.
            {" "}<a className="text-primary underline" href="/cisco-virtual-kubelet/docs/releases/v2026.10.0/">October scope and limitations</a>
          </p>
        </motion.div>

        {/* Feature grid */}
        <motion.div
          variants={containerVariants}
          initial="hidden"
          whileInView="visible"
          viewport={{ once: true, margin: "-50px" }}
          className="grid grid-cols-1 md:grid-cols-2 lg:grid-cols-3 gap-6"
        >
          {features.map((feature) => (
            <motion.div
              key={feature.title}
              variants={itemVariants}
              className="card-hover group relative p-6 rounded-2xl bg-surface/60 backdrop-blur-sm border border-border"
            >
              {/* Icon */}
              <div
                className={`inline-flex p-3 rounded-xl bg-gradient-to-br ${feature.color} mb-5`}
              >
                <feature.icon className="w-6 h-6 text-white" />
              </div>

              {/* Content */}
              <div className="flex flex-wrap items-center gap-2 mb-3">
                <h3 className="text-xl font-semibold text-foreground">
                  {feature.title}
                </h3>
                {feature.beta && (
                  <span className="inline-flex items-center px-2 py-0.5 rounded text-xs font-semibold bg-amber-500/10 text-amber-400 border border-amber-500/25 shrink-0">
                    {feature.maturity ?? "Beta"}
                  </span>
                )}
              </div>
              <p className="text-text-muted leading-relaxed">
                {feature.description}
              </p>

              {/* Hover glow */}
              <div
                aria-hidden
                className={`pointer-events-none absolute -inset-px rounded-2xl bg-gradient-to-br ${feature.color} opacity-0 group-hover:opacity-5 transition-opacity blur-xl`}
              />
            </motion.div>
          ))}
        </motion.div>
      </div>
    </section>
  );
}
