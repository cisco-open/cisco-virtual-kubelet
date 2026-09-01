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

"""Generate the license bundle redistributed with the MkDocs static site."""

from __future__ import annotations

import argparse
import hashlib
import html.parser
import importlib.metadata
import json
import pathlib
import re
import sys

from generate_mermaid_runtime_licenses import (
    MermaidAuditError,
    runtime_inventory as installed_mermaid_runtime_inventory,
)


ROOT = pathlib.Path(__file__).resolve().parents[2]
LOCK_PATH = ROOT / "requirements.txt"
OUTPUT_PATH = ROOT / "docs" / "MKDOCS_THIRD_PARTY_LICENSES.txt"
ASSET_NOTICE_PATH = (
    ROOT / "third_party" / "mkdocs-material" / "9.7.7" / "NPM_ASSET_LICENSES.txt"
)
ASSET_NOTICE_SHA256 = "72967cab229252310be8f9e084fab3c2bc49b4e685784f46bcfaf177d5863fb8"
RUNTIME_ASSET_MANIFEST_PATH = (
    ROOT / "third_party" / "mkdocs-material" / "9.7.7" / "RUNTIME_ASSETS.json"
)
NPM_LOCK_PATH = ROOT / "docs" / "website" / "package-lock.json"
EXPECTED_SOURCE_MAP_PACKAGES = {
    "clipboard",
    "escape-html",
    "focus-visible",
    "lunr",
    "material-design-color",
    "rxjs",
    "tslib",
}
EXPECTED_ASSET_NOTICE_PACKAGES = EXPECTED_SOURCE_MAP_PACKAGES | {
    # Compiled inside clipboard's distributable bundle rather than exposed as
    # separate source-map paths.
    "good-listener",
    "select",
    "tiny-emitter",
}
REQUIREMENT = re.compile(r"^([A-Za-z0-9][A-Za-z0-9._-]*)==([^\s\\]+)")
LICENSE_FILE = re.compile(
    r"^(?:licen[cs]e|copying|notice|authors|copyright)(?:[._-].*)?$",
    re.IGNORECASE,
)


class LicenseBundleError(RuntimeError):
    """Raised when the locked environment cannot produce a complete bundle."""


def canonical_name(name: str) -> str:
    return re.sub(r"[-_.]+", "-", name).lower()


def locked_requirements(path: pathlib.Path) -> dict[str, tuple[str, str]]:
    locked: dict[str, tuple[str, str]] = {}
    for line in path.read_text(encoding="utf-8").splitlines():
        match = REQUIREMENT.match(line)
        if match is None:
            continue
        name, version = match.groups()
        canonical = canonical_name(name)
        if canonical in locked:
            raise LicenseBundleError(f"duplicate locked distribution: {name}")
        locked[canonical] = (name, version)
    if not locked:
        raise LicenseBundleError(f"no locked distributions found in {path}")
    return locked


def distribution_license_files(
    distribution: importlib.metadata.Distribution,
) -> list[tuple[str, str]]:
    license_files: list[tuple[str, str]] = []
    for entry in distribution.files or ():
        relative = pathlib.PurePosixPath(str(entry))
        if not any(part.lower().endswith(".dist-info") for part in relative.parts):
            continue
        if LICENSE_FILE.fullmatch(relative.name) is None:
            continue
        resolved = pathlib.Path(distribution.locate_file(entry)).resolve()
        if not resolved.is_file():
            raise LicenseBundleError(f"declared license file is missing: {resolved}")
        content = resolved.read_text(encoding="utf-8-sig").strip()
        if not content:
            raise LicenseBundleError(f"license file is empty: {resolved}")
        license_files.append((relative.as_posix(), content))
    if not license_files:
        name = distribution.metadata.get("Name", "unknown")
        raise LicenseBundleError(f"no license text found for {name}")
    return sorted(license_files)


