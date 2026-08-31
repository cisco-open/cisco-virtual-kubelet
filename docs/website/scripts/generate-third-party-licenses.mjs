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

import fs from "node:fs";
import { createHash } from "node:crypto";
import path from "node:path";
import { fileURLToPath } from "node:url";

const scriptDir = path.dirname(fileURLToPath(import.meta.url));
const websiteDir = path.resolve(scriptDir, "..");
const repoDir = path.resolve(websiteDir, "../..");
const publicDir = path.join(websiteDir, "public");
const mode = process.argv[2];

if (!["--write", "--check", "--check-build"].includes(mode)) {
  throw new Error("usage: generate-third-party-licenses.mjs --write|--check|--check-build");
}

const read = (file) => fs.readFileSync(file, "utf8").replace(/\r\n/g, "\n").trimEnd() + "\n";
const sha256 = (value) => createHash("sha256").update(value).digest("hex");
const verifyFileHash = (relativePath, expected) => {
  const file = path.join(websiteDir, relativePath);
  if (!fs.existsSync(file)) {
    throw new Error(`audited vendored file is not installed: ${relativePath}`);
  }
  const actual = sha256(fs.readFileSync(file));
  if (actual !== expected) {
    throw new Error(`audited vendored file changed: ${relativePath} (${actual}); review its license and update the pinned hash`);
  }
};
const visitFiles = (directory) => fs.readdirSync(directory, { withFileTypes: true }).flatMap((item) => {
  const child = path.join(directory, item.name);
  return item.isDirectory() ? visitFiles(child) : [child];
});
const verifyDirectoryHash = (relativePath, expected) => {
  const directory = path.join(websiteDir, relativePath);
  if (!fs.existsSync(directory)) {
    throw new Error(`audited vendored directory is not installed: ${relativePath}`);
  }
  const hash = createHash("sha256");
  const files = visitFiles(directory)
    .map((file) => ({ file, relative: path.relative(directory, file).split(path.sep).join("/") }))
    .sort((a, b) => (a.relative < b.relative ? -1 : a.relative > b.relative ? 1 : 0));
  for (const { file, relative } of files) {
    hash.update(relative);
    hash.update("\0");
    hash.update(fs.readFileSync(file));
    hash.update("\0");
  }
  const actual = hash.digest("hex");
  if (actual !== expected) {
    throw new Error(`audited vendored directory changed: ${relativePath} (${actual}); review its code and license and update the pinned hash`);
  }
};

// Next.js ships some browser code inside its own package rather than as
// package-lock production nodes. Pin both the redistributed implementation
// and its license text so a framework update cannot silently change this
// audited set. The post-build check below proves which members reached out/.
const vendoredFiles = {
  coreJsSource: {
    path: "node_modules/next/dist/build/polyfills/polyfill-nomodule.js",
    sha256: "0973c1d64c88adc8e3c950410cb58b288f72118d5965b78049438deb8f2f9683",
  },
  coreJsLicense: {
    path: "licenses/CORE_JS_3.38.1_LICENSE.txt",
    sha256: "c725d3ba76f7d1556952981b8ea5354f570f7c02c50e47737c666164542cae65",
  },
  processSource: {
    path: "node_modules/next/dist/compiled/process/browser.js",
    sha256: "6521ba86acfd0874df2a9ccd66310029dee09c208da07dd44d293e31a2ce8d46",
  },
  processLicense: {
    path: "node_modules/next/dist/compiled/process/LICENSE",
    sha256: "59a400d04c5078579acc27ddd6452c1bdf763f9506e01364700935fbb1a7c91b",
  },
  tailwindManifest: {
    path: "node_modules/tailwindcss/package.json",
    sha256: "6c7bc6dd0cae428b97d94140f29da2575c1f69298b2fa2e9603d031d61864e86",
  },
  tailwindLicense: {
    path: "node_modules/tailwindcss/LICENSE",
    sha256: "60e0b68c0f35c078eef3a5d29419d0b03ff84ec1df9c3f9d6e39a519a5ae7985",
  },
  tailwindPreflight: {
    path: "node_modules/tailwindcss/preflight.css",
    sha256: "fa2d5ae43ae561061b7ce348b89636dbdc6cd71ab5992d4e1cdd046d0b4f28f9",
  },
};
const vendoredDirectories = {
  react: {
    path: "node_modules/next/dist/compiled/react",
    sha256: "1f3c74cfbebf89f3a65c0e45ce5dcb5cab6ed6ec6780de4a151c5030d45d35cf",
  },
  reactDom: {
    path: "node_modules/next/dist/compiled/react-dom",
    sha256: "7bd875c2c3d85503047a83675b8fb06469ab06714ba9f4288f768063fc75e8a0",
  },
  reactServerDomTurbopack: {
    path: "node_modules/next/dist/compiled/react-server-dom-turbopack",
    sha256: "4237c7369061a463932671a52c76b1d6f575d5cb40bbed58eb5b0ebb1295f0b8",
  },
  scheduler: {
    path: "node_modules/next/dist/compiled/scheduler",
    sha256: "214c5392b42b19ca5b41faa360b4adcce4e012fee65abdc913c25237de6f1c05",
  },
};

