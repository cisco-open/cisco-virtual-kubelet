#!/usr/bin/env python3
# Copyright 2026 Cisco Systems Inc.
#
# Licensed under the Apache License, Version 2.0 (the "License");
# you may not use this file except in compliance with the License.
# You may obtain a copy of the License at
#
#     http://www.apache.org/licenses/LICENSE-2.0
#
# Unless required by applicable law or agreed to in writing, software
# distributed under the License is distributed on an "AS IS" BASIS,
# WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
# See the License for the specific language governing permissions and
# limitations under the License.

"""Refresh the exact license inventory for the vendored Mermaid runtime.

The published Mermaid browser bundle contains dependency code built with
versions that can differ from the versions selected by a consumer's npm lock.
Its source map also embeds prebuilt @mermaid-js/parser chunks whose own source
maps expose another dependency layer. This maintainer-only command audits both
layers and downloads each exact npm tarball to capture its license text.

Normal CI is offline: generate_mkdocs_licenses.py validates these generated
artifacts against hash pins and the installed source maps after ``npm ci``.
"""

from __future__ import annotations

import argparse
import base64
import concurrent.futures
import hashlib
import io
import json
import pathlib
import re
import tarfile
import urllib.parse
import urllib.request


ROOT = pathlib.Path(__file__).resolve().parents[2]
WEBSITE = ROOT / "docs" / "website"
MERMAID_ROOT = WEBSITE / "node_modules" / "mermaid"
PARSER_ROOT = WEBSITE / "node_modules" / "@mermaid-js" / "parser"
MAIN_MAP = MERMAID_ROOT / "dist" / "mermaid.min.js.map"
THIRD_PARTY = ROOT / "third_party" / "mkdocs-material" / "9.7.7"
INVENTORY_PATH = THIRD_PARTY / "MERMAID_RUNTIME_INVENTORY.json"
LICENSE_PATH = THIRD_PARTY / "MERMAID_RUNTIME_LICENSES.txt"
LICENSE_NAME = re.compile(
    r"^(?:licen[cs]e|copying|notice|authors|copyright)(?:[._-].*)?$",
    re.IGNORECASE,
)
STORE_SOURCE = re.compile(
    r"node_modules/\.pnpm/([^/]+)/node_modules/((?:@[^/]+/)?[^/]+)"
)
ROUGHJS_BUNDLED_DEPENDENCIES = {
    ("hachure-fill", "0.5.2"),
    ("path-data-parser", "0.1.0"),
    ("points-on-curve", "0.2.0"),
    ("points-on-path", "0.2.1"),
}
# These prebuilt sources hide dependencies from the pnpm-path inventory. Pin
# their complete source, not just their notice, so changed code requires audit.
# The path-browserify source matches index.js in the official 1.0.1 npm tarball,
# but the parser map does not record its version; retain source attribution only.
PATH_BROWSERIFY_SOURCE = "webpack://LIB/node_modules/path-browserify/index.js"
PATH_BROWSERIFY_SHA256 = (
    "f6d60f9e7cd83f65f947cda34ef846e82706c044d9a9fa7c5b69718b13b78e28"
)
ELK_PAIR = ("elkjs", "0.9.3")
ELK_SOURCE = "../../../node_modules/.pnpm/elkjs@0.9.3/node_modules/elkjs/lib/elk.bundled.js"
ELK_BUNDLE_SHA256 = (
    "b0745abd7f23cd91690a1587e377edbe19fd7233c783300290936720546216d4"
)
ELK_GIT_HEAD = "a8304cf79fde75bc2ab1a89d28320f53f8637436"
ELK_SOURCE_NOTICE = f"""Source availability for elkjs 0.9.3 (EPL-2.0)

The npm package's gitHead and upstream 0.9.3 tag identify this source revision:
https://github.com/kieler/elkjs/tree/{ELK_GIT_HEAD}
Source archive: https://github.com/kieler/elkjs/archive/{ELK_GIT_HEAD}.tar.gz
This revision includes the JavaScript API, generated unminified worker, and
bundled distribution. Its lib/elk.bundled.js is byte-identical to the npm
tarball and the source embedded in Mermaid (SHA-256 {ELK_BUNDLE_SHA256}).

The upstream release describes its underlying Eclipse Layout Kernel source as
ELK 0.9.1 plus eclipse-elk/elk PR #955:
https://github.com/kieler/elkjs/releases/tag/0.9.3
ELK 0.9.1 source: https://github.com/eclipse-elk/elk/tree/62d5909f96fad541bc101ad52dabaece6b7eab7e
PR #955: https://github.com/eclipse-elk/elk/pull/955
Merged change: https://github.com/eclipse-elk/elk/commit/7ca51784e42a24201f29bc13e458728b6fc61cdc
The elkjs build reads a sibling ELK checkout; its exact Java build revision is
not recorded. These links preserve the published source provenance without
claiming a reproducible Java build revision.

The embedded web-worker browser shim carries a separate Apache-2.0 notice,
reproduced below. Its exact package version is not recorded in this bundle."""


