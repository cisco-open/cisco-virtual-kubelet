#!/usr/bin/env python3
"""Run a serial, explicitly targeted gNOI round trip and retain its evidence.

Uses the current kubectl credentials. It never reads Kubernetes Secrets or
retries a mutation submission. A failure stops the remaining devices/direction.
"""
# Copyright 2026 Cisco Systems Inc.
# SPDX-License-Identifier: Apache-2.0

import argparse
import datetime as dt
import json
import os
from pathlib import Path
import re
import subprocess
import sys
import time
import uuid
from urllib.parse import urlsplit


TERMINAL = {"Succeeded", "Failed", "PreflightFailed", "ValidationFailed",
            "RolledBack", "RebootTimeout", "Cancelled", "StagedForNextBoot"}
HEALTH_COMMANDS = ["show version", "show install summary", "show gnxi state detail",
                   "show platform software status control-processor brief",
                   "show app-hosting list"]


def matches(actual, requested):
    return actual == requested or actual.startswith(requested + ".")


def ready(obj):
    return any(c.get("type") == "Ready" and c.get("status") == "True"
               for c in obj.get("status", {}).get("conditions", []))


def maintenance_taint(obj):
    return any(taint.get("key") == "cisco.vk/device-maintenance" and taint.get("value") == "gnoi"
               and taint.get("effect") == "NoSchedule" for taint in obj.get("spec", {}).get("taints", []))


def verify_supervisors(result, version):
    if not matches(result.get("Version", ""), version) or result.get("ActivationFailMessage"):
        raise RuntimeError(f"OS.Verify did not prove {version}: {result}")
    standby = result.get("Standby", {})
    if result.get("IndividualSupervisorInstall") or standby.get("State") == "READY":
        if (standby.get("State") != "READY" or not matches(standby.get("Version", ""), version)
                or standby.get("ActivationFailMessage")):
            raise RuntimeError(f"standby supervisor did not prove {version}: {standby}")
    elif standby.get("State") not in (None, "NOT_REPORTED", "UNSUPPORTED", "NON_EXISTENT"):
        raise RuntimeError(f"standby supervisor availability is inconclusive: {standby}")