for (const audited of Object.values(vendoredFiles)) {
  verifyFileHash(audited.path, audited.sha256);
}
for (const audited of Object.values(vendoredDirectories)) {
  verifyDirectoryHash(audited.path, audited.sha256);
}

const reactCanaryVersion = "19.3.0-canary-3f0b9e61-20260317";
const schedulerCanaryVersion = "0.28.0-canary-3f0b9e61-20260317";
const reactDomManifest = JSON.parse(read(path.join(websiteDir, vendoredDirectories.reactDom.path, "package.json")));
const reactServerDomManifest = JSON.parse(read(path.join(websiteDir, vendoredDirectories.reactServerDomTurbopack.path, "package.json")));
if (
  reactDomManifest.peerDependencies?.react !== reactCanaryVersion ||
  reactDomManifest.dependencies?.scheduler !== schedulerCanaryVersion ||
  reactServerDomManifest.peerDependencies?.react !== reactCanaryVersion ||
  reactServerDomManifest.peerDependencies?.["react-dom"] !== reactCanaryVersion
) {
  throw new Error("Next.js vendored React canary family versions changed; audit and update the pinned inventory");
}

const sharedReactLicense = fs.readFileSync(
  path.join(websiteDir, vendoredDirectories.react.path, "LICENSE"),
);
for (const audited of Object.values(vendoredDirectories)) {
  if (!fs.readFileSync(path.join(websiteDir, audited.path, "LICENSE")).equals(sharedReactLicense)) {
    throw new Error(`Next.js vendored React family license differs: ${audited.path}/LICENSE`);
  }
}

const tailwindManifest = JSON.parse(read(path.join(websiteDir, vendoredFiles.tailwindManifest.path)));
if (
  tailwindManifest.name !== "tailwindcss" ||
  tailwindManifest.version !== "4.1.18" ||
  tailwindManifest.license !== "MIT"
) {
  throw new Error("Tailwind CSS package identity changed; audit its generated output and license");
}

const lock = JSON.parse(read(path.join(websiteDir, "package-lock.json")));
const entries = [];
for (const [packagePath, metadata] of Object.entries(lock.packages)) {
  if (
    !packagePath.startsWith("node_modules/") ||
    metadata.dev === true ||
    metadata.devOptional === true ||
    metadata.optional === true
  ) {
    continue;
  }

  const directory = path.join(websiteDir, packagePath);
  const manifestPath = path.join(directory, "package.json");
  if (!fs.existsSync(manifestPath)) {
    throw new Error(`locked production package is not installed: ${packagePath}`);
  }

  const manifest = JSON.parse(read(manifestPath));
  const files = fs.readdirSync(directory)
    .filter((name) => /^(licen[cs]e|copying|notice)(\..*)?$/i.test(name))
    .sort((a, b) => a.localeCompare(b));
  const texts = files.map((name) => ({ name, text: read(path.join(directory, name)) }));

  // These tiny marker/subpackages do not publish a standalone license file.
  // Their upstream composite license is present through next/react below, and
  // the declared SPDX identifier and author remain visible here.
  if (texts.length === 0 && !["@next/env", "client-only"].includes(manifest.name)) {
    throw new Error(`production package has no shipped license/notice text: ${manifest.name}`);
  }

  entries.push({
    name: manifest.name,
    version: manifest.version,
    license: manifest.license ?? metadata.license ?? "UNKNOWN",
    author: typeof manifest.author === "string" ? manifest.author : manifest.author?.name,
    homepage: manifest.homepage ?? manifest.repository?.url,
    packagePath,
    texts,
  });
}

