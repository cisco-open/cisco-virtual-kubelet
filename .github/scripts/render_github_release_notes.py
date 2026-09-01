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

"""Render canonical MkDocs release notes for a curated GitHub Release draft."""

from __future__ import annotations

import argparse
import pathlib
import posixpath
import re
import urllib.parse


RELEASE_TAG = re.compile(r"v[1-9][0-9]{3}\.([1-9]|1[0-2])\.(0|[1-9][0-9]{0,8})")
REPOSITORY = re.compile(r"[A-Za-z0-9_.-]+/[A-Za-z0-9_.-]+")
MARKDOWN_DESTINATION = re.compile(
    r"(?P<prefix>\]\()(?P<destination><[^>\n]+>|[^)\s\n]+)(?P<suffix>\))"
)


class ReleaseNotesError(ValueError):
    """Raised when release notes cannot be rendered safely."""


def _validate_identity(tag: str, repository: str) -> None:
    if RELEASE_TAG.fullmatch(tag) is None:
        raise ReleaseNotesError(f"invalid release tag: {tag}")
    if REPOSITORY.fullmatch(repository) is None:
        raise ReleaseNotesError(f"invalid GitHub repository: {repository}")


def _github_destination(
    destination: str,
    *,
    source_relative: pathlib.PurePosixPath,
    tag: str,
    repository: str,
) -> str:
    wrapped = destination.startswith("<") and destination.endswith(">")
    raw_destination = destination[1:-1] if wrapped else destination
    parsed = urllib.parse.urlsplit(raw_destination)

    # Absolute URLs, email links, and same-document anchors already render
    # correctly in GitHub and must not be rewritten.
    if parsed.scheme or parsed.netloc or raw_destination.startswith("#"):
        return destination
    if not parsed.path:
        return destination
    if parsed.path.startswith("/") or "\\" in parsed.path:
        raise ReleaseNotesError(
            f"release-note link must be repository-relative: {raw_destination}"
        )

    decoded_path = urllib.parse.unquote(parsed.path)
    joined = posixpath.normpath(
        (source_relative.parent / pathlib.PurePosixPath(decoded_path)).as_posix()
    )
    if joined == ".." or joined.startswith("../"):
        raise ReleaseNotesError(
            f"release-note link escapes the repository: {raw_destination}"
        )

    encoded_path = urllib.parse.quote(joined, safe="/-._~")
    rendered = urllib.parse.urlunsplit(
        (
            "https",
            "github.com",
            f"/{repository}/blob/{tag}/{encoded_path}",
            parsed.query,
            parsed.fragment,
        )
    )
    return f"<{rendered}>" if wrapped else rendered


def render_release_notes(
    text: str,
    *,
    source_relative: pathlib.PurePosixPath,
    tag: str,
    repository: str,
) -> str:
    """Replace repository-relative Markdown links with tag-pinned GitHub URLs."""

    _validate_identity(tag, repository)
    if source_relative.is_absolute() or ".." in source_relative.parts:
        raise ReleaseNotesError("source path must be repository-relative")

    def replace(match: re.Match[str]) -> str:
        destination = _github_destination(
            match.group("destination"),
            source_relative=source_relative,
            tag=tag,
            repository=repository,
        )
        return f'{match.group("prefix")}{destination}{match.group("suffix")}'

    return MARKDOWN_DESTINATION.sub(replace, text)


def build_parser() -> argparse.ArgumentParser:
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--source", type=pathlib.Path, required=True)
    parser.add_argument("--output", type=pathlib.Path, required=True)
    parser.add_argument("--repo-root", type=pathlib.Path, required=True)
    parser.add_argument("--tag", required=True)
    parser.add_argument("--repository", required=True)
    return parser


def main() -> int:
    args = build_parser().parse_args()
    root = args.repo_root.resolve(strict=True)
    source = args.source.resolve(strict=True)
    output = args.output.resolve()
    try:
        source_relative = pathlib.PurePosixPath(source.relative_to(root).as_posix())
    except ValueError as error:
        raise ReleaseNotesError("source file must be inside the repository") from error
    if output == source:
        raise ReleaseNotesError("refusing to overwrite canonical release notes")

    rendered = render_release_notes(
        source.read_text(encoding="utf-8"),
        source_relative=source_relative,
        tag=args.tag,
        repository=args.repository,
    )
    output.write_text(rendered, encoding="utf-8")
    return 0


if __name__ == "__main__":
    raise SystemExit(main())