class MermaidAuditError(RuntimeError):
    """Raised when the published runtime cannot be audited exactly."""


def sha256(content: bytes) -> str:
    return hashlib.sha256(content).hexdigest()


def pinned_source(source_map: dict, source: str, digest: str) -> str | None:
    sources = source_map.get("sources")
    contents = source_map.get("sourcesContent")
    if not isinstance(sources, list) or not isinstance(contents, list):
        raise MermaidAuditError("embedded source map lacks sources/sourcesContent")
    if len(sources) != len(contents):
        raise MermaidAuditError("embedded source and sourcesContent counts differ")
    matches = [index for index, name in enumerate(sources) if name == source]
    if not matches:
        return None
    if len(matches) != 1:
        raise MermaidAuditError(f"duplicate embedded source: {source}")
    content = contents[matches[0]]
    if not isinstance(content, str) or sha256(content.encode("utf-8")) != digest:
        raise MermaidAuditError(f"audited embedded source changed: {source}")
    return content


def path_browserify_notice(source_map: dict) -> str | None:
    source = pinned_source(source_map, PATH_BROWSERIFY_SOURCE, PATH_BROWSERIFY_SHA256)
    if source is None:
        return None
    header, separator, _ = source.partition("\n\n'use strict';")
    if (
        not separator
        or "Copyright Joyent, Inc. and other Node contributors." not in header
        or not header.endswith("// USE OR OTHER DEALINGS IN THE SOFTWARE.")
    ):
        raise MermaidAuditError("cannot extract embedded path-browserify notice")
    return f"Source: {PATH_BROWSERIFY_SOURCE}\n\n{header}"


def web_worker_notice(bundle: bytes) -> str:
    if sha256(bundle) != ELK_BUNDLE_SHA256:
        raise MermaidAuditError("audited elkjs bundle changed")
    match = re.search(
        r",4:\[function\(require,module,exports\)\{\n(/\*\*.*?\*/)\n"
        r"module\.exports = Worker;\n\},\{\}\]\},\{\},\[3\]\)\(3\)\n\}\);\s*\Z",
        bundle.decode("utf-8"),
        re.DOTALL,
    )
    if match is None or "Copyright 2020 Google LLC" not in match.group(1):
        raise MermaidAuditError("cannot extract embedded web-worker notice")
    return "web-worker browser shim embedded in elkjs 0.9.3\n\n" + match.group(1)


def package_pairs(source_map: dict) -> set[tuple[str, str]]:
    pairs: set[tuple[str, str]] = set()
    sources = source_map.get("sources")
    if not isinstance(sources, list) or not all(
        isinstance(item, str) for item in sources
    ):
        raise MermaidAuditError("source map does not contain a string sources array")
    sources_content = source_map.get("sourcesContent", [])
    if not isinstance(sources_content, list) or not all(
        item is None or isinstance(item, str) for item in sources_content
    ):
        raise MermaidAuditError("source map has an invalid sourcesContent array")
    # Prebuilt inputs can retain their own pnpm source-map paths inside
    # sourcesContent even when the outer bundler represents the entire input as
    # one source. Audit both layers so nested Langium/Chevrotain/vscode code is
    # never mistaken for first-party Mermaid code.
    for source in [*sources, *(item for item in sources_content if item is not None)]:
        for match in STORE_SOURCE.finditer(source):
            store_name, package_name = match.groups()
            encoded_name = package_name.replace("/", "+")
            prefix = f"{encoded_name}@"
            if not store_name.startswith(prefix):
                raise MermaidAuditError(
                    f"pnpm store identity does not match source package: {source[:200]}"
                )
            version = store_name.removeprefix(prefix).split("_", 1)[0]
            pairs.add((package_name, version))
    return pairs


