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

"""MkDocs release hooks."""

from __future__ import annotations

import hashlib
import json
import logging
import os
import pathlib
import re
import shutil


LOG = logging.getLogger("mkdocs.hooks.cisco-vk")
ROOT = pathlib.Path(__file__).resolve().parents[2]
RUNTIME_ASSET_MANIFEST = (
    ROOT / "third_party" / "mkdocs-material" / "9.7.7" / "RUNTIME_ASSETS.json"
)
MATERIAL_SOURCE_MAP_NAME = "bundle.local.min.js.map"


def _sha256(path: pathlib.Path) -> str:
    return hashlib.sha256(path.read_bytes()).hexdigest()


def _vendor_runtime_assets(site_dir: pathlib.Path) -> list[dict[str, str]]:
    manifest = json.loads(RUNTIME_ASSET_MANIFEST.read_text(encoding="utf-8"))
    if manifest.get("schemaVersion") != 1 or not isinstance(
        manifest.get("assets"), list
    ):
        raise RuntimeError(
            f"invalid MkDocs runtime asset manifest: {RUNTIME_ASSET_MANIFEST}"
        )

    assets = manifest["assets"]
    if {asset.get("name") for asset in assets} != {
        "mermaid",
        "resize-observer-polyfill",
    }:
        raise RuntimeError(
            "MkDocs runtime asset manifest does not match the audited allowlist"
        )

    for asset in assets:
        source = (ROOT / asset["source"]).resolve()
        if not source.is_relative_to(ROOT) or not source.is_file():
            raise RuntimeError(
                f"audited MkDocs runtime asset is not installed: {asset['source']}; "
                "run npm ci in docs/website"
            )
        if _sha256(source) != asset["sha256"]:
            raise RuntimeError(
                f"audited MkDocs runtime asset changed: {asset['source']}"
            )
        destination = (site_dir / asset["deployed"]).resolve()
        if not destination.is_relative_to(site_dir):
            raise RuntimeError(f"refusing MkDocs runtime asset path: {destination}")
        destination.parent.mkdir(parents=True, exist_ok=True)
        shutil.copyfile(source, destination)
        if _sha256(destination) != asset["sha256"]:
            raise RuntimeError(f"copied MkDocs runtime asset changed: {destination}")
    return assets


def _replace_material_remote_loaders(
    site_dir: pathlib.Path, assets: list[dict[str, str]]
) -> None:
    bundles = sorted((site_dir / "assets" / "javascripts").glob("bundle.*.min.js"))
    if len(bundles) != 1:
        raise RuntimeError(
            f"expected exactly one Material JavaScript bundle, found {len(bundles)}"
        )
    bundle = bundles[0]
    source_map_path = bundle.with_name(f"{bundle.name}.map")
    if not source_map_path.is_file():
        raise RuntimeError(
            f"Material JavaScript source map is missing: {source_map_path}"
        )
    content = bundle.read_text(encoding="utf-8")
    source_map = json.loads(source_map_path.read_text(encoding="utf-8"))
    sources_content = source_map.get("sourcesContent")
    if not isinstance(sources_content, list) or not all(
        isinstance(source, str) for source in sources_content
    ):
        raise RuntimeError(
            f"Material source map has no string sourcesContent: {source_map_path}"
        )

    for asset in assets:
        remote = asset["materialRemoteURL"]
        local = asset["materialLocalURL"]
        if len(remote.encode("utf-8")) != len(local.encode("utf-8")):
            raise RuntimeError(
                f"Material local loader must preserve source-map offsets: {asset['name']}"
            )
        if content.count(remote) != 1:
            raise RuntimeError(f"Material remote loader inventory changed: {remote}")
        source_map_matches = sum(source.count(remote) for source in sources_content)
        if source_map_matches != 1:
            raise RuntimeError(
                f"Material source-map loader inventory changed: {asset['name']}"
            )
        content = content.replace(remote, local)
        sources_content = [source.replace(remote, local) for source in sources_content]

    original_map_reference = f"//# sourceMappingURL={bundle.name}.map"
    local_map_reference = f"//# sourceMappingURL={MATERIAL_SOURCE_MAP_NAME}"
    if content.count(original_map_reference) != 1:
        raise RuntimeError(f"Material source-map reference changed: {bundle}")
    content = content.replace(original_map_reference, local_map_reference)
    digest = hashlib.sha256(content.encode("utf-8")).hexdigest()
    deployed_name = f"bundle.{digest[:16]}.min.js"
    deployed_bundle = bundle.with_name(deployed_name)
    deployed_bundle.write_text(content, encoding="utf-8", newline="")

    source_map["file"] = deployed_name
    source_map["sourcesContent"] = sources_content
    deployed_source_map = bundle.with_name(MATERIAL_SOURCE_MAP_NAME)
    deployed_source_map.write_text(
        json.dumps(source_map, ensure_ascii=False, separators=(",", ":")) + "\n",
        encoding="utf-8",
        newline="",
    )
    bundle.unlink()
    source_map_path.unlink()

    for html_file in sorted(site_dir.rglob("*.html")):
        html = html_file.read_text(encoding="utf-8")
        if html.count(bundle.name) != 1:
            raise RuntimeError(
                f"expected one Material bundle reference in {html_file}; "
                f"found {html.count(bundle.name)}"
            )
        html_file.write_text(
            html.replace(bundle.name, deployed_name), encoding="utf-8", newline=""
        )


