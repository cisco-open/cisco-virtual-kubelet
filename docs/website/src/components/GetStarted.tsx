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
    label: "Installation",
    icon: Terminal,
  },
  {
    id: "config",
    label: "Configuration",
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
    code: `# Clone the repository
git clone https://github.com/cisco-open/cisco-virtual-kubelet.git
cd cisco-virtual-kubelet/

# Build the provider
make build

# Install the binary
sudo make install

# Export kubeconfig
export KUBECONFIG=~/.kube/config

# Start the provider
cisco-vk --config dev/config-dhcp-test.yaml`,
  },
  config: {
    language: "yaml",
    code: `# config.yaml
device:
  name: cat8kv-router
  driver: XE
  address: "192.0.2.24"    # Router IP Address
  port: 443
  username: admin
  password: cisco
  tls:
    enabled: true
    insecureSkipVerify: true
  networking:
    dhcpEnabled: true
    virtualPortGroup: "0"
    defaultVRF: ""

kubelet:
  node_name: "cat8kv-node"
  namespace: ""
  update_interval: "30s"
  os: "Linux"
  node_internal_ip: "192.0.2.24"`,
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
  nodeName: cat8kv-node       # Virtual Kubelet node
  containers:
  - name: test-app
    image: flash:/hello-app.iosxe.tar
    resources:
      requests:
        memory: "64Mi"
        cpu: "250m"
      limits:
        memory: "128Mi"
        cpu: "500m"

# Deploy with kubectl
# kubectl apply -f test-pod.yaml
# kubectl get pods -o wide`,
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
            Deploy your first container to a Cisco device in minutes. Follow
            these steps to set up the Cisco Virtual Kubelet provider.
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
              { label: "Go 1.23+", desc: "Build toolchain" },
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
