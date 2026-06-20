#!/usr/bin/env python3
"""Upsert a markdown Lab CI report on the upstream PR.

Invoked as the final step of each lab-ci workflow when
upstream_repo / upstream_pr / upstream_sha inputs are present.

The report is rebuilt from GitHub's state every time (commit
statuses + workflow run jobs/steps), so it's idempotent and
race-free: whichever workflow finishes last simply rebuilds with
complete data. Earlier calls produce partial reports that get
overwritten as more data arrives.

Environment:
  UPSTREAM_REPO        owner/repo of the upstream (where statuses + comment live)
  UPSTREAM_SHA         commit SHA being tested
  UPSTREAM_PR          PR number (where the comment goes)
  FORK_REPO            fork repo (where the workflow runs live)
  GH_TOKEN             PAT with public_repo scope (statuses + comments)

Exits 0 on any expected failure (missing token, API error) so a
reporting hiccup never fails the main CI check. Writes a one-line
warning to stderr in those cases.
"""

import io
import json
import os
import re
import sys
import urllib.error
import urllib.request
import zipfile
from typing import Any

# Contexts we care about. Order = display order in the report table.
CONTEXTS = [
    ("lab-ci / unit-tests", "🧪", "Unit tests"),
    ("lab-ci / cat8kv",     "🖥️",  "Cat8kv (virtual)"),
    ("lab-ci / cat9k",      "🛰️",  "Cat9k (physical)"),
]

LAB_EVIDENCE_ARTIFACTS = {
    "lab-ci / cat8kv": "argo-evidence-cat8kv",
    "lab-ci / cat9k": "argo-evidence-cat9k",
}

CAT8KV_DEVICE_HOSTS = {
    "15": "192.0.2.55",
    "16": "192.0.2.56",
    "17": "192.0.2.57",
    "18": "192.0.2.58",
    "19": "192.0.2.59",
    "20": "192.0.2.60",
}

# Current Cat8kv sharding in cvk-gitops/cat8kv-pr-test-template.yaml.
# Used only for report presentation and as a fallback for legacy marker
# logs that do not carry Argo node input parameters.
CAT8KV_SCENARIO_DEVICE = {
    "primary": "17",
    "restart-resilience": "17",
    "multi-container": "17",
    "concurrent-pods": "18",
    "iosxeconfig-rev": "19",
    "tenancy": "19",
    "show-command": "16",
    "allowlist": "16",
    "output-spill": "16",
    "clobber-existing": "16",
    "subscribe": "20",
    "concurrent-stress": "20",
}

CAT8KV_SHARD_LABEL = {
    "15": "excluded/17.9.8",
    "16": "device-ops",
    "17": "primary/restart/multi",
    "18": "concurrent-pods",
    "19": "config/tenancy",
    "20": "telemetry/stress",
}

ADVISORY_SCENARIOS = {"concurrent-stress"}

STATE_EMOJI = {
    "success": "✅",
    "failure": "❌",
    "error":   "❌",
    "pending": "⏳",
}

GITHUB_API = "https://api.github.com"

