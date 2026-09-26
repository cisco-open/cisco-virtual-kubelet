{{/* Preserve native unhealthy-node detection and taint reconciliation without
granting topology, identity, cordon, or healthy-status authority. */}}
{{- define "cisco-virtual-kubelet.nativeNodeLifecycle" -}}
request.operation == 'UPDATE' && object != null && oldObject != null &&
request.userInfo.groups.exists(g, g == 'system:authenticated') &&
((request.userInfo.username == 'system:serviceaccount:kube-system:node-controller' &&
  request.userInfo.groups.exists(g, g == 'system:serviceaccounts') &&
  request.userInfo.groups.exists(g, g == 'system:serviceaccounts:kube-system')) ||
 request.userInfo.username == 'system:kube-controller-manager') &&
object.metadata.name == oldObject.metadata.name &&
object.metadata.uid == oldObject.metadata.uid &&
has(object.metadata.annotations) == has(oldObject.metadata.annotations) &&
(!has(object.metadata.annotations) || object.metadata.annotations == oldObject.metadata.annotations) &&
has(object.metadata.finalizers) == has(oldObject.metadata.finalizers) &&
(!has(object.metadata.finalizers) || object.metadata.finalizers == oldObject.metadata.finalizers) &&
has(object.metadata.ownerReferences) == has(oldObject.metadata.ownerReferences) &&
(!has(object.metadata.ownerReferences) || object.metadata.ownerReferences == oldObject.metadata.ownerReferences) &&
(has(object.metadata.labels) ? object.metadata.labels.transformMap(k, v,
  !(k in ['beta.kubernetes.io/os', 'beta.kubernetes.io/arch']), v) : {}) ==
(has(oldObject.metadata.labels) ? oldObject.metadata.labels.transformMap(k, v,
  !(k in ['beta.kubernetes.io/os', 'beta.kubernetes.io/arch']), v) : {}) &&
['os', 'arch'].all(k,
  !has(object.metadata.labels) || !('beta.kubernetes.io/' + k in object.metadata.labels) ||
  ('kubernetes.io/' + k in object.metadata.labels &&
   object.metadata.labels['beta.kubernetes.io/' + k] == object.metadata.labels['kubernetes.io/' + k])) &&
((request.subResource == 'status' && object.spec == oldObject.spec &&
  has(object.status) && has(oldObject.status) &&
  {{- range $field := list "capacity" "allocatable" "phase" "addresses" "daemonEndpoints" "nodeInfo" "images" "volumesInUse" "volumesAttached" "config" "runtimeHandlers" "features" "declaredFeatures" }}
  has(object.status.{{ $field }}) == has(oldObject.status.{{ $field }}) &&
  (!has(object.status.{{ $field }}) || object.status.{{ $field }} == oldObject.status.{{ $field }}) &&
  {{- end }}
  has(object.status.conditions) &&
  object.status.conditions.all(c,
    (has(oldObject.status.conditions) && oldObject.status.conditions.exists(o, o == c)) ||
    (c.type in ['Ready', 'MemoryPressure', 'DiskPressure', 'PIDPressure'] &&
     c.status == 'Unknown' && c.reason in ['NodeStatusUnknown', 'NodeStatusNeverUpdated'])) &&
  (!has(oldObject.status.conditions) || oldObject.status.conditions.all(o,
    object.status.conditions.exists(c, c.type == o.type)))) ||
 ((!has(request.subResource) || request.subResource == '') &&
  has(object.status) == has(oldObject.status) &&
  (!has(object.status) || object.status == oldObject.status) &&
  {{- range $field := list "podCIDR" "podCIDRs" "providerID" "unschedulable" "configSource" "externalID" }}
  has(object.spec.{{ $field }}) == has(oldObject.spec.{{ $field }}) &&
  (!has(object.spec.{{ $field }}) || object.spec.{{ $field }} == oldObject.spec.{{ $field }}) &&
  {{- end }}
  (has(object.spec.taints) ? object.spec.taints : []).filter(t,
    !(t.key in ['node.kubernetes.io/not-ready', 'node.kubernetes.io/unreachable',
      'node.kubernetes.io/memory-pressure', 'node.kubernetes.io/disk-pressure',
      'node.kubernetes.io/pid-pressure', 'node.kubernetes.io/network-unavailable',
      'node.kubernetes.io/unschedulable'])) ==
  (has(oldObject.spec.taints) ? oldObject.spec.taints : []).filter(t,
    !(t.key in ['node.kubernetes.io/not-ready', 'node.kubernetes.io/unreachable',
      'node.kubernetes.io/memory-pressure', 'node.kubernetes.io/disk-pressure',
      'node.kubernetes.io/pid-pressure', 'node.kubernetes.io/network-unavailable',
      'node.kubernetes.io/unschedulable'])) &&
  (!has(object.spec.taints) || object.spec.taints.all(t,
    (has(oldObject.spec.taints) && oldObject.spec.taints.exists(o, o == t)) ||
    ((!has(t.value) || t.value == '') &&
     (t.effect == 'NoSchedule' ||
      (t.effect == 'NoExecute' && t.key in ['node.kubernetes.io/not-ready', 'node.kubernetes.io/unreachable'])))))))
{{- end -}}