def material_icon_license_files(
    distribution: importlib.metadata.Distribution,
) -> list[tuple[str, str]]:
    license_files: list[tuple[str, str]] = []
    prefix = pathlib.PurePosixPath("material/templates/.icons")
    allowed_names = {"license", "license.md", "license.txt"}
    for entry in distribution.files or ():
        relative = pathlib.PurePosixPath(str(entry))
        if relative.name.lower() not in allowed_names:
            continue
        try:
            relative.relative_to(prefix)
        except ValueError:
            continue
        resolved = pathlib.Path(distribution.locate_file(entry)).resolve()
        if not resolved.is_file():
            raise LicenseBundleError(f"icon license file is missing: {resolved}")
        content = resolved.read_text(encoding="utf-8-sig").strip()
        if not content:
            raise LicenseBundleError(f"icon license file is empty: {resolved}")
        license_files.append((relative.as_posix(), content))
    if len(license_files) != 4:
        raise LicenseBundleError(
            "Material for MkDocs must provide exactly four icon license files; "
            f"found {len(license_files)}"
        )
    return sorted(license_files)


def audited_asset_notice() -> str:
    content = ASSET_NOTICE_PATH.read_bytes()
    digest = hashlib.sha256(content).hexdigest()
    if digest != ASSET_NOTICE_SHA256:
        raise LicenseBundleError(
            f"audited asset notice digest changed: {digest} != {ASSET_NOTICE_SHA256}"
        )
    return content.decode("utf-8").strip()


def verify_mermaid_runtime_notice(
    notice: str, inventory_packages: list[dict[str, object]]
) -> set[tuple[str, str]]:
    """Cross-check every rendered runtime notice field against its inventory."""

    divider = "=" * 80
    sections = notice.split(f"\n{divider}\n")
    if len(sections) != len(inventory_packages) + 1:
        raise LicenseBundleError(
            "Mermaid runtime notice section count differs from exact inventory"
        )

    expected = {
        (str(package["name"]), str(package["version"])): package
        for package in inventory_packages
    }
    parsed: set[tuple[str, str]] = set()
    heading = re.compile(
        r"^Package: (.+) ([^ \n]+)\n"
        r"License: ([^\n]+)\n"
        r"npm integrity: ([^\n]+)\n\n"
    )
    file_heading = re.compile(r"(?:^|\n\n)--- ([^\n]+) ---\n\n")

    for section in sections[1:]:
        header = heading.match(section)
        if header is None:
            raise LicenseBundleError("invalid Mermaid runtime notice section header")
        name, version, license_label, integrity = header.groups()
        pair = (name, version)
        package = expected.get(pair)
        if package is None or pair in parsed:
            raise LicenseBundleError(
                f"unexpected or duplicate Mermaid runtime notice section: {pair}"
            )
        parsed.add(pair)
        if license_label != package["license"]:
            raise LicenseBundleError(
                f"Mermaid runtime license label differs from inventory: {pair}"
            )
        if integrity != package["integrity"]:
            raise LicenseBundleError(
                f"Mermaid runtime integrity differs from inventory: {pair}"
            )

        payload = section[header.end() :]
        file_matches = list(file_heading.finditer(payload))
        expected_files = package["licenseFiles"]
        if not file_matches or file_matches[0].start() != 0:
            raise LicenseBundleError(
                f"invalid Mermaid runtime license-file layout: {pair}"
            )
        if [match.group(1) for match in file_matches] != [
            item["path"] for item in expected_files
        ]:
            raise LicenseBundleError(
                f"Mermaid runtime license-file paths differ from inventory: {pair}"
            )
        for index, (match, expected_file) in enumerate(
            zip(file_matches, expected_files, strict=True)
        ):
            end = (
                file_matches[index + 1].start()
                if index + 1 < len(file_matches)
                else len(payload)
            )
            rendered_text = payload[match.end() : end].strip().encode("utf-8")
            if hashlib.sha256(rendered_text).hexdigest() != expected_file["sha256"]:
                raise LicenseBundleError(
                    f"Mermaid runtime license text differs from inventory: "
                    f"{pair} {expected_file['path']}"
                )

    return parsed