# Go package path → CRD identifier. Used to bucket gotestsum output
# into the per-CRD table on the upstream PR comment.
#
# Edit this list when a new CRD or a new test package lands; missing
# packages fall through to the "Other / shared code" bucket so they're
# still counted but don't pollute the per-CRD breakdown.
PACKAGE_TO_CRD: dict[str, str] = {
    # CRDs implemented by their own controller package
    "github.com/cisco/virtual-kubelet-cisco/internal/controller":                              "CiscoDevice",
    "github.com/cisco/virtual-kubelet-cisco/internal/aggregator":                              "CiscoDevice",
    "github.com/cisco/virtual-kubelet-cisco/internal/provider":                                "CiscoDevice",
    "github.com/cisco/virtual-kubelet-cisco/internal/provider/deviceoperation":                "DeviceOperation",
    "github.com/cisco/virtual-kubelet-cisco/internal/provider/diagnostic":                     "IOSXEDiagnostic",
    "github.com/cisco/virtual-kubelet-cisco/internal/provider/diagnostic/adminserver":         "IOSXEDiagnostic",
    "github.com/cisco/virtual-kubelet-cisco/internal/provider/softwareupgrade":                "IOSXESoftwareUpgrade",
    "github.com/cisco/virtual-kubelet-cisco/internal/provider/operationalaction":              "IOSXEOperationalAction",
    # Driver / transport packages — bucket against the CRD whose
    # reconciler exercises them most. Operators viewing the report
    # want CRD-level signal, not "transport-X passes".
    "github.com/cisco/virtual-kubelet-cisco/internal/drivers":                                  "CiscoDevice",
    "github.com/cisco/virtual-kubelet-cisco/internal/drivers/common":                          "CiscoDevice",
    "github.com/cisco/virtual-kubelet-cisco/internal/drivers/iosxe":                           "CiscoDevice",
    "github.com/cisco/virtual-kubelet-cisco/internal/drivers/iosxe/configdriver":              "IOSXEConfig",
    "github.com/cisco/virtual-kubelet-cisco/internal/drivers/iosxe/configdriver/engine":       "IOSXEConfig",
    "github.com/cisco/virtual-kubelet-cisco/internal/drivers/iosxe/configdriver/intent":       "IOSXEConfig",
    "github.com/cisco/virtual-kubelet-cisco/internal/drivers/iosxe/configdriver/schema":       "IOSXEConfig",
    "github.com/cisco/virtual-kubelet-cisco/internal/drivers/iosxe/configdriver/transport":    "IOSXEConfig",
    "github.com/cisco/virtual-kubelet-cisco/internal/drivers/iosxe/configdriver/writers":      "IOSXEConfig",
    "github.com/cisco/virtual-kubelet-cisco/internal/drivers/iosxe/telemetry":                 "IOSXETelemetry",
    "github.com/cisco/virtual-kubelet-cisco/internal/drivers/iosxe/devicegrpc":                "(shared) gRPC pool",
    "github.com/cisco/virtual-kubelet-cisco/internal/drivers/iosxe/gnoi":                      "(shared) gNOI client",
    # Telemetry / mapper packages — pre-PR shape.
    "github.com/cisco/virtual-kubelet-cisco/internal/telemetry/classifier":                    "IOSXETelemetry",
    "github.com/cisco/virtual-kubelet-cisco/internal/telemetry/correlation":                   "IOSXETelemetry",
    "github.com/cisco/virtual-kubelet-cisco/internal/telemetry/emit":                          "IOSXETelemetry",
    "github.com/cisco/virtual-kubelet-cisco/internal/telemetry/mapper":                        "IOSXETelemetry",
    "github.com/cisco/virtual-kubelet-cisco/internal/telemetry/state":                         "IOSXETelemetry",
    "github.com/cisco/virtual-kubelet-cisco/internal/telemetry/yang":                          "IOSXETelemetry",
}

# Stable display order for the per-CRD table.
CRD_ORDER = [
    "CiscoDevice",
    "IOSXEConfig",
    "IOSXEConfigBundle",
    "IOSXEConfigDefaults",
    "IOSXEConfigRevision",
    "IOSXEConfigApplyLog",
    "IOSXEDiagnostic",
    "IOSXETelemetry",
    "IOSXEDeviceGroupConfig",
    "IOSXEInterfaceGroupConfig",
    "IOSXETemplate",
    "DeviceOperation",
    "IOSXESoftwareUpgrade",
    "IOSXEOperationalAction",
    "(shared) gRPC pool",
    "(shared) gNOI client",
    "Other",
]

# Single-line marker the lab Argo scenarios emit. See
# cvk-gitops/environments/lab/cvk-cicd/cat{8kv,9k}-pr-test-template.yaml.
SCENARIO_MARKER_RE = re.compile(
    r"::cvk-scenario:: crd=(?P<crd>\S+) name=(?P<name>\S+) phase=(?P<phase>\S+)"
    r"(?:\s+duration=(?P<duration>\d+))?"
    r"(?P<extra>[^\n]*)"
)


def warn(msg: str) -> None:
    sys.stderr.write(f"::warning::post-pr-report: {msg}\n")


def api(path: str, method: str = "GET", body: dict | None = None, token: str | None = None) -> Any:
    """Minimal GitHub API call. Returns parsed JSON (or None on 204).

    `token` overrides GH_TOKEN for fork-internal reads such as
    workflow logs and artefacts, which need actions:read permission
    that the upstream-write PAT may not have.
    """
    url = path if path.startswith("http") else f"{GITHUB_API}{path}"
    data = json.dumps(body).encode() if body is not None else None
    req = urllib.request.Request(url, data=data, method=method)
    req.add_header("Accept", "application/vnd.github+json")
    req.add_header("Authorization", f"Bearer {token or os.environ['GH_TOKEN']}")
    req.add_header("X-GitHub-Api-Version", "2022-11-28")
    if data:
        req.add_header("Content-Type", "application/json")
    with urllib.request.urlopen(req, timeout=30) as resp:
        raw = resp.read()
        return json.loads(raw) if raw else None


