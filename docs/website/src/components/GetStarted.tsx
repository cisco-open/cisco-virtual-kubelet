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

import { useState } from "react";
import { motion } from "framer-motion";
import { Terminal, FileCode, Rocket, Copy, Check } from "lucide-react";

const tabs = [
  {
    id: "install",
    label: "Install",
    icon: Terminal,
  },
  {
    id: "config",
    label: "CiscoDevice CR",
    icon: FileCode,
  },
  {
    id: "deploy",
    label: "Deploy Pod",
    icon: Rocket,
  },
];

const codeBlocks: Record<string, { language: string; code: string }> = {
  install: {
    language: "bash",
    code: `# Install the CRD
kubectl apply -f config/crd/cisco.vk_ciscodevices.yaml

# Install the controller via Helm
helm install cisco-vk charts/cisco-virtual-kubelet \\
  --namespace cisco-vk-system --create-namespace

# Verify the controller is running
kubectl get pods -n cisco-vk-system`,
  },
  config: {
    language: "yaml",
    code: `# ciscodevice.yaml
apiVersion: cisco.vk/v1alpha1
kind: CiscoDevice
metadata:
  name: cat8kv-router
  namespace: cisco-vk-system
spec:
  driver: XE
  address: "192.0.2.24"
  port: 443
  username: admin
  password: cisco123
  tls:
    enabled: true
    insecureSkipVerify: true
  xe:
    networking:
      interface:
        type: VirtualPortGroup
        virtualPortGroup:
          dhcp: true
          interface: "0"
          guestInterface: 0`,
  },
  deploy: {
    language: "yaml",
    code: `# test-pod.yaml
apiVersion: v1
kind: Pod
metadata:
  name: dhcp-test-pod
  namespace: default
spec:
  nodeName: cat8kv-router    # Matches CiscoDevice name
  containers:
  - name: test-app
    image: flash:/hello-app.iosxe.tar
    resources:
      requests:
        memory: "64Mi"
        cpu: "250m"
      limits:
        memory: "128Mi"
        cpu: "500m"`,
  },
};

export default function GetStarted() {
  const [activeTab, setActiveTab] = useState("install");
  const [copied, setCopied] = useState(false);

  const copyToClipboard = () => {
    navigator.clipboard.writeText(codeBlocks[activeTab].code);
    setCopied(true);
    setTimeout(() => setCopied(false), 2000);
  };

  return (
    <section id="get-started" className="py-24 relative">
      <div className="absolute top-1/2 left-1/2 -translate-x-1/2 -translate-y-1/2 w-[600px] h-[600px] bg-primary/3 rounded-full blur-3xl" />

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
            Get <span className="gradient-text">Started</span>
          </h2>
          <p className="text-lg text-text-muted max-w-2xl mx-auto">
            Deploy your first container to a Cisco device in minutes using
            Helm and CiscoDevice custom resources.
          </p>
        </motion.div>

        {/* Prerequisites */}
        <motion.div
          initial={{ opacity: 0, y: 20 }}
          whileInView={{ opacity: 1, y: 0 }}
          viewport={{ once: true, margin: "-50px" }}
          transition={{ duration: 0.6 }}
          className="mb-12"
        >
          <h3 className="text-xl font-semibold mb-4 text-foreground">
            Prerequisites
          </h3>
          <div className="grid grid-cols-1 sm:grid-cols-2 lg:grid-cols-4 gap-4">
            {[
              { label: "Helm 3", desc: "Package manager for K8s" },
              { label: "Kubernetes Cluster", desc: "Any K8s distribution" },
              { label: "Cisco IOS-XE Device", desc: "With IOx & RESTCONF" },
              { label: "Container Image", desc: "Tar file on device flash" },
            ].map((req) => (
              <div
                key={req.label}
                className="px-4 py-3 rounded-xl bg-surface/60 border border-border"
              >
                <div className="text-sm font-semibold text-primary">
                  {req.label}
                </div>
                <div className="text-xs text-text-muted">{req.desc}</div>
              </div>
            ))}
          </div>
        </motion.div>

        {/* Code tabs */}
        <motion.div
          initial={{ opacity: 0, y: 30 }}
          whileInView={{ opacity: 1, y: 0 }}
          viewport={{ once: true, margin: "-50px" }}
          transition={{ duration: 0.7 }}
        >
          <div className="code-block">
            {/* Tab bar */}
            <div className="flex items-center justify-between border-b border-border bg-surface-light/50 px-2">
              <div className="flex items-center gap-1 overflow-x-auto">
                {tabs.map((tab) => (
                  <button
                    key={tab.id}
                    onClick={() => setActiveTab(tab.id)}
                    className={`flex items-center gap-2 px-4 py-3 text-sm font-medium transition-colors border-b-2 whitespace-nowrap ${
                      activeTab === tab.id
                        ? "text-primary border-primary"
                        : "text-text-muted border-transparent hover:text-foreground"
                    }`}
                  >
                    <tab.icon className="w-4 h-4" />
                    {tab.label}
                  </button>
                ))}
              </div>
              <button
                onClick={copyToClipboard}
                className="flex items-center gap-1.5 px-3 py-1.5 text-xs text-text-muted hover:text-foreground transition-colors rounded-lg hover:bg-surface-lighter"
              >
                {copied ? (
                  <>
                    <Check className="w-3.5 h-3.5 text-success" />
                    Copied!
                  </>
                ) : (
                  <>
                    <Copy className="w-3.5 h-3.5" />
                    Copy
                  </>
                )}
              </button>
            </div>

            {/* Code content */}
            <pre className="overflow-x-auto">
              <code className="font-mono text-sm">
                {codeBlocks[activeTab].code.split("\n").map((line, i) => (
                  <div key={i} className="flex">
                    <span className="select-none w-8 text-right pr-4 text-text-muted/40 text-xs leading-7">
                      {i + 1}
                    </span>
                    <span
                      className={`leading-7 ${
                        line.startsWith("#")
                          ? "text-text-muted"
                          : line.includes(":")
                          ? ""
                          : "text-foreground"
                      }`}
                    >
                      {highlightCode(line, codeBlocks[activeTab].language)}
                    </span>
                  </div>
                ))}
              </code>
            </pre>
          </div>
        </motion.div>
      </div>
    </section>
  );
}

function highlightCode(line: string, language: string): React.ReactNode {
  if (line.startsWith("#")) {
    return <span className="text-text-muted italic">{line}</span>;
  }

  if (language === "bash") {
    return line.split(" ").map((word, i) => {
      if (
        [
          "git",
          "cd",
          "make",
          "sudo",
          "export",
          "cisco-vk",
          "clone",
          "helm",
          "kubectl",
          "apply",
          "install",
          "get",
        ].includes(word)
      ) {
        return (
          <span key={i}>
            <span className="text-primary">{word}</span>{" "}
          </span>
        );
      }
      if (word.startsWith("--")) {
        return (
          <span key={i}>
            <span className="text-accent-light">{word}</span>{" "}
          </span>
        );
      }
      return <span key={i}>{word} </span>;
    });
  }

  if (language === "yaml") {
    const keyMatch = line.match(/^(\s*)([\w_-]+)(:)(.*)/);
    if (keyMatch) {
      return (
        <>
          {keyMatch[1]}
          <span className="text-primary">{keyMatch[2]}</span>
          <span className="text-foreground">{keyMatch[3]}</span>
          <span className="text-accent-light">{keyMatch[4]}</span>
        </>
      );
    }
    if (line.trim().startsWith("-")) {
      return <span className="text-success">{line}</span>;
    }
  }

  return line;
}