def platform_health(outputs, version, baseline=None):
    """Fail closed on unfamiliar standalone Catalyst CLI output or lost apps."""
    commands = {output.get("command"): output.get("output", "") for output in outputs}
    if len(outputs) != len(HEALTH_COMMANDS) or set(commands) != set(HEALTH_COMMANDS):
        raise RuntimeError("platform health evidence is incomplete")
    if any(output.get("err") or output.get("truncated") for output in outputs):
        raise RuntimeError("platform health evidence failed or was truncated")
    running = re.search(r"^Cisco IOS XE Software, Version\s+(\S+)", commands["show version"], re.MULTILINE)
    switches = re.findall(r"^\s*\*?\s*\d+\s+\d+\s+(C9\S+)\s+(\S+)\s+CAT9K\S*\s+(?:BUNDLE|INSTALL)\s*$",
                          commands["show version"], re.MULTILINE)
    serial = re.search(r"^System Serial Number\s*:\s*(\S+)", commands["show version"], re.MULTILINE)
    if not running or not matches(running[1], version) or len(switches) != 1 or not matches(switches[0][1], version) or not serial:
        raise RuntimeError("CLI does not prove one standalone Catalyst switch on the requested version")
    installed = re.findall(r"^IMG\s+(\S+)\s+(\S+)", commands["show install summary"], re.MULTILINE)
    committed = [image for state, image in installed if state == "C"]
    unsettled = re.search(r"^[A-Z][A-Z0-9_-]*\s+[UD]\s+\S+", commands["show install summary"], re.MULTILINE)
    if (len(committed) != 1 or not matches(committed[0], version)
            or any(state not in ("I", "C") for state, _ in installed)
            or unsettled
            or not re.search(r"Auto abort timer:\s*inactive\b", commands["show install summary"], re.IGNORECASE)):
        raise RuntimeError("install summary does not prove a committed target with inactive abort timer")
    gnxi = commands["show gnxi state detail"]
    os_service = re.search(r"OS Image service\s*\n\s*-+\s*\n([^\n]*(?:\n[ \t]+[^\n]+)*)", gnxi)
    if (not re.search(r"State\s*:\s*Provisioned\b", gnxi, re.IGNORECASE)
            or not re.search(r"Secure server:\s*Enabled\b", gnxi)
            or not re.search(r"Secure password authentication:\s*Enabled\b", gnxi)
            or not os_service
            or any(not re.search(pattern, os_service[1]) for pattern in
                   (r"Admin state:\s*Enabled\b", r"Oper status:\s*Up\b", r"Supported:\s*Supported\b"))):
        raise RuntimeError("gNXI does not prove provisioned secure password-auth OS service health")
    processors = commands["show platform software status control-processor brief"]
    slots = []
    for heading, ending in ((r"Load Average", r"Memory"), (r"Memory \([^\n]+\)", r"CPU Utilization")):
        section = re.search(r"(?ms)^" + heading + r"\s*\n(.*?)(?=^" + ending + r"|\Z)", processors)
        rows = re.findall(r"^\s*(\d+-RP\d+)\s+(\S+)\s+", section[1], re.MULTILINE) if section else []
        if len(rows) != 1 or rows[0][1] != "Healthy":
            raise RuntimeError("control-processor load/memory health is not conclusively Healthy")
        slots.append(rows[0][0])
    if slots[0] != slots[1]:
        raise RuntimeError("control-processor health sections identify different processors")
    apps_text = commands["show app-hosting list"].strip()
    apps = {}
    if not re.fullmatch(r"No (?:App|apps?)(?:lications?)? (?:found|configured)\.?", apps_text, re.IGNORECASE):
        if not re.search(r"App(?:lication)?\s+id\s+State", apps_text, re.IGNORECASE):
            raise RuntimeError("app-hosting inventory format is inconclusive")
        for line in apps_text.splitlines():
            if not line.strip() or re.fullmatch(r"[\s-]+", line) or re.search(r"App(?:lication)?\s+id\s+State", line, re.IGNORECASE):
                continue
            columns = line.split()
            if len(columns) != 2 or columns[1] != "RUNNING" or columns[0] in apps:
                raise RuntimeError("hosted application is not RUNNING or its inventory is inconclusive")
            apps[columns[0]] = columns[1]
    health = {"serial": serial[1], "model": switches[0][0], "committedVersion": committed[0],
              "controlProcessor": slots[0], "applications": apps}
    if baseline is not None and any(health[key] != baseline[key] for key in ("serial", "model", "controlProcessor", "applications")):
        raise RuntimeError("device identity, processor, or hosted application inventory changed from baseline")
    return health


def metadata(obj):
    return {key: value for key, value in obj.get("metadata", {}).items()
            if key in ("name", "namespace", "uid", "generation", "resourceVersion", "creationTimestamp")}


def observation_timeout(spec):
    # Source resolution + install, initial activation client recovery + reboot
    # + rollback each have independent bounded windows, plus polling/API slack.
    return 2 * spec["installTimeoutSeconds"] + 3 * spec["rebootTimeoutSeconds"] + 600


def verify_worker_revision(output, revision):
    commit = re.search(r"\(commit=([0-9a-f]{40}), built=", output)
    if not output.startswith("cisco-vk ") or not commit or commit[1] != revision:
        raise RuntimeError("worker binary does not prove the full candidate Git revision")