def runtime_assets() -> tuple[list[dict[str, str]], str]:
    try:
        manifest = json.loads(RUNTIME_ASSET_MANIFEST_PATH.read_text(encoding="utf-8"))
        lock = json.loads(NPM_LOCK_PATH.read_text(encoding="utf-8"))
    except (json.JSONDecodeError, UnicodeDecodeError) as exc:
        raise LicenseBundleError(
            "invalid MkDocs browser-runtime manifest or npm lock"
        ) from exc
    assets = manifest.get("assets")
    if manifest.get("schemaVersion") != 1 or not isinstance(assets, list):
        raise LicenseBundleError("invalid MkDocs browser-runtime asset manifest")
    if {asset.get("name") for asset in assets if isinstance(asset, dict)} != {
        "mermaid",
        "resize-observer-polyfill",
    }:
        raise LicenseBundleError("MkDocs browser-runtime asset allowlist changed")

    required_fields = {
        "name",
        "version",
        "license",
        "packageIntegrity",
        "source",
        "deployed",
        "sha256",
        "materialRemoteURL",
        "materialLocalURL",
    }
    packages = lock.get("packages")
    if not isinstance(packages, dict):
        raise LicenseBundleError("website package-lock.json has no packages map")
    for asset in assets:
        if not isinstance(asset, dict) or set(asset) != required_fields:
            raise LicenseBundleError("MkDocs browser-runtime asset fields changed")
        if not all(isinstance(asset[field], str) for field in required_fields):
            raise LicenseBundleError(
                "MkDocs browser-runtime asset has a non-string field"
            )
        deployed = pathlib.PurePosixPath(asset["deployed"])
        if deployed.is_absolute() or ".." in deployed.parts:
            raise LicenseBundleError(f"unsafe deployed runtime path: {deployed}")
        package = packages.get(f"node_modules/{asset['name']}")
        if not isinstance(package, dict) or any(
            package.get(field) != asset[manifest_field]
            for field, manifest_field in (
                ("version", "version"),
                ("license", "license"),
                ("integrity", "packageIntegrity"),
            )
        ):
            raise LicenseBundleError(
                f"locked browser-runtime identity changed: {asset['name']}"
            )
        if not re.fullmatch(r"[0-9a-f]{64}", asset["sha256"]):
            raise LicenseBundleError(f"invalid runtime SHA-256: {asset['name']}")

    audit = manifest.get("mermaidBundleAudit")
    audit_fields = {
        "sourceMap",
        "sourceMapSha256",
        "embeddedParserPackage",
        "embeddedParserVersion",
        "embeddedParserIntegrity",
        "embeddedParserChunkCount",
        "embeddedParserMapsSha256",
        "inventory",
        "inventorySha256",
        "licenses",
        "licensesSha256",
    }
    if not isinstance(audit, dict) or set(audit) != audit_fields:
        raise LicenseBundleError("invalid Mermaid bundled-code audit manifest")

    parser_lock = packages.get(audit["embeddedParserPackage"])
    if not isinstance(parser_lock, dict) or any(
        parser_lock.get(field) != audit[manifest_field]
        for field, manifest_field in (
            ("version", "embeddedParserVersion"),
            ("integrity", "embeddedParserIntegrity"),
        )
    ):
        raise LicenseBundleError("locked @mermaid-js/parser identity changed")

    def audited_file(path_field: str, digest_field: str) -> bytes:
        relative = pathlib.PurePosixPath(audit[path_field])
        if relative.is_absolute() or ".." in relative.parts:
            raise LicenseBundleError(f"unsafe Mermaid audit path: {relative}")
        content = ROOT.joinpath(*relative.parts).read_bytes()
        digest = hashlib.sha256(content).hexdigest()
        if digest != audit[digest_field]:
            raise LicenseBundleError(
                f"Mermaid audit file changed: {relative}: "
                f"{digest} != {audit[digest_field]}"
            )
        return content

    inventory_bytes = audited_file("inventory", "inventorySha256")
    license_bytes = audited_file("licenses", "licensesSha256")
    try:
        inventory = json.loads(inventory_bytes)
        notice = license_bytes.decode("utf-8").strip()
    except (json.JSONDecodeError, UnicodeDecodeError) as exc:
        raise LicenseBundleError(
            "invalid Mermaid audit inventory or license text"
        ) from exc
    if (
        set(inventory)
        != {
            "schemaVersion",
            "mermaidSourceMapSha256",
            "embeddedParserChunkCount",
            "embeddedParserMapsSha256",
            "packages",
        }
        or inventory["schemaVersion"] != 1
    ):
        raise LicenseBundleError("Mermaid runtime inventory schema changed")
    for inventory_field, audit_field in (
        ("mermaidSourceMapSha256", "sourceMapSha256"),
        ("embeddedParserChunkCount", "embeddedParserChunkCount"),
        ("embeddedParserMapsSha256", "embeddedParserMapsSha256"),
    ):
        if inventory[inventory_field] != audit[audit_field]:
            raise LicenseBundleError(
                f"Mermaid inventory differs from audit manifest: {inventory_field}"
            )

    inventory_packages = inventory["packages"]
    if not isinstance(inventory_packages, list):
        raise LicenseBundleError("Mermaid runtime inventory has no packages list")
    expected_pairs: set[tuple[str, str]] = set()
    package_fields = {
        "name",
        "version",
        "license",
        "integrity",
        "tarball",
        "licenseFiles",
    }
    for package in inventory_packages:
        if not isinstance(package, dict) or set(package) != package_fields:
            raise LicenseBundleError("Mermaid runtime package inventory fields changed")
        pair = (package["name"], package["version"])
        if not all(isinstance(value, str) for value in pair) or pair in expected_pairs:
            raise LicenseBundleError(
                f"invalid duplicate Mermaid runtime package: {pair}"
            )
        expected_pairs.add(pair)
        if not isinstance(package["license"], str) or not package["license"]:
            raise LicenseBundleError(
                f"invalid license label for Mermaid package: {pair}"
            )
        if not isinstance(package["tarball"], str) or not package["tarball"].startswith(
            "https://registry.npmjs.org/"
        ):
            raise LicenseBundleError(f"invalid npm tarball for Mermaid package: {pair}")
        if not isinstance(package["integrity"], str) or not package[
            "integrity"
        ].startswith("sha512-"):
            raise LicenseBundleError(
                f"invalid npm integrity for Mermaid package: {pair}"
            )
        license_files = package["licenseFiles"]
        if not isinstance(license_files, list) or not license_files:
            raise LicenseBundleError(f"Mermaid package has no license files: {pair}")
        if not all(
            isinstance(item, dict)
            and set(item) == {"path", "sha256"}
            and isinstance(item["path"], str)
            and re.fullmatch(r"[0-9a-f]{64}", item["sha256"])
            for item in license_files
        ):
            raise LicenseBundleError(f"invalid Mermaid license-file inventory: {pair}")

    notice_pairs = verify_mermaid_runtime_notice(notice, inventory_packages)
    if notice_pairs != expected_pairs:
        raise LicenseBundleError(
            "Mermaid runtime license headings differ from exact inventory"
        )
    for required in [(asset["name"], asset["version"]) for asset in assets]:
        if required not in expected_pairs:
            raise LicenseBundleError(f"runtime license inventory omits {required}")
    parser_pair = ("@mermaid-js/parser", audit["embeddedParserVersion"])
    if parser_pair not in expected_pairs:
        raise LicenseBundleError("runtime license inventory omits embedded parser")

    source_map = ROOT / audit["sourceMap"]
    if source_map.is_file():
        try:
            detected_pairs, detected_audit = installed_mermaid_runtime_inventory()
        except (MermaidAuditError, json.JSONDecodeError, OSError) as exc:
            raise LicenseBundleError(
                "unable to audit installed Mermaid runtime"
            ) from exc
        if detected_pairs != expected_pairs:
            raise LicenseBundleError(
                "installed Mermaid bundled-code inventory changed: "
                f"detected={sorted(detected_pairs)}, expected={sorted(expected_pairs)}"
            )
        for detected_field, audit_field in (
            ("mermaidSourceMapSha256", "sourceMapSha256"),
            ("embeddedParserChunkCount", "embeddedParserChunkCount"),
            ("embeddedParserMapsSha256", "embeddedParserMapsSha256"),
        ):
            if detected_audit[detected_field] != audit[audit_field]:
                raise LicenseBundleError(
                    f"installed Mermaid source-map audit changed: {detected_field}"
                )
    return assets, notice