entries.sort((a, b) =>
  `${a.name}@${a.version}:${a.packagePath}`.localeCompare(`${b.name}@${b.version}:${b.packagePath}`, "en"),
);

const nextEntry = entries.find((entry) => entry.name === "next");
if (!nextEntry) {
  throw new Error("next is missing from the locked production package set");
}

const vendoredEntries = [
  {
    name: "core-js",
    version: "3.38.1",
    license: "MIT",
    author: "Denis Pushkarev",
    homepage: "https://github.com/zloirock/core-js",
    context: `Browser polyfill vendored by next@${nextEntry.version}`,
    licenseFile: vendoredFiles.coreJsLicense.path,
  },
  {
    name: "process browser shim",
    version: `vendored by next@${nextEntry.version}; upstream version omitted by Next.js`,
    license: "MIT",
    author: "Roman Shtylman",
    homepage: "https://github.com/defunctzombie/node-process",
    context: "Browser process shim emitted from next/dist/compiled/process",
    licenseFile: vendoredFiles.processLicense.path,
  },
  {
    name: "Next.js vendored React canary family",
    version: `${reactCanaryVersion}; scheduler ${schedulerCanaryVersion}`,
    license: "MIT",
    author: "Meta Platforms, Inc. and affiliates",
    homepage: "https://react.dev/",
    context: `react, react-dom, react-server-dom-turbopack, and scheduler vendored by next@${nextEntry.version}`,
    licenseFile: `${vendoredDirectories.react.path}/LICENSE`,
  },
  {
    name: "tailwindcss",
    version: tailwindManifest.version,
    license: tailwindManifest.license,
    author: "Tailwind Labs, Inc.",
    homepage: "https://tailwindcss.com/",
    context: "Build-time CSS generator whose preflight and utility rules are redistributed in the static site",
    licenseFile: vendoredFiles.tailwindLicense.path,
  },
];

const divider = "=".repeat(80);
const thirdParty = [
  "Cisco Virtual Kubelet website — third-party notices and licenses",
  "",
  "Generated deterministically from package-lock.json production packages by",
  "scripts/generate-third-party-licenses.mjs, plus a hash-pinned inventory of",
  "framework-vendored and generated browser code. Do not edit by hand.",
  "Key deployed frameworks: Next.js, React, React DOM, Framer Motion, and Lucide React.",
  "",
  divider,
  "Geist and Geist Mono fonts",
  divider,
  read(path.join(websiteDir, "licenses/GEIST-OFL.txt")).trimEnd(),
  ...vendoredEntries.flatMap((entry) => [
    "",
    divider,
    `${entry.name}@${entry.version}`,
    divider,
    `License: ${entry.license}`,
    `Author: ${entry.author}`,
    `Project: ${entry.homepage}`,
    `Redistribution context: ${entry.context}`,
    "",
    `--- ${path.basename(entry.licenseFile)} ---`,
    read(path.join(websiteDir, entry.licenseFile)).trimEnd(),
  ]),
  ...entries.flatMap((entry) => [
    "",
    divider,
    `${entry.name}@${entry.version}`,
    divider,
    `License: ${entry.license}`,
    ...(entry.author ? [`Author: ${entry.author}`] : []),
    ...(entry.homepage ? [`Project: ${entry.homepage}`] : []),
    ...(entry.texts.length === 0
      ? ["License text: supplied by the upstream Next.js/React composite license entry below."]
      : entry.texts.flatMap(({ name, text }) => ["", `--- ${name} ---`, text.trimEnd()])),
  ]),
  "",
].join("\n");

const outputs = new Map([
  ["LICENSE", read(path.join(repoDir, "LICENSE"))],
  ["NOTICE", read(path.join(websiteDir, "NOTICE"))],
  ["THIRD_PARTY_LICENSES.txt", thirdParty],
]);