def fmt_duration(started: str | None, completed: str | None) -> str:
    if not started or not completed:
        return ""
    from datetime import datetime
    s = datetime.fromisoformat(started.replace("Z", "+00:00"))
    e = datetime.fromisoformat(completed.replace("Z", "+00:00"))
    secs = int((e - s).total_seconds())
    if secs < 60:
        return f"{secs}s"
    return f"{secs // 60}m{secs % 60:02d}s"


def latest_status_per_context(repo: str, sha: str) -> dict[str, dict]:
    """Return {context: latest_status_json} for our CONTEXTS."""
    # Statuses API returns most recent first.
    statuses = api(f"/repos/{repo}/commits/{sha}/statuses?per_page=100") or []
    out: dict[str, dict] = {}
    for s in statuses:
        ctx = s.get("context", "")
        if ctx not in out and any(ctx == c for c, _, _ in CONTEXTS):
            out[ctx] = s
    return out


def run_id_from_target_url(url: str) -> int | None:
    m = re.search(r"/actions/runs/(\d+)", url or "")
    return int(m.group(1)) if m else None


def fetch_steps(fork_repo: str, run_id: int) -> list[dict]:
    """Steps from the first (and only) job of a workflow run."""
    try:
        data = api(
            f"/repos/{fork_repo}/actions/runs/{run_id}/jobs",
            token=_fork_read_token(),
        ) or {}
    except urllib.error.HTTPError as e:
        warn(f"fetch jobs {run_id}: {e}")
        return []
    jobs = data.get("jobs", [])
    if not jobs:
        return []
    return jobs[0].get("steps", [])


def _fork_read_token() -> str:
    """Token used for fork-internal read APIs."""
    return os.environ.get("FORK_READ_TOKEN") or os.environ["GH_TOKEN"]


def http_get_bytes(url: str, token: str | None = None) -> bytes | None:
    """Authenticated GET returning raw response body. None on any error."""
    try:
        req = urllib.request.Request(url, method="GET")
        req.add_header("Accept", "application/vnd.github+json")
        req.add_header("Authorization", f"Bearer {token or os.environ['GH_TOKEN']}")
        req.add_header("X-GitHub-Api-Version", "2022-11-28")
        with urllib.request.urlopen(req, timeout=60) as resp:
            return resp.read()
    except (urllib.error.HTTPError, urllib.error.URLError) as e:
        warn(f"http_get_bytes {url}: {e}")
        return None


def fetch_job_log_text(fork_repo: str, run_id: int) -> str:
    """Concatenated stdout of every job in the run.

    Used to grep ::cvk-scenario:: markers out of the Argo workflow
    output without depending on the artefact upload step (which the
    lab-ci workflows only do on failure today).
    """
    token = _fork_read_token()
    try:
        data = api(f"/repos/{fork_repo}/actions/runs/{run_id}/jobs", token=token) or {}
    except urllib.error.HTTPError as e:
        warn(f"fetch jobs {run_id}: {e}")
        return ""
    pieces: list[str] = []
    for job in data.get("jobs", []):
        job_id = job.get("id")
        if not job_id:
            continue
        body = http_get_bytes(
            f"{GITHUB_API}/repos/{fork_repo}/actions/jobs/{job_id}/logs",
            token=token,
        )
        if body:
            pieces.append(body.decode("utf-8", errors="replace"))
    return "\n".join(pieces)


def fetch_artefact_file(fork_repo: str, run_id: int, artefact_name: str, file_in_zip: str) -> bytes | None:
    """Download a single file out of a named workflow artefact zip.

    The GitHub artefacts API returns a redirect to a temporary signed
    URL; urllib follows it automatically when authenticated. Returns
    None on any failure — the report degrades gracefully.
    """
    token = _fork_read_token()
    try:
        listing = api(f"/repos/{fork_repo}/actions/runs/{run_id}/artifacts", token=token) or {}
    except urllib.error.HTTPError as e:
        warn(f"list artefacts {run_id}: {e}")
        return None
    for art in listing.get("artifacts", []):
        if art.get("name") != artefact_name:
            continue
        zip_bytes = http_get_bytes(art.get("archive_download_url", ""), token=token)
        if not zip_bytes:
            return None
        try:
            with zipfile.ZipFile(io.BytesIO(zip_bytes)) as z:
                with z.open(file_in_zip) as f:
                    return f.read()
        except (zipfile.BadZipFile, KeyError) as e:
            warn(f"artefact {artefact_name} missing {file_in_zip}: {e}")
            return None
    return None


