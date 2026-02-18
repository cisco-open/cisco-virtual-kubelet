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
import { ArrowRight, Github, Terminal } from "lucide-react";
import NetworkAnimation from "./NetworkAnimation";

export default function Hero() {
  return (
    <section className="relative min-h-screen flex items-center justify-center overflow-hidden">
      {/* Animated network background */}
      <div className="absolute inset-0">
        <NetworkAnimation />
      </div>

      {/* Radial gradient overlays */}
      <div className="absolute inset-0 bg-gradient-to-b from-background/40 via-background/60 to-background" />
      <div className="absolute top-0 left-1/2 -translate-x-1/2 w-[800px] h-[600px] bg-primary/5 rounded-full blur-3xl" />
      <div className="absolute bottom-0 right-0 w-[600px] h-[400px] bg-accent/5 rounded-full blur-3xl" />

      <div className="relative z-10 max-w-7xl mx-auto px-4 sm:px-6 lg:px-8 pt-24 pb-16">
        <div className="text-center">
          {/* Badge */}
          <motion.div
            initial={{ opacity: 0, y: 20 }}
            animate={{ opacity: 1, y: 0 }}
            transition={{ duration: 0.6, delay: 0.2 }}
            className="inline-flex items-center gap-2 px-4 py-2 mb-8 rounded-full bg-surface/80 backdrop-blur-sm border border-border text-sm text-text-muted"
          >
            <span className="flex h-2 w-2 rounded-full bg-success animate-pulse" />
            Open Source — Apache 2.0 Licensed
          </motion.div>

          {/* Title */}
          <motion.h1
            initial={{ opacity: 0, y: 30 }}
            animate={{ opacity: 1, y: 0 }}
            transition={{ duration: 0.7, delay: 0.3 }}
            className="text-4xl sm:text-5xl md:text-7xl font-bold tracking-tight mb-6 leading-tight"
          >
            Deploy Containers to
            <br />
            <span className="gradient-text">Cisco Network Devices</span>
          </motion.h1>

          {/* Subtitle */}
          <motion.p
            initial={{ opacity: 0, y: 30 }}
            animate={{ opacity: 1, y: 0 }}
            transition={{ duration: 0.7, delay: 0.4 }}
            className="text-lg sm:text-xl text-text-muted max-w-3xl mx-auto mb-10 leading-relaxed"
          >
            A{" "}
            <span className="text-foreground font-medium">
              Virtual Kubelet provider
            </span>{" "}
            that enables Kubernetes to schedule container workloads on Cisco
            Catalyst series switches and IOS-XE devices with App-Hosting
            capabilities.
          </motion.p>

          {/* CTA Buttons */}
          <motion.div
            initial={{ opacity: 0, y: 30 }}
            animate={{ opacity: 1, y: 0 }}
            transition={{ duration: 0.7, delay: 0.5 }}
            className="flex flex-col sm:flex-row items-center justify-center gap-4 mb-16"
          >
            <a
              href="#get-started"
              className="group flex items-center gap-2 px-8 py-3.5 bg-gradient-to-r from-primary to-primary-dark text-white font-medium rounded-xl transition-all hover:shadow-lg hover:shadow-primary/25 hover:scale-105"
            >
              Get Started
              <ArrowRight className="w-4 h-4 group-hover:translate-x-1 transition-transform" />
            </a>
            <a
              href="https://github.com/cisco-open/cisco-virtual-kubelet"
              target="_blank"
              rel="noopener noreferrer"
              className="group flex items-center gap-2 px-8 py-3.5 bg-surface/80 backdrop-blur-sm border border-border text-foreground font-medium rounded-xl transition-all hover:bg-surface-light hover:border-primary/50"
            >
              <Github className="w-4 h-4" />
              View on GitHub
            </a>
          </motion.div>

          {/* Quick install command */}
          <motion.div
            initial={{ opacity: 0, y: 30 }}
            animate={{ opacity: 1, y: 0 }}
            transition={{ duration: 0.7, delay: 0.6 }}
            className="max-w-2xl mx-auto"
          >
            <div className="code-block group relative">
              <div className="flex items-center gap-2 px-4 py-3 border-b border-border bg-surface-light/50">
                <Terminal className="w-4 h-4 text-text-muted" />
                <span className="text-xs text-text-muted font-mono">
                  Quick Install
                </span>
              </div>
              <pre className="text-left">
                <code className="font-mono text-sm">
                  <span className="text-text-muted">$</span>{" "}
                  <span className="text-primary">git clone</span>{" "}
                  <span className="text-foreground">
                    https://github.com/cisco-open/cisco-virtual-kubelet.git
                  </span>
                  {"\n"}
                  <span className="text-text-muted">$</span>{" "}
                  <span className="text-primary">cd</span>{" "}
                  <span className="text-foreground">
                    cisco-virtual-kubelet
                  </span>
                  {"\n"}
                  <span className="text-text-muted">$</span>{" "}
                  <span className="text-primary">make</span>{" "}
                  <span className="text-accent-light">build</span>
                  {"\n"}
                  <span className="text-text-muted">$</span>{" "}
                  <span className="text-primary">sudo make</span>{" "}
                  <span className="text-accent-light">install</span>
                </code>
              </pre>
            </div>
          </motion.div>
        </div>
      </div>

      {/* Scroll indicator */}
      <motion.div
        initial={{ opacity: 0 }}
        animate={{ opacity: 1 }}
        transition={{ delay: 1.5 }}
        className="absolute bottom-8 left-1/2 -translate-x-1/2"
      >
        <motion.div
          animate={{ y: [0, 8, 0] }}
          transition={{ repeat: Infinity, duration: 2 }}
          className="w-6 h-10 rounded-full border-2 border-text-muted/30 flex items-start justify-center p-1.5"
        >
          <div className="w-1.5 h-1.5 rounded-full bg-primary" />
        </motion.div>
      </motion.div>
    </section>
  );
}