fs.mkdirSync(publicDir, { recursive: true });
for (const [name, expected] of outputs) {
  const target = path.join(publicDir, name);
  if (mode === "--write") {
    fs.writeFileSync(target, expected);
    continue;
  }
  if (!fs.existsSync(target) || fs.readFileSync(target, "utf8") !== expected) {
    throw new Error(`${path.relative(websiteDir, target)} is stale; run npm run licenses:generate`);
  }

  if (mode === "--check-build") {
    const deployed = path.join(websiteDir, "out", name);
    if (!fs.existsSync(deployed) || fs.readFileSync(deployed, "utf8") !== expected) {
      throw new Error(`out/${name} does not contain the verified license artifact`);
    }
  }
}

if (mode === "--check-build") {
  const chunksDir = path.join(websiteDir, "out", "_next", "static", "chunks");
  if (!fs.existsSync(chunksDir)) {
    throw new Error("built Next.js chunks are missing; run npm run build first");
  }

  const chunks = visitFiles(chunksDir)
    .filter((file) => file.endsWith(".js"))
    .map((file) => ({ file, bytes: fs.readFileSync(file) }));

  const styles = visitFiles(path.join(websiteDir, "out", "_next", "static"))
    .filter((file) => file.endsWith(".css"))
    .map((file) => ({ file, bytes: fs.readFileSync(file) }));
  const tailwindPreflightMarker = Buffer.from(
    '@layer base{*,:after,:before,::backdrop{box-sizing:border-box;border:0 solid;margin:0;padding:0}::file-selector-button{box-sizing:border-box;border:0 solid;margin:0;padding:0}',
  );
  const tailwindStyles = styles.filter(({ bytes }) => bytes.includes(tailwindPreflightMarker));
  if (tailwindStyles.length !== 1) {
    throw new Error(`expected Tailwind CSS 4.1.18 preflight output in exactly one stylesheet, found ${tailwindStyles.length}`);
  }

  const coreJsMarker = Buffer.from('version:"3.38.1",mode:"global",copyright:"© 2014-2024 Denis Pushkarev');
  const coreJsChunks = chunks.filter(({ bytes }) => bytes.includes(coreJsMarker));
  if (coreJsChunks.length !== 1) {
    throw new Error(`expected exactly one core-js 3.38.1 output chunk, found ${coreJsChunks.length}`);
  }
  if (sha256(coreJsChunks[0].bytes) !== vendoredFiles.coreJsSource.sha256) {
    throw new Error("emitted core-js chunk differs from the audited Next.js polyfill source");
  }

  const compiledPattern = /\/next\/dist\/compiled\/([^/"']+)\//g;
  const compiledPackages = new Set();
  for (const { bytes } of chunks) {
    for (const match of bytes.toString("utf8").matchAll(compiledPattern)) {
      compiledPackages.add(match[1]);
    }
  }
  const allowedCompiledPackages = new Set(["process"]);
  const unexpected = [...compiledPackages].filter((name) => !allowedCompiledPackages.has(name));
  const missing = [...allowedCompiledPackages].filter((name) => !compiledPackages.has(name));
  if (unexpected.length > 0 || missing.length > 0) {
    throw new Error(`Next.js compiled output inventory changed (unexpected: ${unexpected.join(", ") || "none"}; missing: ${missing.join(", ") || "none"})`);
  }

  const reactCanaryMarker = Buffer.from(reactCanaryVersion);
  const reactCanaryChunks = chunks.filter(({ bytes }) => bytes.includes(reactCanaryMarker));
  if (reactCanaryChunks.length !== 3) {
    throw new Error(`expected the audited React canary version in exactly three output chunks, found ${reactCanaryChunks.length}`);
  }
  const emittedCanaryVersions = new Set();
  const canaryPattern = /19\.3\.0-canary-[A-Za-z0-9.-]+/g;
  for (const { bytes } of chunks) {
    for (const match of bytes.toString("utf8").matchAll(canaryPattern)) {
      emittedCanaryVersions.add(match[0]);
    }
  }
  if (emittedCanaryVersions.size !== 1 || !emittedCanaryVersions.has(reactCanaryVersion)) {
    throw new Error(`emitted React canary version inventory changed: ${[...emittedCanaryVersions].join(", ") || "none"}`);
  }
}

console.log(`${mode === "--write" ? "generated" : "verified"} ${outputs.size} website license artifacts from ${entries.length} production packages and ${vendoredEntries.length} audited vendored entries`);