def license_label(distribution: importlib.metadata.Distribution) -> str:
    label = distribution.metadata.get("License-Expression")
    if not label:
        label = distribution.metadata.get("License")
    if label and label.upper() != "UNKNOWN":
        return " ".join(label.split())
    classifiers = [
        value.removeprefix("License :: ")
        for value in distribution.metadata.get_all("Classifier", [])
        if value.startswith("License :: ")
    ]
    return "; ".join(classifiers) if classifiers else "See bundled license text"


def render_bundle(lock_path: pathlib.Path = LOCK_PATH) -> str:
    _, browser_notice = runtime_assets()
    sections = [
        "MkDocs documentation third-party licenses",
        "",
        "Generated from the exact hashed Python closure in requirements.txt and",
        "the audited source-map-derived Mermaid/ResizeObserver runtime closure.",
        "Do not edit this file by hand; run make mkdocs-licenses.",
    ]
    for canonical, (locked_name, locked_version) in sorted(
        locked_requirements(lock_path).items()
    ):
        try:
            distribution = importlib.metadata.distribution(locked_name)
        except importlib.metadata.PackageNotFoundError as exc:
            raise LicenseBundleError(
                f"locked distribution is not installed: {locked_name}=={locked_version}"
            ) from exc
        installed_name = distribution.metadata.get("Name", locked_name)
        if canonical_name(installed_name) != canonical:
            raise LicenseBundleError(
                f"distribution name mismatch: {installed_name} != {locked_name}"
            )
        if distribution.version != locked_version:
            raise LicenseBundleError(
                f"installed version mismatch for {locked_name}: "
                f"{distribution.version} != {locked_version}"
            )
        sections.extend(
            [
                "",
                "=" * 80,
                f"Package: {installed_name} {distribution.version}",
                f"License: {license_label(distribution)}",
            ]
        )
        for relative, content in distribution_license_files(distribution):
            sections.extend(["", f"--- {relative} ---", "", content])
        if canonical == "mkdocs-material":
            for relative, content in material_icon_license_files(distribution):
                sections.extend(["", f"--- {relative} ---", "", content])
    sections.extend(
        [
            "",
            "=" * 80,
            audited_asset_notice(),
            "",
            "=" * 80,
            "Apache License, Version 2.0 (full text for RxJS 7.8.2)",
            "",
            (ROOT / "LICENSE").read_text(encoding="utf-8").strip(),
            "",
            "=" * 80,
            "MkDocs browser-runtime dependency licenses",
            "",
            browser_notice,
        ]
    )
    return "\n".join(sections).rstrip() + "\n"


