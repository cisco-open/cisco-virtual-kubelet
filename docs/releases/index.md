# Releases and archive

Match images, chart versions and signed artifacts to the version you install.
This documentation targets proposed **v2026.11.1**, the corrected November
candidate. Preparation does not establish publication. The GitHub release page
determines artifact availability; the previous published baseline remains
**v2026.10.0**.

## November 2026

[November release notes](v2026.11.1.md) describe the changes from published
October `v2026.10.0`, the migration path and known limitations. The
[November preparation record](v2026.11.1-readiness.md) explains the remaining
candidate and artifact checks, including the embedded-source packaging fix.

**Rejected and unpublished:** `v2026.11.0` failed exact staged-image licensing
inspection. Its tag and artifacts are immutable recovery markers; do not publish
or install that candidate. Read its [historical notes](v2026.11.0.md) and
[rejection record](v2026.11.0-readiness.md).

[Check November publication and artifacts](https://github.com/cisco-open/cisco-virtual-kubelet/releases/tag/v2026.11.1){ .md-button .md-button--primary }
[Feature availability](../status.md){ .md-button }

## Published releases

Until November is published, use the signed
[October v2026.10.0 release](https://github.com/cisco-open/cisco-virtual-kubelet/releases/tag/v2026.10.0).
The [GitHub release list](https://github.com/cisco-open/cisco-virtual-kubelet/releases)
is the authority for publication status and available artifacts.

## Release preparation records

These pages preserve the preparation history. Candidate-era wording describes
the record at that time; it does not override the published release.

| Record | Context |
| --- | --- |
| [v2026.11.0 notes](v2026.11.0.md) | Rejected, unpublished November candidate; embedded-source bundle failed the licensing gate. |
| [v2026.11.0 preparation record](v2026.11.0-readiness.md) | Original preparation history and rejection marker. |
| [October preparation notes](v2026.10.0.md) | Planned scope and upgrade considerations for v2026.10.0. |
| [October readiness ledger](v2026.10.0-readiness.md) | Checks and evidence recorded during release preparation. |
| [v2026.9.2 notes](v2026.9.2.md) | September release notes. |
| [v2026.9.1 candidate](v2026.9.1.md) | Unpublished preparation draft. |
| [v2026.9.0 candidate](v2026.9.0.md) | Unpublished preparation draft. |

## Topology development history

The [bounded merge scope](../topology-merge-scope.md) and
[November handoff](../topology-november-handoff.md) preserve dated development
decisions and qualification evidence. The handoff records an October checkpoint;
its branch instructions and review status are historical.

Evidence applies to its recorded revision and scope. For current instructions,
use the [managed topology guide](../topology-awareness.md); for remaining work,
see the [roadmap](../topology-roadmap.md) and
[execution plan](../topology-roadmap-execution.md).