class Cycle:
    def __init__(self, args):
        self.args = args
        self.directory = Path(args.evidence_dir)
        self.directory.mkdir(mode=0o700, parents=True, exist_ok=False)
        self.started = dt.datetime.now(dt.timezone.utc).isoformat()
        self.prefix = "cycle-" + dt.datetime.now(dt.timezone.utc).strftime("%Y%m%d%H%M%S") + "-" + uuid.uuid4().hex[:6]
        self.command = ["kubectl", "--context", args.context, "--request-timeout=30s", "-n", args.namespace]
        self.baselines = {}
        self.worker_identities = {}
        self.maintenance_observed = set()
        self.log = self.directory / "report.md"
        self.log.write_text(f"# CVK gNOI lab round trip\n\nStarted: {self.started}\n\n"
                            f"Candidate revision: `{args.revision}`\n\nExpected image: `{args.image}`\n\n"
                            f"Context: `{args.context}`; namespace: `{args.namespace}`\n\n"
                            "Each device is verified before moving to the next. JSON files are the exact Kubernetes manifests and results. "
                            "Automatic acceptance covers standalone Catalyst software commitment, secure gNOI, control-processor load/memory, "
                            "Kubernetes readiness, and unchanged RUNNING hosted applications. Unfamiliar or inconclusive output stops progression. "
                            "Forwarding, routing adjacencies, and application traffic are not tested by this lab harness.\n")

    def note(self, message):
        print(message, flush=True)
        with self.log.open("a") as output:
            output.write(f"\n{dt.datetime.now(dt.timezone.utc).isoformat()} — {message}\n")

    def save(self, name, value):
        (self.directory / name).write_text(value if isinstance(value, str) else json.dumps(value, indent=2) + "\n")

    def kubectl(self, *args, body=None, timeout=45):
        result = subprocess.run(self.command + list(args), input=body, text=True,
                                capture_output=True, timeout=timeout, check=False)
        if result.returncode:
            raise RuntimeError(f"kubectl {' '.join(args)} failed: {result.stderr.strip()}")
        return result.stdout

    def get(self, kind, name):
        return json.loads(self.kubectl("get", kind, name, "-o", "json"))

    def observe_maintenance(self, device, label, phase, timeout=5):
        obj = json.loads(self.kubectl("get", "node", device, "-o", "json", "--request-timeout=5s", timeout=timeout))
        present = maintenance_taint(obj)
        with (self.directory / (label + ".maintenance.jsonl")).open("a") as history:
            history.write(json.dumps({"observedAt": dt.datetime.now(dt.timezone.utc).isoformat(),
                "phase": phase, "metadata": metadata(obj), "taints": obj.get("spec", {}).get("taints", []),
                "ownedMaintenanceTaintPresent": present}) + "\n")
        return present

    def wait_maintenance_clear(self, device, label):
        deadline = time.monotonic() + 45
        while True:
            remaining = deadline - time.monotonic()
            if remaining <= 0:
                raise RuntimeError(f"{device}: owned maintenance taint did not clear within 45 seconds")
            if not self.observe_maintenance(device, label, "settling", timeout=min(5, remaining)):
                return
            time.sleep(min(5, max(0, deadline - time.monotonic())))

    def submit(self, manifest):
        name = manifest["metadata"]["name"]
        self.save(name + ".manifest.json", manifest)
        self.note(f"Create `{manifest['kind']}/{name}`; manifest `{name}.manifest.json`.")
        # A timeout is ambiguous: let the caller inspect the persisted object.
        # Never repeat create, delete/recreate, or activate from this script.
        self.kubectl("create", "-f", "-", body=json.dumps(manifest))

    def wait(self, kind, name, timeout):
        deadline = time.monotonic() + timeout
        phase = None
        while time.monotonic() < deadline:
            obj = self.get(kind, name)
            self.save(name + ".result.json", obj)
            status = obj.get("status", {})
            with (self.directory / (name + ".observations.jsonl")).open("a") as history:
                history.write(json.dumps({"observedAt": dt.datetime.now(dt.timezone.utc).isoformat(),
                                          "metadata": metadata(obj), "status": status}) + "\n")
            if status.get("phase") != phase:
                phase = status.get("phase")
                self.note(f"`{name}`: `{phase}` — {status.get('message', '')}")
            if phase in TERMINAL:
                if phase != "Succeeded":
                    raise RuntimeError(f"{name}: {phase}/{status.get('failureReason', '')}; preserve the object and inspect device state")
                return obj
            if kind == "xeupgrade" and phase not in (None, "Pending", "Resolving"):
                device = obj.get("spec", {}).get("deviceRef", {}).get("name")
                if device:
                    try:
                        if self.observe_maintenance(device, name, phase) and name not in self.maintenance_observed:
                            self.maintenance_observed.add(name)
                            self.note(f"`{name}`: observed CVK's owned NoSchedule maintenance taint during `{phase}`.")
                    except Exception as error:
                        self.note(f"Maintenance observation unavailable for `{name}`: {error}")
            time.sleep(10)
        raise RuntimeError(f"observation deadline for {name}; device outcome may be unknown, do not resubmit")

    def probe(self, device, label, kind, commands=None):
        name = f"{self.prefix}-{device}-{label}"
        operation = {"kind": kind}
        if commands:
            operation["commands"] = commands
        manifest = {"apiVersion": "ops.cisco.vk/v1alpha1", "kind": "DeviceOperation",
                    "metadata": {"name": name, "namespace": self.args.namespace},
                    "spec": {"deviceRef": {"name": device}, "operation": operation}}
        self.submit(manifest)
        result = self.wait("deviceoperation", name, 300)
        outputs = result.get("status", {}).get("outputs", [])
        if not outputs or any(o.get("err") or o.get("truncated") for o in outputs):
            raise RuntimeError(f"{name}: missing, failed, or truncated health evidence")
        return outputs

    def verify(self, device, label, version):
        output = self.probe(device, label + "-verify", "GNOIOSVerify")
        result = json.loads(output[0]["output"])
        verify_supervisors(result, version)
        for kind in ("ciscodevice", "node"):
            obj = self.get(kind, device)
            spec = obj.get("spec", {})
            safe_spec = ({key: spec[key] for key in ("address", "driver") if key in spec}
                         if kind == "ciscodevice" else {"taints": spec.get("taints", [])})
            self.save(f"{device}.{label}.{kind}.json", {"metadata": metadata(obj), "spec": safe_spec, "status": obj.get("status", {})})
            is_ready = obj.get("status", {}).get("phase") == "Ready" if kind == "ciscodevice" else ready(obj)
            if not is_ready:
                raise RuntimeError(f"{kind}/{device} is not Ready")
        outputs = self.probe(device, label + "-health", "ShowCommand", HEALTH_COMMANDS)
        health = platform_health(outputs, version, self.baselines.get(device))
        self.save(f"{device}.{label}.accepted-health.json", health)
        self.wait_maintenance_clear(device, f"{device}.{label}")
        if label == "baseline":
            self.baselines[device] = health
        self.note(f"`{device}` accepted on `{version}`: committed software, Ready, secure/provisioned OS service, "
                  "healthy processor load/memory, unchanged RUNNING app inventory, and CVK's maintenance taint cleared. "
                  "Forwarding/traffic not tested.")

    def worker_snapshot(self, device, label, require_ready=True):
        deployment = self.get("deployment", device + "-vk")
        selector = deployment["spec"]["selector"].get("matchLabels", {})
        if not selector or deployment["spec"]["selector"].get("matchExpressions"):
            raise RuntimeError(f"{device}: unsupported worker selector")
        pods = json.loads(self.kubectl("get", "pods", "-l", ",".join(f"{key}={value}" for key, value in sorted(selector.items())), "-o", "json"))["items"]
        snapshots = []
        for pod in pods:
            containers = [c for c in pod.get("status", {}).get("containerStatuses", []) if c.get("name") == "cisco-vk"]
            images = [c.get("image") for c in pod.get("spec", {}).get("containers", []) if c.get("name") == "cisco-vk"]
            snapshots.append({"metadata": metadata(pod), "deleting": bool(pod["metadata"].get("deletionTimestamp")),
                              "phase": pod.get("status", {}).get("phase"), "ready": ready(pod),
                              "requestedImages": images,
                              "containers": [{key: value for key, value in container.items()
                                              if key in ("name", "image", "imageID", "containerID", "ready", "restartCount", "state", "lastState")}
                                             for container in containers]})
        self.save(f"{device}.{label}.workers.json", snapshots)
        if require_ready:
            if len(snapshots) != 1 or snapshots[0]["deleting"] or not snapshots[0]["ready"] or len(snapshots[0]["containers"]) != 1:
                raise RuntimeError(f"{device}: expected exactly one ready worker")
            container = snapshots[0]["containers"][0]
            if (not container.get("ready") or snapshots[0]["requestedImages"] != [self.args.image]
                    or not container.get("imageID") or not snapshots[0]["metadata"].get("uid")):
                raise RuntimeError(f"{device}: ready worker image identity is not proven")
            identity = (snapshots[0]["metadata"].get("uid"), container["imageID"], container.get("restartCount", 0))
            if device in self.worker_identities and identity != self.worker_identities[device]:
                raise RuntimeError(f"{device}: worker changed or restarted during the cycle; inspect recorded evidence")
            self.worker_identities[device] = identity
            if label == "baseline":
                binary = self.kubectl("exec", snapshots[0]["metadata"]["name"], "-c", "cisco-vk", "--",
                                      "/usr/local/bin/cisco-vk", "version")
                self.save(f"{device}.binary-version.txt", binary)
                verify_worker_revision(binary, self.args.revision)
        return snapshots

    def inspect_target(self, device, address):
        obj = self.get("ciscodevice", device)
        spec = obj["spec"]
        if spec.get("address") != address or spec.get("driver") != "XE":
            raise RuntimeError(f"{device}: expected XE device at {address}; target identity mismatch")
        if spec.get("gnoi", {}).get("transportSecurity") != "tls":
            raise RuntimeError(f"{device}: explicit secure gNOI is required")
        deployment = self.get("deployment", device + "-vk")
        images = [c["image"] for c in deployment["spec"]["template"]["spec"]["containers"] if c["name"] == "cisco-vk"]
        if images != [self.args.image]:
            raise RuntimeError(f"{device}: worker image differs from candidate: {images}")
        self.save(device + ".deployment.json", {"metadata": {"name": deployment["metadata"]["name"]},
                  "strategy": deployment["spec"].get("strategy"), "status": deployment.get("status"),
                  "workers": [{"name": c["name"], "image": c["image"], "resources": c.get("resources", {})}
                              for c in deployment["spec"]["template"]["spec"]["containers"]]})
        self.save(device + ".identity.json", {"namespace": self.args.namespace, "name": device,
                  "address": address, "driver": spec["driver"], "status": obj.get("status")})
        self.kubectl("rollout", "status", "deployment/" + device + "-vk", "--timeout=25s")
        self.worker_snapshot(device, "baseline")
        inventory = self.probe(device, "certificates", "GNOICertGet")
        certificates = json.loads(inventory[0]["output"])
        minimum = dt.datetime.now(dt.timezone.utc) + dt.timedelta(days=self.args.minimum_cert_days)
        if not certificates:
            raise RuntimeError(f"{device}: empty certificate inventory")
        for certificate in certificates:
            expiry = certificate.get("NotAfter")
            if not expiry or dt.datetime.fromisoformat(expiry.replace("Z", "+00:00")) <= minimum:
                raise RuntimeError(f"{device}: certificate {certificate.get('CertificateID')} has missing validity or expires within {self.args.minimum_cert_days} days")
        self.verify(device, "baseline", self.args.downgrade_version)

    def transition(self, device, direction):
        version = getattr(self.args, direction + "_version")
        name = f"{self.prefix}-{device}-{direction}"
        manifest = {"apiVersion": "ops.cisco.vk/v1alpha1", "kind": "IOSXESoftwareUpgrade",
                    "metadata": {"name": name, "namespace": self.args.namespace},
                    "spec": {"deviceRef": {"name": device}, "imageSource": {
                        "url": getattr(self.args, direction + "_url"),
                        "sha256": getattr(self.args, direction + "_sha256")},
                        "targetVersion": version, "strategy": "Reload", "rollbackOnFailure": True,
                        "installTimeoutSeconds": 14400, "rebootTimeoutSeconds": 3600}}
        try:
            self.worker_snapshot(device, direction + "-before")
            previous = self.args.downgrade_version if direction == "upgrade" else self.args.upgrade_version
            self.verify(device, direction + "-before", previous)
            self.submit(manifest)
            obj = self.wait("xeupgrade", name, observation_timeout(manifest["spec"]))
            if name not in self.maintenance_observed:
                self.note(f"`{name}`: no in-flight maintenance taint was captured; observations do not prove its pre-dispatch ordering.")
            status = obj["status"]
            if not ready(obj) or not matches(status.get("runningVersion", ""), version):
                raise RuntimeError(f"{name}: success lacks Ready or matching runningVersion")
            if not status.get("primarySupervisorActivationRequested"):
                raise RuntimeError(f"{name}: no recorded activation; this is not evidence of a round trip")
            self.verify(device, direction, version)
            self.worker_snapshot(device, direction + "-after")
        finally:
            evidence_errors = []
            for filename, args in [(name + ".final-observation.json", ("get", "xeupgrade", name, "-o", "json")),
                    (name + ".events.json", ("get", "events", "--field-selector",
                    f"involvedObject.kind=IOSXESoftwareUpgrade,involvedObject.name={name}", "-o", "json")),
                    (name + ".provider.log", ("logs", "deployment/" + device + "-vk", "-c", "cisco-vk",
                     "--timestamps", "--since-time=" + self.started))]:
                try:
                    self.save(filename, self.kubectl(*args))
                except Exception as error:
                    evidence_errors.append(filename)
                    self.note(f"Evidence collection issue for `{filename}`: {error}")
            try:
                for worker in self.worker_snapshot(device, direction + "-final", require_ready=False):
                    for container in worker["containers"]:
                        if container.get("restartCount", 0):
                            pod = worker["metadata"]["name"]
                            self.save(name + "." + pod + ".previous-provider.log", self.kubectl("logs", pod,
                                      "-c", "cisco-vk", "--previous", "--timestamps", "--since-time=" + self.started))
            except Exception as error:
                evidence_errors.append("worker/previous logs")
                self.note(f"Worker/previous-log evidence incomplete: {error}")
            if evidence_errors and sys.exc_info()[0] is None:
                raise RuntimeError("required evidence collection incomplete: " + ", ".join(evidence_errors))


