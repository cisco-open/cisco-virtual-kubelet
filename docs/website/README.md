# Dynamic documentation website

The landing page is a Next.js static export. MkDocs builds the detailed guides
under `/cisco-virtual-kubelet/docs/`.

## Combined local preview

Run the following from the **repository root**, using Node.js 24 and Python
3.13 (`python3`). The [publication workflow](../../.github/workflows/develop.yml)
pins the exact CI versions. Install Python dependencies from the hash-locked
`requirements.txt`; the npm build checks the generated third-party notices.

```bash
(
  set -e
  cvk_preview="$(mktemp -d "${TMPDIR:-/tmp}/cvk-docs-preview.XXXXXX")"
  python3 -m venv "$cvk_preview/venv"
  "$cvk_preview/venv/bin/python" -m pip install --require-hashes -r requirements.txt

  # Build the landing page and install the assets used by MkDocs.
  (
    cd docs/website
    npm ci --include=dev
    npm run lint
    npm audit --audit-level=high
    npm run build
  )

  # Build the guides and verify their license bundle.
  "$cvk_preview/venv/bin/python" .github/scripts/generate_mkdocs_licenses.py --check
  "$cvk_preview/venv/bin/python" -m mkdocs build --strict
  "$cvk_preview/venv/bin/python" .github/scripts/generate_mkdocs_licenses.py --check --site site

  # Match the deployed URL layout, keeping the virtualenv outside the server root.
  mkdir -p "$cvk_preview/public/cisco-virtual-kubelet/docs"
  cp -R docs/website/out/. "$cvk_preview/public/cisco-virtual-kubelet/"
  cp -R site/. "$cvk_preview/public/cisco-virtual-kubelet/docs/"
  "$cvk_preview/venv/bin/python" -m http.server 3000 \
    --bind 127.0.0.1 --directory "$cvk_preview/public"
)
```

Open the [landing page](http://127.0.0.1:3000/cisco-virtual-kubelet/) or
[documentation](http://127.0.0.1:3000/cisco-virtual-kubelet/docs/). Stop the server
with **Ctrl+C**. This static preview reflects the current checkout; rebuild and
assemble fresh outputs after edits. Publication instead builds the verified tag
of an existing published release.

For landing-page iteration alone, run `npm run dev -- --hostname 127.0.0.1`
from `docs/website` and open the same landing-page URL. **The Next.js dev server
does not serve MkDocs**; use the combined preview to review documentation links
and navigation.

## Lint dependency security override

Next's ESLint plugin currently uses `fast-glob` only to find root directories
through `globSync(pattern, { onlyDirectories: true })`. Its dependency chain
includes `braces <=3.0.3`, affected by
[GHSA-vfj7-8cjw-p6xm](https://github.com/advisories/GHSA-vfj7-8cjw-p6xm), with no
patched version at the 4 October 2026 review.

The scoped npm override uses the small local `scripts/next-root-glob` adapter
with pinned `tinyglobby@0.2.15` for this one plugin dependency. Directly aliasing
tinyglobby is **not** compatible: it expands a literal root into its contents.
The adapter disables that behavior and preserves absolute input paths. It
implements only Next's directory `globSync` call, failing on unsupported calls;
it is not a general replacement for arbitrary fast-glob consumers.

`npm run lint` first tests the resolved dependency, literal and glob directory
discovery and the actual Next internal-link rule. Results can differ in trailing
separators; tests verify the paths and rule behavior consumed by the plugin,
not byte-identical output. Next/ESLint versions are unchanged.

Remove this override when the upstream plugin uses an unaffected dependency
chain, then update the regression test and lockfile. Re-run clean installation,
lint, audit, license checks and production build. Do not suppress the advisory
or use a forced major downgrade to make the audit green.
