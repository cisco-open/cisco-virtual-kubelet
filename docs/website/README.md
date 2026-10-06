# Dynamic documentation website

Use `npm ci`, `npm run lint`, `npm audit --audit-level=high`, then
`npm run build`. The build also checks the generated third-party notices.
Run `npm run dev` for a local preview.

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