def parse_gotestsum_json(content: bytes) -> dict[str, dict[str, int]]:
    """Parse gotestsum's JSON event stream into per-package counts.

    Each line is one event with Action ∈ {run,pass,fail,skip,output,...}.
    We use the package-level summary events (Test field absent / empty)
    to derive {package: {pass, fail, skip, total}}. Falls back to
    counting individual test events when no package summary appears.
    """
    stats: dict[str, dict[str, int]] = {}
    by_test: dict[tuple[str, str], str] = {}
    for line in content.splitlines():
        line = line.strip()
        if not line or not line.startswith(b"{"):
            continue
        try:
            ev = json.loads(line)
        except json.JSONDecodeError:
            continue
        pkg = ev.get("Package", "")
        action = ev.get("Action", "")
        test = ev.get("Test", "")
        if not pkg:
            continue
        if test:
            if action in ("pass", "fail", "skip"):
                by_test[(pkg, test)] = action
        else:
            # Package summary
            if action in ("pass", "fail", "skip"):
                stats.setdefault(pkg, {"pass": 0, "fail": 0, "skip": 0})
                # Aggregate from individual test events for the count;
                # the package-summary's Action alone tells us the
                # pass/fail of the package, not the count of tests.
    # Roll up per-test outcomes.
    for (pkg, _test), outcome in by_test.items():
        s = stats.setdefault(pkg, {"pass": 0, "fail": 0, "skip": 0})
        s[outcome] = s.get(outcome, 0) + 1
    return stats


def parse_marker_extras(extra: str) -> dict[str, str]:
    """Parse key=value tokens appended to ::cvk-scenario:: markers."""
    out: dict[str, str] = {}
    for token in extra.split():
        if "=" not in token:
            continue
        key, value = token.split("=", 1)
        if key:
            out[key] = value
    return out


def _truthy(value: Any) -> bool:
    if isinstance(value, bool):
        return value
    return str(value).lower() in {"1", "true", "yes", "y", "on"}


def is_advisory_scenario(row: dict[str, Any]) -> bool:
    return _truthy(row.get("advisory")) or row.get("name") in ADVISORY_SCENARIOS


def parse_scenario_markers(text: str) -> dict[tuple[str, ...], dict[str, Any]]:
    """Extract the latest phase per (crd, scenario-name) tuple.

    Each Argo scenario emits two markers (running + final). We keep
    whichever is most recent in the log, so the end-of-run state wins.
    """
    out: dict[tuple[str, ...], dict[str, Any]] = {}
    for m in SCENARIO_MARKER_RE.finditer(text):
        extras = parse_marker_extras(m.group("extra") or "")
        key = (m.group("crd"), m.group("name"))
        out[key] = {
            "crd": m.group("crd"),
            "name": m.group("name"),
            "phase": m.group("phase"),
            "duration": int(m.group("duration") or 0),
            "advisory": _truthy(extras.get("advisory", False)),
            "rc": extras.get("rc", ""),
            "source": "job-log-marker",
        }
    return out


def _duration_seconds(started: str | None, completed: str | None) -> int:
    if not started or not completed:
        return 0
    from datetime import datetime
    s = datetime.fromisoformat(started.replace("Z", "+00:00"))
    e = datetime.fromisoformat(completed.replace("Z", "+00:00"))
    return max(0, int((e - s).total_seconds()))


def _node_parameter_map(node: dict[str, Any]) -> dict[str, str]:
    params = node.get("inputs", {}).get("parameters", []) or []
    out: dict[str, str] = {}
    if isinstance(params, dict):
        for key, value in params.items():
            out[str(key)] = str(value)
        return out
    for p in params:
        if not isinstance(p, dict):
            continue
        name = p.get("name")
        if not name:
            continue
        out[str(name)] = str(p.get("value", ""))
    return out


def _device_id_from_text(*values: str) -> str:
    for value in values:
        m = re.search(r"cat8kv-(\d{2})", value or "")
        if m:
            return m.group(1)
    return ""


