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
    code: `# The image isn't on a public registry yet — build it locally.
git clone https://github.com/cisco-open/cisco-virtual-kubelet.git
cd cisco-virtual-kubelet

docker build -t <your-registry>/cisco-vk:dev .
docker push <your-registry>/cisco-vk:dev

# Install the controller with Helm (chart lives in-repo)
helm install cvk ./charts/cisco-virtual-kubelet \\
  --namespace cvk-system --create-namespace \\
  --set image.repository=<your-registry>/cisco-vk \\
  --set image.tag=dev

# Verify the CRD and controller are up
kubectl get crd ciscodevices.cisco.vk
kubectl -n cvk-system get pods`,
  },
  config: {
    language: "yaml",
    code: `# 1. Secret — device password, never in etcd plaintext
apiVersion: v1
kind: Secret
metadata:
  name: cat9000-1-creds
  namespace: default
type: Opaque
stringData:
  password: <device-password>
---
# 2. CiscoDevice — references the Secret via secretKeyRef
apiVersion: cisco.vk/v1alpha1
kind: CiscoDevice
metadata:
  name: cat9000-1
  namespace: default
spec:
  driver: XE
  address: "192.168.1.100"
  port: 443
  username: admin
  credentialSecretRef:
    name: cat9000-1-creds
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
    code: `apiVersion: v1
kind: Pod
metadata:
  name: hello-app
spec:
  nodeName: cat9000-1
  tolerations:
  - key: virtual-kubelet.io/provider
    operator: Exists
  containers:
  - name: hello
    image: flash:/hello-app.iosxe.tar
    resources:
      requests:
        memory: "256Mi"
        cpu: "500m"
      limits:
        memory: "512Mi"
        cpu: "1000m"`,
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
              { label: "Kubernetes 1.28+", desc: "Any distribution" },
              { label: "Helm v3 + Docker", desc: "To install the chart and build the image" },
              { label: "Container Registry", desc: "Reachable from the cluster" },
              { label: "Cisco IOS-XE Device", desc: "Cat 8000V or Cat 9000" },
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
          "install",
          "get",
          "docker",
          "build",
          "push",
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
