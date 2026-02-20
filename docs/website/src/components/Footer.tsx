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

import { Github, ExternalLink } from "lucide-react";

const footerLinks = {
  Project: [
    {
      name: "GitHub",
      href: "https://github.com/cisco-open/cisco-virtual-kubelet",
    },
    {
      name: "Documentation",
      href: "https://cisco-open.github.io/cisco-virtual-kubelet/docs/",
    },
    {
      name: "Releases",
      href: "https://github.com/cisco-open/cisco-virtual-kubelet/releases",
    },
    {
      name: "License",
      href: "https://github.com/cisco-open/cisco-virtual-kubelet/blob/main/LICENSE",
    },
  ],
  "Virtual Kubelet": [
    {
      name: "VK Project",
      href: "https://github.com/virtual-kubelet/virtual-kubelet",
    },
    { name: "VK Website", href: "https://virtual-kubelet.io/" },
    {
      name: "Providers",
      href: "https://github.com/virtual-kubelet/virtual-kubelet#providers",
    },
    { name: "CNCF", href: "https://www.cncf.io/" },
  ],
  Community: [
    {
      name: "Kubernetes Slack",
      href: "https://kubernetes.slack.com/messages/virtual-kubelet",
    },
    { name: "Cisco DevNet", href: "https://developer.cisco.com" },
    {
      name: "Contributing",
      href: "https://github.com/cisco-open/cisco-virtual-kubelet/blob/main/CONTRIBUTING.md",
    },
    {
      name: "Issues",
      href: "https://github.com/cisco-open/cisco-virtual-kubelet/issues",
    },
  ],
};

export default function Footer() {
  return (
    <footer className="relative border-t border-border bg-surface/30">
      <div className="max-w-7xl mx-auto px-4 sm:px-6 lg:px-8 py-16">
        <div className="grid grid-cols-1 md:grid-cols-4 gap-12">
          {/* Brand */}
          <div className="md:col-span-1">
            <a href="#" className="flex items-center gap-3 mb-4">
              <div className="w-9 h-9 rounded-lg bg-gradient-to-br from-primary to-accent flex items-center justify-center">
                <svg
                  viewBox="0 0 24 24"
                  fill="none"
                  className="w-5 h-5 text-white"
                  stroke="currentColor"
                  strokeWidth="2"
                >
                  <path d="M12 2L2 7l10 5 10-5-10-5z" />
                  <path d="M2 17l10 5 10-5" />
                  <path d="M2 12l10 5 10-5" />
                </svg>
              </div>
              <span className="text-lg font-bold text-foreground">
                Cisco VK
              </span>
            </a>
            <p className="text-sm text-text-muted leading-relaxed mb-6">
              Deploy container workloads on Cisco Catalyst series switches and
              IOS-XE devices using standard Kubernetes workflows.
            </p>
            <a
              href="https://github.com/cisco-open/cisco-virtual-kubelet"
              target="_blank"
              rel="noopener noreferrer"
              className="inline-flex items-center gap-2 text-sm text-text-muted hover:text-foreground transition-colors"
            >
              <Github className="w-4 h-4" />
              cisco-open/cisco-virtual-kubelet
            </a>
          </div>

          {/* Link columns */}
          {Object.entries(footerLinks).map(([title, links]) => (
            <div key={title}>
              <h4 className="text-sm font-semibold text-foreground mb-4 uppercase tracking-wider">
                {title}
              </h4>
              <ul className="space-y-3">
                {links.map((link) => (
                  <li key={link.name}>
                    <a
                      href={link.href}
                      target="_blank"
                      rel="noopener noreferrer"
                      className="group flex items-center gap-1.5 text-sm text-text-muted hover:text-primary transition-colors"
                    >
                      {link.name}
                      <ExternalLink className="w-3 h-3 opacity-0 group-hover:opacity-100 transition-opacity" />
                    </a>
                  </li>
                ))}
              </ul>
            </div>
          ))}
        </div>

        {/* Bottom bar */}
        <div className="mt-12 pt-8 border-t border-border flex flex-col sm:flex-row items-center justify-between gap-4">
          <p className="text-sm text-text-muted">
            © {new Date().getFullYear()} Cisco Systems, Inc. Licensed under{" "}
            <a
              href="https://github.com/cisco-open/cisco-virtual-kubelet/blob/main/LICENSE"
              target="_blank"
              rel="noopener noreferrer"
              className="text-primary hover:underline"
            >
              Apache 2.0
            </a>
          </p>
          <div className="flex items-center gap-4 text-sm text-text-muted">
            <span>
              Part of the{" "}
              <a
                href="https://virtual-kubelet.io/"
                target="_blank"
                rel="noopener noreferrer"
                className="text-primary hover:underline"
              >
                Virtual Kubelet
              </a>{" "}
              ecosystem
            </span>
            <span className="text-border">•</span>
            <a
              href="https://www.cncf.io/"
              target="_blank"
              rel="noopener noreferrer"
              className="hover:text-foreground transition-colors"
            >
              CNCF Sandbox Project
            </a>
          </div>
        </div>
      </div>
    </footer>
  );
}
