# Catalyst Center API access and fabric qualification

The local tunnel endpoint `https://127.0.0.1:19443` was reachable. Token
authentication using the supplied `catapi` account returned HTTP 401. No token
was obtained, no inventory qualification ran, and no configuration was mutated.
The password was supplied through a terminal with echo disabled and was not
written to the repository or a credential file.

Added `TestLiveCatalystCenterFabricInventoryRead` under the `live` build tag.
It uses the production readers for fabric sites, multicast replication,
multicast virtual networks and embedded wireless settings. It checks fabric
references and reports empty topology as skipped qualification. It does not
request mutation authority or submit configuration.

Compilation passed with Go 1.26.9:

```sh
go test -tags=live ./internal/controlleradapter/catalystcenter -run '^$'
```

Authentication was subsequently restored using the user-supplied credential
file outside the repository. Token generation succeeded. The following tests
ran with credentials passed through the existing live-test environment:

```sh
go test -tags=live ./internal/controlleradapter/catalystcenter \
  -run '^TestLiveCatalystCenter(HealthInventoryAndSWIMRead|SitesReport|FabricInventoryRead)$' \
  -count=1 -v
```

Live results (Go 1.26.9, 2.397 seconds):

- Health, device inventory and SWIM image inventory: PASS; three devices and
  two images.
- Site model round-trip and local-only synthetic drift detection: PASS; three
  sites, including one area and one building, with no floors.
- Fabric inventory qualification: SKIP; no fabric sites are configured.
- Independent GET checks for fabric sites, multicast replication and multicast
  virtual networks: HTTP 200 with empty response arrays on all three endpoints.

No configuration writes were performed. Empty fabric inventory establishes API
availability, not multicast or embedded WLC functionality.

Reader qualification does not establish deployed-actor mutation qualification,
embedded WLC disable/reload completion, or complete NetAsCode parity. Those
remain separate work items.