def parse_argo_scenarios(content: bytes) -> dict[tuple[str, ...], dict[str, Any]]:
    """Extract scenario status directly from an Argo workflow JSON dump."""
    try:
        workflow = json.loads(content)
    except json.JSONDecodeError as e:
        warn(f"parse argo workflow json: {e}")
        return {}

    templates = (
        workflow.get("status", {})
        .get("storedWorkflowTemplateSpec", {})
        .get("templates", [])
    )
    labels_by_template: dict[str, dict[str, str]] = {}
    for tmpl in templates:
        name = tmpl.get("name")
        if name:
            labels_by_template[name] = tmpl.get("metadata", {}).get("labels", {}) or {}

    out: dict[tuple[str, ...], dict[str, Any]] = {}
    nodes = workflow.get("status", {}).get("nodes", {}) or {}
    for node in nodes.values():
        if node.get("type") != "Pod":
            continue
        template_name = node.get("templateName") or ""
        labels = labels_by_template.get(template_name, {})
        scenario_name = labels.get("cvk.cisco.io/scenario-name")
        crd = labels.get("cvk.cisco.io/crd")
        if not scenario_name and not template_name.startswith("scenario-"):
            continue

        display_name = node.get("displayName") or node.get("name") or template_name
        scenario_name = scenario_name or display_name
        crd = crd or "Lab"
        phase = (node.get("phase") or "unknown").lower()
        params = _node_parameter_map(node)
        device_id = params.get("device_id") or _device_id_from_text(display_name, node.get("name", ""))
        device_host = params.get("device_host") or CAT8KV_DEVICE_HOSTS.get(device_id, "")
        key = (crd, scenario_name, device_id or display_name)
        candidate = {
            "crd": crd,
            "name": scenario_name,
            "phase": phase,
            "duration": _duration_seconds(node.get("startedAt"), node.get("finishedAt")),
            "template": template_name,
            "displayName": display_name,
            "device_id": device_id,
            "device_host": device_host,
            "message": node.get("message", ""),
            "startedAt": node.get("startedAt", ""),
            "advisory": scenario_name in ADVISORY_SCENARIOS,
            "source": "argo-workflow",
        }

        previous = out.get(key)
        if not previous or candidate["startedAt"] >= previous.get("startedAt", ""):
            out[key] = candidate
    return out


def annotate_lab_scenarios(
    ctx: str,
    scenarios: dict[tuple[str, ...], dict[str, Any]],
) -> dict[tuple[str, ...], dict[str, Any]]:
    """Attach lab-specific device/shard metadata used by the PR report."""
    annotated: dict[tuple[str, ...], dict[str, Any]] = {}
    for row in scenarios.values():
        row = dict(row)
        name = row.get("name", "")
        if ctx == "lab-ci / cat8kv":
            device_id = row.get("device_id") or CAT8KV_SCENARIO_DEVICE.get(name, "")
            host = row.get("device_host") or CAT8KV_DEVICE_HOSTS.get(device_id, "")
            row["device_id"] = device_id
            row["device_host"] = host
            row["device"] = f"cat8kv-{device_id}" if device_id else "cat8kv"
            row["shard"] = CAT8KV_SHARD_LABEL.get(device_id, "")
        elif ctx == "lab-ci / cat9k":
            row["device"] = "cat9k"
            row["device_host"] = "198.51.100.102"
            row["shard"] = "physical"

        if name in ADVISORY_SCENARIOS:
            row["advisory"] = True

        key = (row.get("crd", "Lab"), row.get("name", ""), row.get("device", ""))
        annotated[key] = row
    return annotated


def crd_for_package(pkg: str) -> str:
    """Bucket a Go package path into a CRD label, with longest-prefix match."""
    best = ""
    for candidate in PACKAGE_TO_CRD:
        if pkg == candidate or pkg.startswith(candidate + "/"):
            if len(candidate) > len(best):
                best = candidate
    return PACKAGE_TO_CRD.get(best, "Other") if best else "Other"


