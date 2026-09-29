{{/* Native garbage collection may remove only its own foreground finalizer
from an already-deleting object. It receives no workload/spec/status authority. */}}
{{- define "cisco-virtual-kubelet.nativeForegroundCleanup" -}}
request.operation == 'UPDATE' &&
(!has(request.subResource) || request.subResource == '') &&
object != null && oldObject != null &&
has(oldObject.metadata.deletionTimestamp) &&
request.userInfo.groups.exists(g, g == 'system:authenticated') &&
((request.userInfo.username == 'system:serviceaccount:kube-system:generic-garbage-collector' &&
  request.userInfo.groups.exists(g, g == 'system:serviceaccounts') &&
  request.userInfo.groups.exists(g, g == 'system:serviceaccounts:kube-system')) ||
 request.userInfo.username == 'system:kube-controller-manager') &&
object.metadata.name == oldObject.metadata.name &&
object.metadata.namespace == oldObject.metadata.namespace &&
object.metadata.uid == oldObject.metadata.uid &&
has(object.metadata.labels) == has(oldObject.metadata.labels) &&
(!has(object.metadata.labels) || object.metadata.labels == oldObject.metadata.labels) &&
has(object.metadata.annotations) == has(oldObject.metadata.annotations) &&
(!has(object.metadata.annotations) || object.metadata.annotations == oldObject.metadata.annotations) &&
has(object.metadata.ownerReferences) == has(oldObject.metadata.ownerReferences) &&
(!has(object.metadata.ownerReferences) || object.metadata.ownerReferences == oldObject.metadata.ownerReferences) &&
has(oldObject.metadata.finalizers) &&
'foregroundDeletion' in oldObject.metadata.finalizers &&
(has(object.metadata.finalizers) ? object.metadata.finalizers : []) ==
  oldObject.metadata.finalizers.filter(f, f != 'foregroundDeletion') &&
object.spec == oldObject.spec &&
has(object.status) == has(oldObject.status) &&
(!has(object.status) || object.status == oldObject.status)
{{- end -}}
