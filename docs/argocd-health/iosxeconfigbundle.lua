-- ArgoCD health check for config.cisco.vk/v1alpha1/IOSXEConfigBundle.
--
-- A bundle is Healthy when every fanned-out child IOSXEConfig is
-- itself InSync. Any child in Failed/Drifted demotes the bundle to
-- Degraded; any child Progressing keeps the bundle Progressing.
-- An empty match set (Ready=False / NoMatchingDevices) is
-- explicitly Healthy=false rather than Progressing — that's a
-- configuration error the operator should see, not a transient
-- "controller hasn't run yet" state.

hs = {}
if obj.status == nil then
  hs.status = "Progressing"
  hs.message = "no status yet"
  return hs
end

if obj.status.conditions ~= nil then
  for _, c in ipairs(obj.status.conditions) do
    if c.type == "Ready" and c.status == "False" and c.reason == "NoMatchingDevices" then
      hs.status  = "Degraded"
      hs.message = c.message or "no matching devices"
      return hs
    end
  end
end

degraded = 0
progressing = 0
healthy = 0

if obj.status.generatedCRs ~= nil then
  for _, child in ipairs(obj.status.generatedCRs) do
    p = child.phase or ""
    if p == "InSync" then
      healthy = healthy + 1
    elseif p == "Drifted" or p == "Failed" then
      degraded = degraded + 1
    else
      -- Pending, Paused, empty, etc. — count as progressing so
      -- the bundle doesn't flap healthy on the first reconcile.
      progressing = progressing + 1
    end
  end
end

total = healthy + degraded + progressing

if degraded > 0 then
  hs.status  = "Degraded"
  hs.message = string.format("%d/%d children failed/drifted", degraded, total)
  return hs
end

if progressing > 0 then
  hs.status  = "Progressing"
  hs.message = string.format("%d/%d children still converging", progressing, total)
  return hs
end

if healthy == 0 then
  hs.status  = "Progressing"
  hs.message = "no children reconciled yet"
  return hs
end

hs.status  = "Healthy"
hs.message = string.format("%d device(s) in sync", healthy)
return hs