def render_crd_coverage(
    unit_stats: dict[str, dict[str, int]],
    cat8kv_markers: dict[tuple[str, ...], dict[str, Any]],
    cat9k_markers: dict[tuple[str, ...], dict[str, Any]],
) -> list[str]:
    """Produce the '### Coverage by CRD' section as a list of md lines."""
    # Unit-test aggregation: package counts → CRD totals.
    unit: dict[str, dict[str, int]] = {}
    for pkg, c in unit_stats.items():
        crd = crd_for_package(pkg)
        u = unit.setdefault(crd, {"pass": 0, "fail": 0, "skip": 0})
        u["pass"] += c.get("pass", 0)
        u["fail"] += c.get("fail", 0)
        u["skip"] += c.get("skip", 0)

    # Scenario aggregation: count per (crd, source).
    def bucket_scenarios(markers: dict[tuple[str, ...], dict[str, Any]]) -> dict[str, dict[str, int]]:
        b: dict[str, dict[str, int]] = {}
        for v in markers.values():
            if is_advisory_scenario(v):
                continue
            crd = v["crd"]
            phase = v["phase"]
            s = b.setdefault(crd, {"pass": 0, "fail": 0})
            if phase == "succeeded":
                s["pass"] += 1
            elif phase == "failed":
                s["fail"] += 1
            # phase=running implies the trap didn't fire → treat as failed
            else:
                s["fail"] += 1
        return b

    cat8kv = bucket_scenarios(cat8kv_markers)
    cat9k = bucket_scenarios(cat9k_markers)

    # Union of CRDs that appear in any bucket, ordered by CRD_ORDER.
    seen = set(unit) | set(cat8kv) | set(cat9k)
    ordered = [c for c in CRD_ORDER if c in seen] + sorted(seen - set(CRD_ORDER))

    if not ordered:
        return []  # nothing to render yet

    lines: list[str] = []
    lines.append("")
    lines.append("### Coverage by CRD")
    lines.append("")
    lines.append("| CRD | Unit | Cat8kv | Cat9k |")
    lines.append("| :--- | :---: | :---: | :---: |")
    for crd in ordered:
        u = unit.get(crd, {"pass": 0, "fail": 0, "skip": 0})
        c8 = cat8kv.get(crd, {"pass": 0, "fail": 0})
        c9 = cat9k.get(crd, {"pass": 0, "fail": 0})
        def cell(c: dict[str, int]) -> str:
            p, f = c.get("pass", 0), c.get("fail", 0)
            tot = p + f
            if tot == 0:
                return "—"
            mark = "✅" if f == 0 else "❌"
            return f"{p}/{tot} {mark}"
        lines.append(f"| `{crd}` | {cell(u)} | {cell(c8)} | {cell(c9)} |")
    lines.append("")
    return lines


def fetch_run_duration(fork_repo: str, run_id: int) -> str:
    """Format a workflow run's wall-clock duration.

    The commit-status's created_at/updated_at are both stamped at the moment
    the status is posted (single API call at workflow completion), so they
    can't be subtracted to get a meaningful duration. The actual run
    timing lives on the workflow run object as run_started_at → updated_at.
    """
    try:
        run = api(
            f"/repos/{fork_repo}/actions/runs/{run_id}",
            token=_fork_read_token(),
        ) or {}
    except urllib.error.HTTPError as e:
        warn(f"fetch run {run_id}: {e}")
        return ""
    return fmt_duration(run.get("run_started_at"), run.get("updated_at"))


def scenario_results_for(
    ctx: str,
    latest: dict[str, dict],
    fork_repo: str,
) -> dict[tuple[str, ...], dict[str, Any]]:
    """Return lab scenario results for a context.

    New lab workflows upload Argo workflow JSON on both success and
    failure. Prefer that durable source, then fall back to log markers
    for older runs.
    """
    s = latest.get(ctx)
    if not s:
        return {}
    run_id = run_id_from_target_url(s.get("target_url", ""))
    if not run_id:
        return {}

    artifact_name = LAB_EVIDENCE_ARTIFACTS.get(ctx)
    if artifact_name:
        content = fetch_artefact_file(fork_repo, run_id, artifact_name, "argo-workflow.json")
        if content:
            scenarios = parse_argo_scenarios(content)
            if scenarios:
                return annotate_lab_scenarios(ctx, scenarios)

    return annotate_lab_scenarios(ctx, parse_scenario_markers(fetch_job_log_text(fork_repo, run_id)))


def render_lab_scenarios(scenarios: dict[tuple[str, ...], dict[str, Any]]) -> list[str]:
    if not scenarios:
        return []

    rows = sorted(
        scenarios.values(),
        key=lambda s: (
            s.get("startedAt", ""),
            s.get("device", ""),
            s.get("crd", ""),
            s.get("name", ""),
        ),
    )
    lines: list[str] = []
    lines.append("")
    lines.append("**Argo scenarios**")
    lines.append("")
    lines.append("| Device | CRD | Scenario | Shard | Template | Status | Duration |")
    lines.append("| :--- | :--- | :--- | :--- | :--- | :---: | :---: |")
    for row in rows:
        phase = row.get("phase", "unknown")
        icon = {
            "succeeded": "✅",
            "failed": "❌",
            "error": "❌",
            "running": "⏳",
            "pending": "⏳",
            "skipped": "⏭️",
        }.get(phase, "❔")
        duration = row.get("duration", 0)
        msg = (row.get("message") or "").replace("|", "\\|")
        device = row.get("device") or "—"
        host = row.get("device_host") or ""
        device_cell = f"`{device}`"
        if host:
            device_cell += f"<br><sub>{host}</sub>"
        shard = row.get("shard") or "—"
        status = f"{icon} `{phase}`"
        if is_advisory_scenario(row):
            status += "<br><sub>advisory</sub>"
        if msg:
            status += f"<br><sub>{msg}</sub>"
        lines.append(
            f"| {device_cell} | `{row.get('crd', '-')}` | `{row.get('name', '-')}` | "
            f"{shard} | `{row.get('template', '-')}` | {status} | {duration}s |"
        )
    return lines