def arguments():
    parser = argparse.ArgumentParser(description=__doc__)
    for name in ("context", "namespace", "image", "revision", "evidence-dir"):
        parser.add_argument("--" + name, required=True)
    parser.add_argument("--device", action="append", required=True, help="Exact CiscoDevice name=management-address; repeat for serial targets")
    for direction in ("upgrade", "downgrade"):
        for name in ("version", "url", "sha256"):
            parser.add_argument(f"--{direction}-{name}", required=True)
    parser.add_argument("--minimum-cert-days", type=int, default=30)
    parser.add_argument("--execute", action="store_true", help="Authorize Install/Activate/reloads; otherwise run read-only probes")
    args = parser.parse_args()
    if not re.fullmatch(r"[0-9a-f]{40}", args.revision):
        parser.error("revision must be the full candidate Git SHA")
    if not 0 <= args.minimum_cert_days <= 3650:
        parser.error("minimum-cert-days must be between 0 and 3650")
    targets = []
    for item in args.device:
        name, separator, address = item.partition("=")
        if not separator or not re.fullmatch(r"[a-z0-9]([-a-z0-9]*[a-z0-9])?", name) or len(name) > 100 or not address:
            parser.error("each device must be a DNS-label name (max 100 characters) and address")
        targets.append((name, address))
    if len({name for name, _ in targets}) != len(targets) or len({address for _, address in targets}) != len(targets):
        parser.error("duplicate device names or addresses are not allowed")
    args.targets = targets
    for direction in ("upgrade", "downgrade"):
        if not re.fullmatch(r"[0-9]+(?:\.[0-9]+)+[a-z]?", getattr(args, direction + "_version")):
            parser.error("invalid version")
        if not re.fullmatch(r"[0-9a-f]{64}", getattr(args, direction + "_sha256")):
            parser.error("image SHA-256 must have 64 lowercase hexadecimal characters")
        url = urlsplit(getattr(args, direction + "_url"))
        if url.scheme not in ("https", "http") or not url.hostname or url.username or url.password or url.query or url.fragment:
            parser.error("lab image URL must be HTTP(S), with no credentials, query, or fragment")
    if args.upgrade_version == args.downgrade_version:
        parser.error("upgrade and downgrade versions must differ")
    return args


def main():
    os.umask(0o077)
    cycle = Cycle(arguments())
    try:
        for device, address in cycle.args.targets:
            cycle.inspect_target(device, address)
        if cycle.args.execute:
            for direction in ("upgrade", "downgrade"):
                for device, _ in cycle.args.targets:
                    cycle.transition(device, direction)
            cycle.note("PASS: every target completed upgrade and downgrade with fresh gNOI and platform evidence.")
        else:
            cycle.note("PASS: preflight only; no software mutation was submitted.")
    except (Exception, KeyboardInterrupt) as error:
        cycle.note(f"STOPPED: {error}. Preserve all operation objects; no automatic cleanup or retry was attempted.")
        return 1
    return 0


if __name__ == "__main__":
    sys.exit(main())
