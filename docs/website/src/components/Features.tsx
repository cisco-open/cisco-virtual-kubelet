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
import {
  Box,
  Network,
  Shield,
  Activity,
  Layers,
  LineChart,
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
      "Extensible driver pattern with IOS-XE (Catalyst 8000V, Catalyst 9000) available today. Add new device types through a clean driver interface.",
    color: "from-accent to-accent-light",
    glowColor: "accent",
  },
  {
    icon: Activity,
    title: "Lifecycle & Recovery",
    description:
      "Full pod lifecycle via RESTCONF with an automatic recovery loop that reprocesses stuck pods using exponential backoff.",
    color: "from-success to-emerald-400",
    glowColor: "success",
  },
  {
    icon: LineChart,
    title: "Observability & Topology",
    description:
      "Prometheus metrics for device and interface health, plus OpenTelemetry topology traces with CDP/OSPF neighbors and hosted apps.",
    color: "from-primary-light to-primary",
    glowColor: "primary",
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
      "Device credentials injected from Kubernetes Secrets via secretKeyRef. Passwords never touch ConfigMaps or etcd in plaintext.",
    color: "from-success to-teal-400",
    glowColor: "success",
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
    <section id="features" className="py-24 relative">
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
            Make{" "}
            <span className="gradient-text">Edge Computing</span>
            <br />
            Simple and Powerful
          </h2>
          <p className="text-lg text-text-muted max-w-2xl mx-auto">
            Built on the Virtual Kubelet framework, Cisco Virtual Kubelet brings
            cloud-native container orchestration to your network infrastructure.
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
              <h3 className="text-xl font-semibold mb-3 text-foreground">
                {feature.title}
              </h3>
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