def runtime_inventory() -> tuple[set[tuple[str, str]], dict[str, object]]:
    main_bytes = MAIN_MAP.read_bytes()
    main = json.loads(main_bytes)
    main_sources = main["sources"]
    main_content = main["sourcesContent"]
    if len(main_sources) != len(main_content):
        raise MermaidAuditError("Mermaid source and sourcesContent counts differ")

    parser_maps: list[tuple[str, bytes]] = []
    embedded_parser_chunks = 0
    pairs = package_pairs(main)
    elk_source = pinned_source(main, ELK_SOURCE, ELK_BUNDLE_SHA256)
    if ELK_PAIR not in pairs or elk_source is None:
        raise MermaidAuditError("audited Mermaid map no longer embeds elkjs 0.9.3")
    web_worker_notice(elk_source.encode("utf-8"))
    embedded_path_notice = False
    for source, expected_content in zip(main_sources, main_content, strict=True):
        if not source.startswith("../../parser/dist/") or not source.endswith(".mjs"):
            continue
        embedded_parser_chunks += 1
        relative = pathlib.PurePosixPath(source.removeprefix("../../parser/"))
        chunk = PARSER_ROOT.joinpath(*relative.parts)
        if chunk.read_text(encoding="utf-8") != expected_content:
            raise MermaidAuditError(
                f"installed parser chunk differs from Mermaid sourcesContent: {source}"
            )
        source_map_path = chunk.with_name(f"{chunk.name}.map")
        source_map_bytes = source_map_path.read_bytes()
        parser_maps.append((relative.as_posix() + ".map", source_map_bytes))
        parser_map = json.loads(source_map_bytes)
        pairs.update(package_pairs(parser_map))
        if path_browserify_notice(parser_map) is not None:
            embedded_path_notice = True

    if embedded_parser_chunks == 0:
        raise MermaidAuditError("Mermaid source map contains no embedded parser chunks")
    if not embedded_path_notice:
        raise MermaidAuditError("audited parser maps lack embedded path-browserify")
    parser_hash = hashlib.sha256()
    for relative, content in sorted(parser_maps):
        parser_hash.update(relative.encode("utf-8"))
        parser_hash.update(b"\0")
        parser_hash.update(content)
        parser_hash.update(b"\0")

    mermaid_manifest = json.loads((MERMAID_ROOT / "package.json").read_text())
    parser_manifest = json.loads((PARSER_ROOT / "package.json").read_text())
    resize_manifest = json.loads(
        (
            WEBSITE / "node_modules" / "resize-observer-polyfill" / "package.json"
        ).read_text()
    )
    if ("roughjs", "4.6.6") not in pairs:
        raise MermaidAuditError("audited Mermaid map no longer embeds roughjs 4.6.6")
    # roughjs/bundled/rough.esm.js contains these four packages but publishes no
    # nested source map. Their exact versions and distinct MIT notices were
    # audited against the prebuilt roughjs 4.6.6 input embedded by Mermaid.
    pairs.update(ROUGHJS_BUNDLED_DEPENDENCIES)
    pairs.update(
        {
            (mermaid_manifest["name"], mermaid_manifest["version"]),
            (parser_manifest["name"], parser_manifest["version"]),
            (resize_manifest["name"], resize_manifest["version"]),
        }
    )
    audit = {
        "mermaidSourceMapSha256": sha256(main_bytes),
        "embeddedParserChunkCount": embedded_parser_chunks,
        "embeddedParserMapsSha256": parser_hash.hexdigest(),
    }
    return pairs, audit


def decode_license(content: bytes, source: str) -> str:
    try:
        text = (
            content.decode("utf-8-sig")
            .replace("\r\n", "\n")
            .replace("\r", "\n")
            .strip()
        )
    except UnicodeDecodeError as exc:
        raise MermaidAuditError(f"license is not UTF-8: {source}") from exc
    if not text:
        raise MermaidAuditError(f"license is empty: {source}")
    return text


