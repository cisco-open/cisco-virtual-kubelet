// Copyright 2026 Cisco Systems Inc.
// SPDX-License-Identifier: Apache-2.0
import assert from "node:assert/strict";
import { statSync } from "node:fs";
import { createRequire } from "node:module";
import path from "node:path";
import test from "node:test";
import { fileURLToPath } from "node:url";
import { Linter } from "eslint";
import nextPlugin from "@next/eslint-plugin-next";

const require = createRequire(import.meta.url);
const pluginRequire = createRequire(require.resolve("@next/eslint-plugin-next/package.json"));
const { getRootDirs } = pluginRequire("./dist/utils/get-root-dirs.js");
const root = path.resolve(path.dirname(fileURLToPath(import.meta.url)), "..");
const context = (rootDir) => ({ cwd: root, settings: { next: { rootDir } } });
// The plugin consumes these as filesystem paths; directory results may have
// trailing separators, which do not affect the plugin's path joins.
const directoriesFor = (rootDir) => getRootDirs(context(rootDir)).map((directory) => path.resolve(root, directory)).sort();

test("Next lint uses the audited glob override", () => {
  assert.equal(pluginRequire("fast-glob/package.json").name, "@cvk/next-root-glob");
  assert.equal(pluginRequire("fast-glob/package.json").dependencies.tinyglobby, "0.2.15");
});

test("Next root directory discovery preserves supported glob behavior", () => {
  assert.deepEqual(getRootDirs(context(undefined)), [root]);
  assert.deepEqual(directoriesFor(root), [root]);
  assert.deepEqual(directoriesFor("."), [root]);
  const expected = [path.join(root, "src/app"), path.join(root, "src/components")].sort();
  assert.deepEqual(directoriesFor(`${root}/src/{app,components}`), expected);
  assert.deepEqual(directoriesFor("src/{app,components}"), expected);
  assert.deepEqual(directoriesFor([`${root}/src/app`, false, `${root}/src/components`]), expected);
  assert.deepEqual(getRootDirs(context(`${root}/src/does-not-exist-*`)), []);
  const directories = getRootDirs(context(`${root}/src/*`));
  assert.ok(directories.length > 0);
  assert.ok(directories.every((directory) => statSync(directory).isDirectory()));
  assert.throws(() => pluginRequire("fast-glob").globSync("*", { onlyFiles: true }), TypeError);
});

test("Next internal-link lint still finds pages through an absolute root glob", () => {
  const linter = new Linter();
  const config = {
    languageOptions: { parserOptions: { ecmaFeatures: { jsx: true } } },
    plugins: { next: nextPlugin },
    settings: { next: { rootDir: `${path.dirname(root)}/{website,does-not-exist}` } },
    rules: { "next/no-html-link-for-pages": "error" },
  };
  const messages = linter.verify('const link = <a href="/">Home</a>;', config);
  assert.equal(messages.length, 1);
  assert.equal(messages[0].ruleId, "next/no-html-link-for-pages");
  assert.deepEqual(linter.verify('const link = <a href="https://example.com/">External</a>;', config), []);
});
