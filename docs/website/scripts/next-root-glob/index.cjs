// Copyright 2026 Cisco Systems Inc.
// SPDX-License-Identifier: Apache-2.0
/* eslint-disable @typescript-eslint/no-require-imports -- Next loads this CommonJS adapter with require. */
const path = require("node:path");
const { globSync } = require("tinyglobby");

// Narrow adapter for Next's getRootDirs, not a general fast-glob substitute.
exports.globSync = (pattern, options) => {
  if (typeof pattern !== "string" || options?.onlyDirectories !== true) {
    throw new TypeError("Next root glob adapter requires a directory pattern");
  }
  const absolute = path.isAbsolute(pattern);
  return globSync(path.resolve(pattern), {
    ...options,
    expandDirectories: false,
    // Avoid treating a match of the process cwd as an empty pattern.
    cwd: path.parse(process.cwd()).root,
    absolute: true,
  }).map((directory) => absolute ? directory : path.relative(process.cwd(), directory) || ".");
};
