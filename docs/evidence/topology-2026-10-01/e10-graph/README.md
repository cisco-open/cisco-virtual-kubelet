# E10 read-only physical topology graph diagnostic

Status: **partial PASS**, 2 October 2026. This record qualifies the read-only
runtime consumer and its fail-closed behavior. It does not qualify declared
topology drift, path health, disruption authority, or E10 as a whole.

## Scope

The Linux/amd64 `kubectl-ciscovk` plugin was built from the graph diagnostic
increment immediately following candidate `2f27f302` and copied to the
Ubuntu16 lab control host. The cluster still ran the exact `2f27f302` manager
and worker candidate previously qualified by the C3 physical pacing run. No
device mutation, rollout, label, policy, approval, credential, or direct
device session was used for this check.

The command was run against the physical-test namespace:

```console
sudo /tmp/kubectl-ciscovk-graph-v2 topology graph \
  -n cvk-live \
  --kubeconfig /etc/rancher/k3s/k3s.yaml \
  --max-age 5m \
  -o table
```

It reported:

```text
complete=false nodes=4 edges=9 diagnostics=11
evidence=sha256:00bd38816de29856fbef4dd60e5c1a511b1f60f578bfec26877db650d2b140a1
provenance=sha256:c20e6b0eaeac33e526526faca4140cacf868801d7214f22d92c1b8f8a3722835
```

The three physical Catalyst devices were keyed by their manager-bound serial
identities: `foc2416u0mv`, `foc2520l6e8`, and `foc2520l6h1`. The legacy
`nexus9300v-live` object had no manager-bound physical identity or accepted
collection time and remained visible as `unbound:cvk-live/nexus9300v-live`.

The nine accepted CDP edges used peer host names such as `C9K-1`, `C9K-2`, and
`C9K-4.dmz.cisco.com`, while graph nodes use immutable bound serial identities.
Because no manager-approved mapping proves those names correspond to those
serials, each remained `UnknownPeer`. Guessing from naming convention would
cross the graph trust boundary, so the diagnostic correctly remained
incomplete.

The structured fail-closed invocation was then run:

```console
sudo /tmp/kubectl-ciscovk-graph-v2 topology graph \
  -n cvk-live \
  --kubeconfig /etc/rancher/k3s/k3s.yaml \
  --max-age 5m \
  -o json \
  --require-complete
```

It emitted the complete diagnostic JSON and returned exit status `1` with:

```text
error: topology graph is incomplete; inspect diagnostics
```

## Retained evidence

- [`graph.json`](graph.json) is the exact structured output, SHA-256
  `e523b7909ee9aea33ff2a29fbf44a84c65eeaecca4f71469fcd531059799d363`.
- `evidenceHash` covers normalized graph content. `provenanceHash` separately
  covers the four manager-accepted device/sample identities and collection
  intervals recorded in `observations`; neither is disruption authority.
- It contains accepted topology identities and lab interface names only. It
  contains no Secret data, session token, credential, kubeconfig, private key,
  or device command output.

## Qualification boundary and next tests

This run proves that the CLI reaches real manager-accepted physical evidence,
retains incomplete/unbound inventory, produces deterministic bounded output,
and can fail automation closed. It also exposes the next required design work:

1. add a manager-approved, provenance-carrying mapping from protocol peer
   identities to bound physical identities; never trust CDP names alone;
2. accept bounded administrator-declared links for drift comparison without
   allowing discovery to rewrite policy;
3. complete maximum diagnostic/declaration and declared-link CLI fixtures;
4. disable and restore an isolated lab link, collect fresh accepted samples,
   and prove the drift appears and clears without changing authorization.

Until those steps pass, E10-B–D and complete E10 acceptance remain open.