def _relative_asset_url(
    html_file: pathlib.Path, site_dir: pathlib.Path, asset: dict[str, str]
) -> str:
    destination = site_dir / asset["deployed"]
    relative = os.path.relpath(destination, html_file.parent).replace(os.sep, "/")
    return f"{relative}?v={asset['sha256']}"


def _inject_local_runtime_scripts(
    site_dir: pathlib.Path, assets: list[dict[str, str]]
) -> None:
    by_name = {asset["name"]: asset for asset in assets}
    material_script = re.compile(
        r'(?P<indent>[ \t]*)<script src="[^"\n]*assets/javascripts/bundle\.[^"\n]+\.min\.js"></script>'
    )
    for html_file in sorted(site_dir.rglob("*.html")):
        content = html_file.read_text(encoding="utf-8")
        matches = list(material_script.finditer(content))
        if len(matches) != 1:
            raise RuntimeError(
                f"expected exactly one Material script in {html_file}; found {len(matches)}"
            )
        match = matches[0]
        indent = match.group("indent")
        resize_url = _relative_asset_url(
            html_file, site_dir, by_name["resize-observer-polyfill"]
        )
        resize_loader = (
            f"{indent}<script>if(!window.ResizeObserver){{document.write("
            f"'<script src=\"{resize_url}\"><\\/script>')}}</script>"
        )
        injected = [resize_loader]
        if '<pre class="mermaid">' in content:
            mermaid_url = _relative_asset_url(html_file, site_dir, by_name["mermaid"])
            injected.append(f'{indent}<script src="{mermaid_url}"></script>')
        injected.append(match.group(0))
        content = (
            content[: match.start()] + "\n".join(injected) + content[match.end() :]
        )
        html_file.write_text(content, encoding="utf-8", newline="")


def on_post_build(config: dict) -> None:
    """Remove unused locale search assets from the English-only site.

    Material for MkDocs copies every lunr-languages bundle, including MPL-1.1
    files and bundled tokenizers, even when the search plugin is configured for
    English only. They are not requested by this site, so do not redistribute
    that unused code.
    """

    site_dir = pathlib.Path(config["site_dir"]).resolve()
    lunr_dir = (site_dir / "assets" / "javascripts" / "lunr").resolve()
    if not lunr_dir.is_relative_to(site_dir):
        raise RuntimeError(f"refusing to clean a path outside site_dir: {lunr_dir}")
    if not lunr_dir.is_dir():
        raise RuntimeError(
            f"expected Material locale asset directory is missing: {lunr_dir}"
        )
    shutil.rmtree(lunr_dir)
    LOG.info("Removed unused non-English lunr language assets")

    assets = _vendor_runtime_assets(site_dir)
    _replace_material_remote_loaders(site_dir, assets)
    _inject_local_runtime_scripts(site_dir, assets)
    LOG.info("Vendored exact Mermaid and ResizeObserver runtime assets")
