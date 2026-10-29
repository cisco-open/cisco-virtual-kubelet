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

const links = [
  { label: "Documentation", href: "/cisco-virtual-kubelet/docs/" },
  { label: "Releases", href: "https://github.com/cisco-open/cisco-virtual-kubelet/releases" },
  { label: "Contribute", href: "https://github.com/cisco-open/cisco-virtual-kubelet/blob/main/CONTRIBUTING.md" },
  { label: "Report an issue", href: "https://github.com/cisco-open/cisco-virtual-kubelet/issues" },
];

const linkClass =
  "rounded text-text-muted hover:text-primary transition-colors focus-visible:outline-2 focus-visible:outline-offset-4 focus-visible:outline-primary";

export default function Footer() {
  return (
    <footer className="border-t border-border bg-surface/30">
      <div className="max-w-6xl mx-auto px-4 sm:px-6 lg:px-8 py-10">
        <div className="flex flex-col sm:flex-row sm:items-center sm:justify-between gap-6">
          <a href="#" className="rounded text-lg font-bold focus-visible:outline-2 focus-visible:outline-offset-4 focus-visible:outline-primary">
            Cisco <span className="gradient-text">Virtual Kubelet</span>
          </a>
          <nav aria-label="Project links" className="flex flex-wrap gap-x-6 gap-y-3 text-sm">
            {links.map((link) => (
              <a key={link.label} href={link.href} className={linkClass}>
                {link.label}
              </a>
            ))}
          </nav>
        </div>

        <div className="mt-8 pt-6 border-t border-border flex flex-col gap-4 text-xs text-text-muted">
          <p className="max-w-3xl text-sm leading-relaxed">
            Built on <a href="https://virtual-kubelet.io/" className={`${linkClass} underline underline-offset-4`}>Virtual Kubelet</a>, a{" "}
            <a href="https://www.cncf.io/projects/virtual-kubelet/" className={`${linkClass} underline underline-offset-4`}>Cloud Native Computing Foundation (CNCF) Sandbox project</a>.
            {" "}CNCF is part of the <a href="https://www.linuxfoundation.org/" className={`${linkClass} underline underline-offset-4`}>Linux Foundation</a>.
          </p>
          <div className="flex flex-col sm:flex-row sm:items-center sm:justify-between gap-3">
            <p>© {new Date().getFullYear()} Cisco Systems Inc.</p>
            <div className="flex flex-wrap gap-x-5 gap-y-3">
              <a href="/cisco-virtual-kubelet/LICENSE" className={linkClass}>Apache 2.0</a>
              <a href="/cisco-virtual-kubelet/NOTICE" className={linkClass}>Notices</a>
              <a href="/cisco-virtual-kubelet/THIRD_PARTY_LICENSES.txt" className={linkClass}>Third-party licenses</a>
            </div>
          </div>
        </div>
      </div>
    </footer>
  );
}
