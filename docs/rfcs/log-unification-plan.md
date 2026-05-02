# Log unification plan

**Branch:** `pr/johalley/nacxe`
**Status:** plan, not implementation. Closes architectural-review watch-item **#9** (two logging configs in one process — logrus from VK + zap from controller-runtime).
**Audience:** anyone planning the SLO-grade observability cut.

---

## 1. The current state, accurately

Inside one cisco-vk pod, two logging libraries coexist:

| Library | Source of dependency | Used for |
|---|---|---|
| `github.com/sirupsen/logrus` (`virtual-kubelet/log`) | upstream `virtual-kubelet/virtual-kubelet` — its `log.Logger` interface is satisfied by a logrus adapter | every code path that runs under the VK provider lifecycle: pod controller, apphosting reconciler, NETCONF/RESTCONF/gNMI transports, intent resolver, engine apply path, drift detection |
| `go.uber.org/zap` via `sigs.k8s.io/controller-runtime/pkg/log/zap` | controller-runtime — its `logr.Logger` interface is the canonical k8s-controller logger | every code path that runs under controller-runtime: `ConfigReconciler.Reconcile`, `IOSXEConfigBundle` controller, the aggregator's `AggregatedReconciler` |

Both libraries write to stderr. Both produce structured output. Their formats are subtly different — logrus uses lowercase keys (`level=info msg=...`), zap uses uppercase keys with a different timestamp format (`INFO ... ` ... `{...}`). Operators end up writing two grep patterns instead of one.

Two specific operator complaints this produces today:

1. **Mixed JSON/non-JSON output.** Set `LOG_FORMAT=json` and you get JSON from controller-runtime spans but text from VK pod controller — the cisco-vk binary's stdout is then unparseable as a single JSON stream.
2. **Independent log-level routing.** `LOG_LEVEL=debug` on the binary tunes logrus only. controller-runtime's zap stays at `info`. Operators chasing a reconciler bug see partial picture.

This is cosmetic in the sense that no behaviour breaks. It is a real operability tax in the sense that every "what's my pod doing right now" question takes two queries.

---

## 2. Constraints

The hard constraints, before we discuss options:

1. **Cannot change `virtual-kubelet/virtual-kubelet`'s `log.G(ctx)` API.** That's an upstream contract. The cisco-vk codebase uses `log.G(ctx).WithError(err).Warn(...)` in dozens of places; any plan that requires changing those call sites everywhere is too big for the value.
2. **controller-runtime's `logr.Logger` is non-negotiable for any code that takes a `ctrl.Context`** — controller-runtime hands its own logger via `ctrl.LoggerFrom(ctx)` and uses it for its own internal events.
3. **No new third-party dependency** unless it directly resolves the duplication. `slog` (stdlib, Go 1.21+) is the obvious lever; the codebase already targets Go 1.25 in CI.
4. **Existing structured fields must survive** intact — every `WithField("device", deviceName)` etc. The transition cannot drop attribution.

---

## 3. The three real options

### Option A — single zap backend, logrus is a shim

**Mechanism.** Replace logrus's default writer with one that funnels into the controller-runtime zap logger. logrus has a `Hook` API; a custom hook intercepts every entry and re-emits it through zap.

**Wins.**

- Single output stream, single format.
- `LOG_LEVEL` flag tunes zap; logrus inherits.
- Spans (OTel) and zap output share trace IDs out of the box (zap supports `WithFields` from a context).

**Costs.**

- Dual-allocation per log line (logrus formats, hook re-formats through zap). ~2× CPU cost on the hot reconcile path. Likely fine; reconcile is not log-bound.
- The shim is non-obvious code.
- logrus levels (`Trace`, `Debug`, `Info`, `Warn`, `Error`, `Fatal`, `Panic`) don't map 1:1 onto zap (`Debug`, `Info`, `Warn`, `Error`, `DPanic`, `Panic`, `Fatal`); collapse `Trace` → `Debug` and accept the loss.

### Option B — single logrus backend, zap is a shim

**Mechanism.** controller-runtime accepts any `logr.Logger` via `ctrl.SetLogger(...)`. Provide a `logr.LogSink` implementation that delegates to logrus.

**Wins.**

- Smaller change — only one call site changes (`log/zap` is replaced with our adapter in `cmd/cisco-vk/manager.go` and `cmd/cisco-vk/run.go`).
- logrus already has a JSON formatter mode.

**Costs.**

- logrus is not the future. The Go ecosystem is converging on `slog`. Doubling down on logrus extends our exposure to a library that's effectively in maintenance mode.
- controller-runtime's expectation is structured zap-style key/value pairs; logrus's `WithFields` is similar but the call patterns inside controller-runtime ("starting workers", "Failed to watch") are zap-shaped.

### Option C — single `slog` backend, both shims

**Mechanism.** stdlib `log/slog` is the destination. logrus → slog via a hook. controller-runtime → slog via the official `logr.LogSinkFromSlog` adapter.

**Wins.**

- Future-proof: `slog` is the official Go logger and has community-maintained handlers (JSON, text, OTel-aware).
- No third-party dependency in the *backend* layer; the only third-party code is the two thin adapters.
- A single log-format flag feeds slog's `HandlerOptions`.
- Trivial to bridge to OTel logs once the OTel logs SDK stabilises.