def readme_license(archive: tarfile.TarFile) -> tuple[str, str] | None:
    candidates = [
        member
        for member in archive.getmembers()
        if member.isfile()
        and pathlib.PurePosixPath(member.name).name.lower()
        in {"readme", "readme.md", "readme.txt"}
    ]
    for member in sorted(candidates, key=lambda item: item.name.count("/")):
        extracted = archive.extractfile(member)
        if extracted is None:
            continue
        text = decode_license(extracted.read(), member.name)
        match = re.search(r"(?ims)^#{1,6}\s+licen[cs]e\s*$.*?(?=^#{1,6}\s+|\Z)", text)
        if match is not None:
            return member.name, match.group(0).strip()
    return None


def normalize_license(value: object) -> str | None:
    if isinstance(value, str) and value.strip():
        return value.strip()
    if isinstance(value, dict) and isinstance(value.get("type"), str):
        return value["type"].strip()
    return None


def archive_bytes(archive: tarfile.TarFile, path: str) -> bytes:
    members = [member for member in archive.getmembers() if member.name == path]
    if len(members) != 1 or not members[0].isfile():
        raise MermaidAuditError(f"npm tarball must contain one regular file: {path}")
    extracted = archive.extractfile(members[0])
    if extracted is None:
        raise MermaidAuditError(f"cannot read npm tarball file: {path}")
    return extracted.read()


def embedded_notices(
    pair: tuple[str, str], archive: tarfile.TarFile, metadata: dict
) -> list[tuple[str, str]]:
    if pair == ELK_PAIR:
        if metadata.get("gitHead") != ELK_GIT_HEAD:
            raise MermaidAuditError("elkjs source provenance changed")
        bundle = archive_bytes(archive, "package/lib/elk.bundled.js")
        return [
            ("package/lib/elk.bundled.js (source availability)", ELK_SOURCE_NOTICE),
            ("package/lib/elk.bundled.js (web-worker notice)", web_worker_notice(bundle)),
        ]
    if pair[0] == "@mermaid-js/parser":
        notices = set()
        for member in archive.getmembers():
            if member.isfile() and member.name.endswith(".mjs.map"):
                source_map = json.loads(archive_bytes(archive, member.name))
                notice = path_browserify_notice(source_map)
                if notice is not None:
                    notices.add(notice)
        if len(notices) != 1:
            raise MermaidAuditError("parser tarball lacks one audited path-browserify notice")
        return [("path-browserify/index.js (embedded source notice)", notices.pop())]
    return []


