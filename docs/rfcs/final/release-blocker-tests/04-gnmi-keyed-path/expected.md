# Test 04 — Expected outcome

## Phase + family status

```yaml
status:
  phase: InSync
  observedGeneration: 1
  familyStatus:
    - name: interface_ethernet
      state: InSync
      opCount: 1   # the Replace/Merge for the description field
  conditions:
    - type: Ready
      status: "True"
      reason: Succeeded
```

## Device-side state

`GigabitEthernet0/0/0` (or the substituted port) carries a `description` field whose value matches the string in `00-apply.yaml`'s ConfigMap:

```
description cisco-vk release-blocker test 04 — wave 5A-fu/7B
```

Verifiable directly via RESTCONF:

```sh
curl --silent --insecure --user "${USER}:${PASS}" \
  --header 'Accept: application/yang-data+json' \
  "https://${ADDR}/restconf/data/Cisco-IOS-XE-native:native/interface/GigabitEthernet=0%2F0%2F0/description" \
  | jq -r '.["Cisco-IOS-XE-native:description"]'
```

## Wire-level proof

If the cisco-vk pod's transport layer is configured to log gNMI Set requests at debug level, the log line for this reconcile should show a `PathElement` sequence like:

```
update.path.elem[0]: name="interface"
update.path.elem[1]: name="GigabitEthernet" key={"name": "0/0/0"}
update.path.elem[2]: name="description"
```

Specifically, the key value is `"0/0/0"` (three characters separated by two slashes), NOT `"0"` or any URL-encoded form. The pre-Wave-5A-fu path lost the slashes during string parsing; the structured `PathSpec` carries them through.

## Other interfaces

**Untouched.** The test names exactly one interface in `managedFamilies`'s scope; other interfaces' descriptions, IPs, ACLs, and operational state must be identical to pre-state. If any other interface's `description` changes, that's a writer bug or a CR-overlap bug — investigate before continuing the runbook.
