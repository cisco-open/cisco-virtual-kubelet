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
  Cpu,
  Network,
  Activity,
  Layers,
  Settings,
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
      "Extensible driver pattern supporting Catalyst 8Kv device types (IOS-XE). Easily add support for new device types through the driver interface.",
    color: "from-accent to-accent-light",
    glowColor: "accent",
  },
  {
    icon: Activity,
    title: "Full Lifecycle Management",
    description:
      "Create, monitor, and delete containers via RESTCONF APIs. Complete pod lifecycle management with real-time status updates.",
    color: "from-success to-emerald-400",
    glowColor: "success",
  },
  {
    icon: Cpu,
    title: "Resource Management",
    description:
      "Precise CPU, memory, and storage allocation per container. Ensure your workloads get exactly the resources they need on the edge.",
    color: "from-primary-light to-primary",
    glowColor: "primary",
  },
  {
    icon: Network,
    title: "Flexible Networking",
    description:
      "Support for DHCP IP allocation via Virtual Port Groups. Automatic IP discovery from device operational data or ARP tables.",
    color: "from-accent-light to-accent",
    glowColor: "accent",
  },
  {
    icon: Settings,
    title: "Helm & CRD Operator",
    description:
      "Deploy via Helm and manage devices as CiscoDevice custom resources. The controller automatically provisions VK nodes and keeps them in sync.",
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
                className={`absolute -inset-px rounded-2xl bg-gradient-to-br ${feature.color} opacity-0 group-hover:opacity-5 transition-opacity blur-xl`}
              />
            </motion.div>
          ))}
        </motion.div>
      </div>
    </section>
  );
}