def source_map_packages(site: pathlib.Path) -> set[str]:
    maps = sorted(site.rglob("*.map"))
    if not maps:
        raise LicenseBundleError(f"no source maps found beneath {site}")
    packages: set[str] = set()
    marker = re.compile(r"(?:^|/)node_modules/((?:@[^/]+/)?[^/]+)")
    for source_map in maps:
        try:
            document = json.loads(source_map.read_text(encoding="utf-8"))
        except (json.JSONDecodeError, UnicodeDecodeError) as exc:
            raise LicenseBundleError(f"invalid source map: {source_map}") from exc
        sources = document.get("sources")
        if not isinstance(sources, list) or not all(
            isinstance(source, str) for source in sources
        ):
            raise LicenseBundleError(f"source map has no string sources: {source_map}")
        for source in sources:
            match = marker.search(source)
            if match:
                packages.add(match.group(1))
    return packages


def _is_remote_resource(value: str | None) -> bool:
    return bool(value and re.match(r"^(?:https?:)?//", value, re.IGNORECASE))


class _ResourceReferenceParser(html.parser.HTMLParser):
    def __init__(self, source: pathlib.Path) -> None:
        super().__init__(convert_charrefs=True)
        self.source = source
        self.remote: list[str] = []

    def handle_starttag(self, tag: str, attrs: list[tuple[str, str | None]]) -> None:
        attributes = {name.lower(): value for name, value in attrs}
        lowered_tag = tag.lower()
        if lowered_tag == "script" and _is_remote_resource(attributes.get("src")):
            self.remote.append(f"script src={attributes['src']}")
        executable_attribute = {
            "iframe": "src",
            "embed": "src",
            "object": "data",
        }.get(lowered_tag)
        if executable_attribute and _is_remote_resource(
            attributes.get(executable_attribute)
        ):
            self.remote.append(
                f"{lowered_tag} {executable_attribute}="
                f"{attributes[executable_attribute]}"
            )
        if lowered_tag != "link" or not _is_remote_resource(attributes.get("href")):
            return
        rel = set((attributes.get("rel") or "").lower().split())
        resource_kind = (attributes.get("as") or "").lower()
        if (
            rel & {"stylesheet", "modulepreload"}
            or ("preload" in rel and resource_kind in {"font", "script", "style"})
            or "fonts.googleapis.com" in (attributes.get("href") or "")
            or "fonts.gstatic.com" in (attributes.get("href") or "")
        ):
            self.remote.append(
                f"link rel={attributes.get('rel')} href={attributes['href']}"
            )