def render_report(
    upstream_repo: str,
    upstream_sha: str,
    upstream_pr: str,
    fork_repo: str,
    latest: dict[str, dict],
) -> str:
    sha_short = upstream_sha[:12]
    lines: list[str] = []
    lines.append(f"<!-- lab-ci-report:{upstream_sha} -->")
    lines.append(f"## 🤖 Lab CI Report")
    lines.append("")
    lines.append(
        f"Commit `{sha_short}` · validated on lab hardware via "
        f"[`{fork_repo}`](https://github.com/{fork_repo})."
    )
    lines.append("")

    # Summary table
    lines.append("| Check | Result | Duration | Details |")
    lines.append("| :--- | :---: | :---: | :--- |")
    total_pass = total_fail = total_pending = 0
    for ctx, emoji, label in CONTEXTS:
        s = latest.get(ctx)
        if not s:
            state = "pending"
            duration = "—"
            link = "—"
            total_pending += 1
        else:
            state = s.get("state", "pending")
            if state == "success":
                total_pass += 1
            elif state in ("failure", "error"):
                total_fail += 1
            else:
                total_pending += 1
            tgt = s.get("target_url") or ""
            run_id = run_id_from_target_url(tgt)
            duration = fetch_run_duration(fork_repo, run_id) if run_id else ""
            link = f"[logs]({tgt})" if tgt else "—"
        verdict_emoji = STATE_EMOJI.get(state, "❔")
        lines.append(f"| {emoji} {label} | {verdict_emoji} `{state}` | {duration or '—'} | {link} |")

    lines.append("")
    totals = []
    if total_pass:
        totals.append(f"**{total_pass} passed** ✅")
    if total_fail:
        totals.append(f"**{total_fail} failed** ❌")
    if total_pending:
        totals.append(f"**{total_pending} pending** ⏳")
    lines.append("**Summary:** " + " · ".join(totals) if totals else "**Summary:** no results yet")

    # CRD-coverage section. Each block degrades to "—" cells when its
    # source data isn't available yet (e.g. unit-tests run before
    # gotestsum artefact uploads, Cat9k run before Argo scenario logs
    # are emitted).
    unit_stats: dict[str, dict[str, int]] = {}
    unit_run_id = None
    if (s := latest.get("lab-ci / unit-tests")):
        unit_run_id = run_id_from_target_url(s.get("target_url", ""))
    if unit_run_id:
        content = fetch_artefact_file(
            fork_repo, unit_run_id, "unit-test-results", "test-output.json"
        )
        if content:
            unit_stats = parse_gotestsum_json(content)

    lab_scenarios = {
        ctx: scenario_results_for(ctx, latest, fork_repo)
        for ctx in LAB_EVIDENCE_ARTIFACTS
    }
    cat8kv_markers = lab_scenarios.get("lab-ci / cat8kv", {})
    cat9k_markers = lab_scenarios.get("lab-ci / cat9k", {})

    lines.extend(render_crd_coverage(unit_stats, cat8kv_markers, cat9k_markers))

    # Per-workflow step breakdown
    for ctx, emoji, label in CONTEXTS:
        s = latest.get(ctx)
        if not s:
            continue
        run_id = run_id_from_target_url(s.get("target_url", ""))
        if not run_id:
            continue
        steps = fetch_steps(fork_repo, run_id)
        scenarios = lab_scenarios.get(ctx, {})
        # Filter out skipped post-action steps to keep the table compact.
        visible = [st for st in steps if st.get("conclusion") != "skipped" or st.get("status") == "in_progress"]
        if not visible and not scenarios:
            continue
        pass_n = sum(1 for st in visible if st.get("conclusion") == "success")
        fail_n = sum(1 for st in visible if st.get("conclusion") == "failure")
        scen_gate = [sc for sc in scenarios.values() if not is_advisory_scenario(sc)]
        scen_pass_n = sum(1 for sc in scen_gate if sc.get("phase") == "succeeded")
        scen_fail_n = sum(1 for sc in scen_gate if sc.get("phase") not in ("succeeded", "skipped"))
        scen_advisory_n = sum(1 for sc in scenarios.values() if is_advisory_scenario(sc))
        state = s.get("state", "pending")
        hdr_emoji = STATE_EMOJI.get(state, "❔")
        scenario_text = ""
        if scenarios:
            scenario_text = f", {scen_pass_n} scenarios passed, {scen_fail_n} failed"
            if scen_advisory_n:
                scenario_text += f", {scen_advisory_n} advisory"
        lines.append(
            f"<details><summary>{emoji} {label} — {hdr_emoji} "
            f"{pass_n} wrapper steps passed, {fail_n} failed{scenario_text}</summary>\n"
        )
        lines.extend(render_lab_scenarios(scenarios))
        if ctx in LAB_EVIDENCE_ARTIFACTS and not scenarios:
            lines.append("")
            lines.append("_No Argo scenario evidence was available for this run._")
        if visible:
            lines.append("")
            lines.append("**GitHub wrapper steps**")
            lines.append("")
            lines.append("| # | Step | Status | Duration |")
            lines.append("| ---: | :--- | :---: | :---: |")
            for i, st in enumerate(visible, 1):
                name = st.get("name", "?").replace("|", "\\|")
                conclusion = st.get("conclusion") or st.get("status") or "?"
                step_emoji = {
                    "success": "✅", "failure": "❌", "skipped": "⏭️",
                    "cancelled": "🚫", "in_progress": "⏳",
                }.get(conclusion, "❔")
                dur = fmt_duration(st.get("started_at"), st.get("completed_at"))
                lines.append(f"| {i} | {name} | {step_emoji} | {dur or '—'} |")
        lines.append("\n</details>\n")

    lines.append("---")
    from datetime import datetime, timezone
    now = datetime.now(timezone.utc).strftime("%Y-%m-%d %H:%M UTC")
    lines.append(
        f"_Updated {now}. Report rebuilt from commit statuses on each run; "
        f"see individual workflow logs via the **logs** links above._"
    )
    return "\n".join(lines)


