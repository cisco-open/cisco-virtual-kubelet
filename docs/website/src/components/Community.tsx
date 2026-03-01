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
  Github,
  MessageSquare,
  Bug,
  Users,
  ExternalLink,
  Heart,
} from "lucide-react";

const communityLinks = [
  {
    icon: Github,
    title: "GitHub Repository",
    description:
      "Star the repo, explore the code, and contribute to the project.",
    href: "https://github.com/cisco-open/cisco-virtual-kubelet",
    iconBg: "bg-primary/10 border-primary/20",
    iconColor: "text-primary",
    ctaColor: "text-primary",
    cta: "View Repository",
  },
  {
    icon: Bug,
    title: "Report Issues",
    description:
      "Found a bug or have a feature request? Open an issue on GitHub.",
    href: "https://github.com/cisco-open/cisco-virtual-kubelet/issues",
    iconBg: "bg-accent/10 border-accent/20",
    iconColor: "text-accent",
    ctaColor: "text-accent",
    cta: "Open Issue",
  },
  {
    icon: MessageSquare,
    title: "Slack Community",
    description:
      "Join the #virtual-kubelet channel in Kubernetes Slack for discussions.",
    href: "https://kubernetes.slack.com/messages/virtual-kubelet",
    iconBg: "bg-success/10 border-success/20",
    iconColor: "text-success",
    ctaColor: "text-success",
    cta: "Join Slack",
  },
  {
    icon: Users,
    title: "Cisco DevNet",
    description:
      "Access developer resources, documentation, and Cisco community support.",
    href: "https://developer.cisco.com",
    iconBg: "bg-primary/10 border-primary/20",
    iconColor: "text-primary",
    ctaColor: "text-primary",
    cta: "Visit DevNet",
  },
];

export default function Community() {
  return (
    <section id="community" className="py-24 relative">
      <div className="absolute bottom-0 left-1/2 -translate-x-1/2 w-[800px] h-[400px] bg-accent/3 rounded-full blur-3xl" />

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
            Building the{" "}
            <span className="gradient-text">Community Together</span>
          </h2>
          <p className="text-lg text-text-muted max-w-2xl mx-auto">
            Join the community and interact with maintainers and other users.
            Contributions are welcome — help shape the future of edge computing
            with Kubernetes.
          </p>
        </motion.div>

        {/* Community links grid */}
        <div className="grid grid-cols-1 md:grid-cols-2 gap-6 mb-16">
          {communityLinks.map((link, i) => (
            <motion.a
              key={link.title}
              href={link.href}
              target="_blank"
              rel="noopener noreferrer"
              initial={{ opacity: 0, y: 20 }}
              whileInView={{ opacity: 1, y: 0 }}
              viewport={{ once: true }}
              transition={{ duration: 0.5, delay: i * 0.1 }}
              className="card-hover group flex items-start gap-5 p-6 rounded-2xl bg-surface/60 backdrop-blur-sm border border-border"
            >
              <div
                className={`shrink-0 p-3 rounded-xl border ${link.iconBg}`}
              >
                <link.icon className={`w-6 h-6 ${link.iconColor}`} />
              </div>
              <div className="flex-1 min-w-0">
                <h3 className="text-lg font-semibold text-foreground mb-1 flex items-center gap-2">
                  {link.title}
                  <ExternalLink className="w-4 h-4 text-text-muted opacity-0 group-hover:opacity-100 transition-opacity" />
                </h3>
                <p className="text-text-muted text-sm leading-relaxed mb-3">
                  {link.description}
                </p>
                <span
                  className={`text-sm font-medium ${link.ctaColor} group-hover:underline`}
                >
                  {link.cta} →
                </span>
              </div>
            </motion.a>
          ))}
        </div>

        {/* Contributing CTA */}
        <motion.div
          initial={{ opacity: 0, y: 20 }}
          whileInView={{ opacity: 1, y: 0 }}
          viewport={{ once: true }}
          transition={{ duration: 0.6 }}
          className="text-center p-8 sm:p-12 rounded-2xl bg-gradient-to-br from-surface via-surface-light to-surface border border-border relative overflow-hidden"
        >
          <div className="absolute top-0 right-0 w-64 h-64 bg-primary/5 rounded-full blur-3xl" />
          <div className="absolute bottom-0 left-0 w-64 h-64 bg-accent/5 rounded-full blur-3xl" />

          <div className="relative">
            <Heart className="w-10 h-10 text-primary mx-auto mb-4" />
            <h3 className="text-2xl sm:text-3xl font-bold mb-4 text-foreground">
              Want to Contribute?
            </h3>
            <p className="text-text-muted max-w-lg mx-auto mb-8 leading-relaxed">
              We welcome contributions of all kinds — whether it&apos;s new
              features, bug fixes, documentation improvements, or new driver
              implementations for additional Cisco device types.
            </p>
            <div className="flex flex-col sm:flex-row items-center justify-center gap-4">
              <a
                href="https://github.com/cisco-open/cisco-virtual-kubelet/blob/main/CONTRIBUTING.md"
                target="_blank"
                rel="noopener noreferrer"
                className="flex items-center gap-2 px-6 py-3 bg-gradient-to-r from-primary to-primary-dark text-white font-medium rounded-xl transition-all hover:shadow-lg hover:shadow-primary/25 hover:scale-105"
              >
                Contributing Guide
                <ExternalLink className="w-4 h-4" />
              </a>
              <a
                href="https://github.com/cisco-open/cisco-virtual-kubelet"
                target="_blank"
                rel="noopener noreferrer"
                className="flex items-center gap-2 px-6 py-3 bg-surface border border-border text-foreground font-medium rounded-xl transition-all hover:bg-surface-light hover:border-primary/50"
              >
                <Github className="w-4 h-4" />
                Star on GitHub
              </a>
            </div>
          </div>
        </motion.div>
      </div>
    </section>
  );
}