def verify_no_remote_runtime_resources(site: pathlib.Path) -> None:
    offenders: list[str] = []
    for html_file in sorted(site.rglob("*.html")):
        parser = _ResourceReferenceParser(html_file)
        parser.feed(html_file.read_text(encoding="utf-8"))
        offenders.extend(f"{html_file}: {entry}" for entry in parser.remote)
    remote_css = re.compile(
        r"(?:@import\s+(?:url\()?\s*['\"]?(?:https?:)?//|url\(\s*['\"]?(?:https?:)?//)",
        re.IGNORECASE,
    )
    for css_file in sorted(site.rglob("*.css")):
        if remote_css.search(css_file.read_text(encoding="utf-8")):
            offenders.append(f"{css_file}: remote CSS import/font/image URL")
    for js_file in sorted(site.rglob("*.js")):
        content = js_file.read_text(encoding="utf-8")
        if re.search(r"https?://unpkg\.com/", content, re.IGNORECASE):
            offenders.append(f"{js_file}: mutable unpkg executable URL")
    if offenders:
        raise LicenseBundleError(
            "built site contains remote executable/style/font resources: "
            + "; ".join(offenders)
        )


def verify_built_site(site: pathlib.Path, rendered: str) -> None:
    deployed_bundle = site / OUTPUT_PATH.name
    if (
        not deployed_bundle.is_file()
        or deployed_bundle.read_text(encoding="utf-8") != rendered
    ):
        raise LicenseBundleError(
            f"built site does not contain the exact license bundle: {deployed_bundle}"
        )
    detected = source_map_packages(site)
    if detected != EXPECTED_SOURCE_MAP_PACKAGES:
        raise LicenseBundleError(
            "built source-map package set differs from the audited allowlist: "
            f"detected={sorted(detected)}, expected={sorted(EXPECTED_SOURCE_MAP_PACKAGES)}"
        )
    for package in sorted(EXPECTED_ASSET_NOTICE_PACKAGES):
        if f"- {package} " not in rendered:
            raise LicenseBundleError(
                f"source-map package is absent from the asset notice: {package}"
            )
    unused_locales = site / "assets" / "javascripts" / "lunr"
    if unused_locales.exists():
        raise LicenseBundleError(
            f"unused lunr-languages assets were redistributed: {unused_locales}"
        )

    assets, _ = runtime_assets()
    by_name = {asset["name"]: asset for asset in assets}
    for asset in assets:
        deployed = site / asset["deployed"]
        if not deployed.is_file():
            raise LicenseBundleError(f"vendored runtime asset is missing: {deployed}")
        if hashlib.sha256(deployed.read_bytes()).hexdigest() != asset["sha256"]:
            raise LicenseBundleError(f"vendored runtime asset changed: {deployed}")

    material_bundles = sorted((site / "assets" / "javascripts").glob("bundle.*.min.js"))
    if len(material_bundles) != 1:
        raise LicenseBundleError(
            f"expected exactly one Material JavaScript bundle; found {len(material_bundles)}"
        )
    material = material_bundles[0].read_text(encoding="utf-8")
    name_match = re.fullmatch(
        r"bundle\.([0-9a-f]{16})\.min\.js", material_bundles[0].name
    )
    material_digest = hashlib.sha256(material_bundles[0].read_bytes()).hexdigest()
    if name_match is None or name_match.group(1) != material_digest[:16]:
        raise LicenseBundleError(
            f"Material bundle is not SHA-256 content-addressed: {material_bundles[0]}"
        )
    material_map_path = material_bundles[0].with_name("bundle.local.min.js.map")
    if not material_map_path.is_file():
        raise LicenseBundleError("patched Material source map is missing")
    try:
        material_map = json.loads(material_map_path.read_text(encoding="utf-8"))
    except (json.JSONDecodeError, UnicodeDecodeError) as exc:
        raise LicenseBundleError("patched Material source map is invalid") from exc
    if material_map.get("file") != material_bundles[0].name:
        raise LicenseBundleError("patched Material source-map file identity changed")
    sources_content = material_map.get("sourcesContent")
    if not isinstance(sources_content, list) or not all(
        isinstance(source, str) for source in sources_content
    ):
        raise LicenseBundleError("patched Material map has no string sourcesContent")
    expected_map_reference = "//# sourceMappingURL=bundle.local.min.js.map"
    if material.count(expected_map_reference) != 1:
        raise LicenseBundleError("patched Material source-map reference changed")
    for asset in assets:
        if asset["materialRemoteURL"] in material:
            raise LicenseBundleError(
                f"Material still contains a remote loader: {asset['materialRemoteURL']}"
            )
        local_url = asset["materialLocalURL"]
        if len(local_url.encode("utf-8")) != len(
            asset["materialRemoteURL"].encode("utf-8")
        ):
            raise LicenseBundleError(
                f"Material local fallback changes source-map offsets: {asset['name']}"
            )
        if material.count(local_url) != 1:
            raise LicenseBundleError(
                f"Material local fallback inventory changed: {asset['name']}"
            )
        if sum(source.count(local_url) for source in sources_content) != 1:
            raise LicenseBundleError(
                f"Material local source-map inventory changed: {asset['name']}"
            )
        if any(asset["materialRemoteURL"] in source for source in sources_content):
            raise LicenseBundleError(
                f"Material source map retains remote loader: {asset['name']}"
            )

    for html_file in sorted(site.rglob("*.html")):
        content = html_file.read_text(encoding="utf-8")
        bundle_position = content.find("assets/javascripts/bundle.")
        resize = by_name["resize-observer-polyfill"]
        resize_reference = f"{resize['deployed']}?v={resize['sha256']}"
        resize_position = content.find(resize_reference)
        if (
            content.count(resize_reference) != 1
            or bundle_position < 0
            or resize_position >= bundle_position
        ):
            raise LicenseBundleError(
                f"local ResizeObserver fallback is not loaded before Material: {html_file}"
            )
        mermaid = by_name["mermaid"]
        mermaid_reference = f"{mermaid['deployed']}?v={mermaid['sha256']}"
        if '<pre class="mermaid">' in content:
            mermaid_position = content.find(mermaid_reference)
            if (
                content.count(mermaid_reference) != 1
                or mermaid_position >= bundle_position
            ):
                raise LicenseBundleError(
                    f"local Mermaid runtime is not loaded before Material: {html_file}"
                )
        elif mermaid_reference in content:
            raise LicenseBundleError(
                f"Mermaid runtime is unnecessarily loaded on {html_file}"
            )

    verify_no_remote_runtime_resources(site)


def main() -> int:
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument(
        "--check",
        action="store_true",
        help="fail if the committed bundle differs from the locked environment",
    )
    parser.add_argument(
        "--site",
        type=pathlib.Path,
        help="verify built source maps and the deployed license bundle",
    )
    parser.add_argument("--output", type=pathlib.Path, default=OUTPUT_PATH)
    args = parser.parse_args()

    try:
        rendered = render_bundle()
        if args.check:
            if (
                not args.output.is_file()
                or args.output.read_text(encoding="utf-8") != rendered
            ):
                raise LicenseBundleError(
                    f"{args.output} is stale; run make mkdocs-licenses"
                )
            print(f"verified {args.output}")
        else:
            args.output.parent.mkdir(parents=True, exist_ok=True)
            args.output.write_text(rendered, encoding="utf-8", newline="\n")
            print(f"wrote {args.output}")
        if args.site is not None:
            verify_built_site(args.site.resolve(), rendered)
            print(f"verified built MkDocs assets beneath {args.site}")
        return 0
    except LicenseBundleError as exc:
        print(f"error: {exc}", file=sys.stderr)
        return 1


if __name__ == "__main__":
    raise SystemExit(main())