def upsert_comment(
    upstream_repo: str,
    upstream_pr: str,
    upstream_sha: str,
    body: str,
) -> None:
    marker = f"<!-- lab-ci-report:{upstream_sha} -->"
    # Find existing comment with our marker. Issue comments are paginated;
    # we iterate but for practical PRs one page (30) is enough.
    existing_id: int | None = None
    for page in range(1, 10):
        comments = api(
            f"/repos/{upstream_repo}/issues/{upstream_pr}/comments"
            f"?per_page=100&page={page}"
        ) or []
        if not comments:
            break
        for c in comments:
            if marker in (c.get("body") or ""):
                existing_id = c["id"]
                break
        if existing_id or len(comments) < 100:
            break

    if existing_id:
        api(
            f"/repos/{upstream_repo}/issues/comments/{existing_id}",
            method="PATCH",
            body={"body": body},
        )
    else:
        api(
            f"/repos/{upstream_repo}/issues/{upstream_pr}/comments",
            method="POST",
            body={"body": body},
        )


def main() -> int:
    required = ["UPSTREAM_REPO", "UPSTREAM_SHA", "UPSTREAM_PR", "FORK_REPO"]
    missing = [k for k in required if not os.environ.get(k)]
    if missing:
        warn(f"missing env: {missing}; skipping")
        return 0
    if not os.environ.get("GH_TOKEN"):
        warn("GH_TOKEN not set; skipping report")
        return 0

    upstream_repo = os.environ["UPSTREAM_REPO"]
    upstream_sha = os.environ["UPSTREAM_SHA"]
    upstream_pr = os.environ["UPSTREAM_PR"]
    fork_repo = os.environ["FORK_REPO"]

    try:
        latest = latest_status_per_context(upstream_repo, upstream_sha)
        body = render_report(upstream_repo, upstream_sha, upstream_pr, fork_repo, latest)
        upsert_comment(upstream_repo, upstream_pr, upstream_sha, body)
    except urllib.error.HTTPError as e:
        warn(f"API error {e.code}: {e.reason}")
        return 0
    except Exception as e:  # noqa: BLE001 — never fail the main check for reporting
        warn(f"{type(e).__name__}: {e}")
        return 0

    return 0


if __name__ == "__main__":
    sys.exit(main())