def fetch_package(
    pair: tuple[str, str]
) -> tuple[dict[str, object], list[tuple[str, str]]]:
    name, version = pair
    escaped_name = urllib.parse.quote(name, safe="@")
    url = f"https://registry.npmjs.org/{escaped_name}/{urllib.parse.quote(version, safe='')}"
    request = urllib.request.Request(
        url, headers={"User-Agent": "cisco-vk-license-audit"}
    )
    with urllib.request.urlopen(request, timeout=60) as response:
        metadata = json.load(response)
    if metadata.get("name") != name or metadata.get("version") != version:
        raise MermaidAuditError(f"npm registry identity mismatch for {name}@{version}")
    dist = metadata.get("dist")
    if not isinstance(dist, dict):
        raise MermaidAuditError(
            f"npm registry has no dist metadata for {name}@{version}"
        )
    integrity = dist.get("integrity")
    tarball_url = dist.get("tarball")
    if not isinstance(integrity, str) or not integrity.startswith("sha512-"):
        raise MermaidAuditError(
            f"npm package lacks SHA-512 integrity: {name}@{version}"
        )
    if not isinstance(tarball_url, str) or not tarball_url.startswith("https://"):
        raise MermaidAuditError(f"npm package lacks HTTPS tarball: {name}@{version}")
    with urllib.request.urlopen(tarball_url, timeout=120) as response:
        tarball = response.read()
    try:
        expected = base64.b64decode(integrity.removeprefix("sha512-"), validate=True)
    except ValueError as exc:
        raise MermaidAuditError(f"invalid npm SHA-512 integrity: {name}@{version}") from exc
    if hashlib.sha512(tarball).digest() != expected:
        raise MermaidAuditError(f"npm tarball integrity mismatch: {name}@{version}")

    texts: list[tuple[str, str]] = []
    package_manifest: dict[str, object] = {}
    with tarfile.open(fileobj=io.BytesIO(tarball), mode="r:gz") as archive:
        package_manifest = json.loads(archive_bytes(archive, "package/package.json"))
        if package_manifest.get("name") != name or package_manifest.get("version") != version:
            raise MermaidAuditError(
                f"npm tarball identity mismatch for {name}@{version}"
            )
        for member in archive.getmembers():
            if (
                not member.isfile()
                or LICENSE_NAME.fullmatch(pathlib.PurePosixPath(member.name).name)
                is None
            ):
                continue
            extracted = archive.extractfile(member)
            if extracted is not None:
                texts.append(
                    (member.name, decode_license(extracted.read(), member.name))
                )
        if not texts:
            fallback = readme_license(archive)
            if fallback is not None:
                texts.append(fallback)
        if not texts:
            raise MermaidAuditError(
                f"npm tarball contains no license text: {name}@{version}"
            )
        texts.extend(embedded_notices(pair, archive, metadata))

    license_label = normalize_license(metadata.get("license")) or normalize_license(
        package_manifest.get("license")
    )
    entry: dict[str, object] = {
        "name": name,
        "version": version,
        "license": license_label or "See bundled license text",
        "integrity": integrity,
        "tarball": tarball_url,
        "licenseFiles": [
            {"path": path, "sha256": sha256(text.encode("utf-8"))}
            for path, text in sorted(texts)
        ],
    }
    return entry, sorted(texts)


def render_licenses(
    packages: list[tuple[dict[str, object], list[tuple[str, str]]]],
) -> str:
    versions = {}
    for name in ("mermaid", "@mermaid-js/parser"):
        matches = [entry["version"] for entry, _ in packages if entry["name"] == name]
        if len(matches) != 1:
            raise MermaidAuditError(f"notice requires one exact {name} package")
        versions[name] = matches[0]
    sections = [
        "MkDocs vendored browser-runtime licenses",
        "",
        "Generated from the exact package/version identities embedded in the",
        f"hash-pinned Mermaid {versions['mermaid']} source map, recursively including its",
        f"prebuilt @mermaid-js/parser {versions['@mermaid-js/parser']} chunks, plus the exact top-level",
        "Mermaid, parser, and ResizeObserver runtime packages.",
    ]
    for entry, texts in packages:
        sections.extend(
            [
                "",
                "=" * 80,
                f"Package: {entry['name']} {entry['version']}",
                f"License: {entry['license']}",
                f"npm integrity: {entry['integrity']}",
            ]
        )
        for path, text in texts:
            sections.extend(["", f"--- {path} ---", "", text])
    return "\n".join(sections).rstrip() + "\n"


def main() -> int:
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument(
        "--refresh",
        action="store_true",
        help="download exact npm tarballs and replace the generated artifacts",
    )
    args = parser.parse_args()
    if not args.refresh:
        parser.error("this maintainer command requires --refresh")

    pairs, audit = runtime_inventory()
    with concurrent.futures.ThreadPoolExecutor(max_workers=8) as executor:
        fetched = list(executor.map(fetch_package, sorted(pairs)))
    fetched.sort(key=lambda item: (item[0]["name"], item[0]["version"]))
    inventory = {
        "schemaVersion": 1,
        **audit,
        "packages": [entry for entry, _ in fetched],
    }
    THIRD_PARTY.mkdir(parents=True, exist_ok=True)
    INVENTORY_PATH.write_text(
        json.dumps(inventory, indent=2, ensure_ascii=False) + "\n",
        encoding="utf-8",
        newline="\n",
    )
    LICENSE_PATH.write_text(render_licenses(fetched), encoding="utf-8", newline="\n")
    print(f"wrote {INVENTORY_PATH} for {len(fetched)} exact runtime packages")
    print(f"wrote {LICENSE_PATH}")
    return 0


if __name__ == "__main__":
    raise SystemExit(main())