**Costs.**

- More code to write than option A — two adapters, not one.
- Initial change set is larger because every `log.G(ctx)` call still works (the shim is invisible) but the project's logging contract changes from "logrus is the truth" to "slog is the truth."

---

## 4. Recommendation

**Option C — slog as the single backend, logrus and zap as thin shims.** Rationale:

1. `slog` is the canonical Go logger going forward. Building on logrus or zap as the truth keeps us coupled to libraries the broader ecosystem is moving away from.
2. The shim cost is one-time. Once the controller-runtime adapter exists, every future controller-runtime upgrade benefits without re-work.
3. OTel logs integration becomes a one-line handler swap when that SDK reaches GA.
4. Operators get a single output format and a single level knob, which is the actual pain point.

The work is bounded by the surface area of two adapters and a `cmd/cisco-vk/main.go` configuration block. No call sites move.

---

## 5. Implementation outline

### 5.1 Package layout

```
internal/log/
├── slog_backend.go       // slog.Handler factory: text or JSON, timestamp + level wiring
├── logrus_to_slog.go     // logrus.Hook that emits via slog.Logger
├── zap_to_slog.go        // logr.LogSink (or zapcore.Core) that emits via slog.Logger
└── log_test.go           // round-trip: logrus.Info("hi", "k", "v") yields a slog record with k=v
```

### 5.2 Wiring (single change point)

```go
// cmd/cisco-vk/manager.go (and cmd/cisco-vk/run.go, identical block)

handler := mylog.NewHandler(mylog.Config{
    Level:  resolveLogLevel(),  // honours LOG_LEVEL env + --log-level flag
    Format: resolveLogFormat(), // "text" or "json"
    Output: os.Stderr,
})
backend := slog.New(handler)
slog.SetDefault(backend)

logrus.AddHook(mylog.NewLogrusHook(backend))
logrus.SetOutput(io.Discard)               // logrus's own writer is no longer used

ctrl.SetLogger(mylog.NewLogrLogger(backend)) // replaces zap.New(...)
```

That's the entire production change. Every existing `log.G(ctx).Info(...)` keeps compiling. Every existing `crlog.FromContext(ctx).Info(...)` keeps compiling. Both end up as `slog.Record` values written through one handler.

### 5.3 Behaviour preservation

- All `WithField`/`WithValues` calls land as slog attributes — no field drops, no lossy conversions.
- Log levels: a uniform mapping table inside `internal/log` covers logrus's `Trace`/`Debug`/`Info`/`Warn`/`Error`/`Fatal`/`Panic` and controller-runtime's `Info`/`Error` (V-levels honoured for `Info`).
- Default format stays text-mode for human-friendliness, JSON-on-flag for log shipping.

### 5.4 Tests

- Round-trip: assert that a logrus call with field `k=v` produces a slog record carrying the same attribute.
- Level filter: assert `LOG_LEVEL=warn` suppresses `Info` from both logrus and controller-runtime call sites.
- Format JSON: assert the line for both call sites is valid JSON with the same schema.

---

## 6. Out of scope

- **OTel logs**: deliberately deferred. The OTel logs SDK is still settling; once GA, swap the slog handler for an OTel handler in one line.
- **Per-package log levels**: logrus and zap both support this in different ways; slog supports it via custom handlers. Not a shipping requirement; can land as a follow-up.
- **Log sampling**: high-volume reconciler debug spam is real but not pervasive enough today to warrant a sampler. Reassess after operator feedback.
- **Audit log persistence**: orthogonal — `IOSXEConfigApplyLog` (Phase 7) is the audit channel, not stdout.

---

## 7. Effort and timing

Bounded at ~3 engineer-days:

- Day 1: implement the three files in `internal/log/`, wire `cmd/cisco-vk/`.
- Day 2: round-trip + level + format tests; full test suite green.
- Day 3: smoke against kind, verify operator-visible stdout is single-format, ship docs/observability.md update with the new `LOG_FORMAT=json` knob.

**No CRD changes. No chart changes (env vars are pre-existing).** Ship as a single PR titled `chore(log): unify logrus + zap onto slog` with this RFC linked.

---

## 8. What we are NOT doing — explicitly

- **Not switching to a structured-only API.** Operators rely on `log.G(ctx).WithError(err).Warnf("…")`. Those keep working.
- **Not introducing a new logging interface.** This is a backend change, not a contract change.
- **Not changing the controller-runtime logger field names.** controller-runtime emits `controller`, `controllerKind`, `reconcileID` etc. by convention; the slog adapter passes them through unmodified.

---

## 9. Acceptance criteria

The implementation PR ships when:

1. `go test -race ./...` is green.
2. A kind smoke run produces a single, parseable log stream with both VK-provider lines and controller-runtime lines in the same format.
3. `LOG_LEVEL=debug` adjusts both library families' verbosity.
4. `LOG_FORMAT=json` produces valid JSON for every line emitted during a 60s smoke window.
5. No `log.G(ctx)` or `crlog.FromContext(ctx)` call site has been edited (zero source diff outside `internal/log/` and the two `cmd/cisco-vk/` wiring blocks).
