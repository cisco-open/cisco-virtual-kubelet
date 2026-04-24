-- ArgoCD health check for config.cisco.vk/v1alpha1/IOSXEConfig.
--
-- Maps the CR's status.phase to an ArgoCD Health.Status:
--   InSync   -> Healthy
--   Drifted  -> Degraded     (driftPolicy=report finds work to do)
--   Failed   -> Degraded
--   Paused   -> Suspended    (operator opted out of reconcile)
--   *        -> Progressing  (Pending, Validating, Planning, …)
--
-- Operators install this by appending to ArgoCD's resourceCustomizations
-- or argocd-cm:
--
--   resource.customizations.health.config.cisco.vk_IOSXEConfig: |
--     <contents of this file>

hs = {}
if obj.status == nil then
  hs.status = "Progressing"
  hs.message = "no status yet"
  return hs
end

phase = obj.status.phase or ""

if phase == "InSync" then
  hs.status  = "Healthy"
  hs.message = "device matches declared intent"
  return hs
end

if phase == "Drifted" then
  hs.status  = "Degraded"
  hs.message = "device drift detected (driftPolicy=report); see status.drift[]"
  return hs
end

if phase == "Failed" then
  hs.status = "Degraded"
  if obj.status.conditions ~= nil then
    for _, c in ipairs(obj.status.conditions) do
      if c.type == "Ready" and c.status == "False" then
        hs.message = c.message
        break
      end
    end
  end
  if hs.message == nil or hs.message == "" then
    hs.message = "reconcile failed; see CR conditions"
  end
  return hs
end

if phase == "Paused" then
  hs.status  = "Suspended"
  hs.message = "driftPolicy=pause; reconcile suspended by operator"
  return hs
end

hs.status  = "Progressing"
hs.message = "phase=" .. phase
return hs
