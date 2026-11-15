# Independent review, round 2

Reviewed baseline: `06e2bfa` plus the pending fixed transport-error normalization
in desktop_http.go. This document is interim until the native Windows lifecycle
fix and rerun are examined. No implementation files were edited by this reviewer.

## Round-1 findings rechecked

| Finding | Result |
| --- | --- |
| Process existence called readiness | Fixed: status.ready comes from the authenticated pool/lease state; a spawned-but-unready engine is shown as loading. testAnswer uses the same readiness gate. |
| Missing restarting/stopping progress | Fixed: both states are busy in the frontend, with cancellation remaining available. |
| Cross-port cookie collision | Fixed: names derive from installation directory and listener; a shared cookie-jar test verifies two live installations. |
| Interrupted application-key initialization | Fixed: private intent precedes central configuration creation; restart resumes pending provisioning, while an authoritative durable key set preserves revocation even with a stale marker. |
| Contrast failures | Fixed in independently rerun first-run and completed-mobile axe checks. |
| Profile-dependent explicit service directory | Fixed: os.UserConfigDir is now consulted only if no explicit directory was supplied, with a regression test. |

Application access export is explicit, uses a distinct nonadmin model-scoped
credential, is absent from polling status, is checked against the durable key
set, and is cleared from the browser DOM when hidden. A public CA certificate can
be exported without exposing its private key. The admin key stays server-side.

The runtime trust boundary remains appropriately narrow: fixed release metadata,
exact hash/size, bounded HTTPS redirects, private staging and executable probe;
no browser-supplied executable URL is accepted. Unsupported platforms are stated
rather than silently offered a different package.

The server installer now restricts its application path to ProgramFiles/KI-Basis
both before installer lifecycle actions and in the helper. A negative native CI
case attempts a user-writable alternate path. No broad user read permission to
server credentials is introduced for the elevated launcher.

## Independently rerun tests

- Focused Go race suite TestDesktop|TestRuntime: passed, 13.358 seconds.
- Real browser first-run, explicit download consent, cancellation/retry, readiness,
  actual-text rendering, application access export/hide and narrow layout:
  passed, including axe WCAG A/AA checks in first-run and completed mobile states.
- Read the new deterministic full-lifecycle test. It uses real TLS enrollment,
  agent heartbeat/broker traffic, a genuine owned model subprocess implementing
  the engine contract, tokenization and inference through the gateway, then
  restarts while retaining identity. It correctly labels itself an orchestration
  test rather than evidence of actual Gemma model quality.

## Remaining blocking native evidence

The first native Windows run failed in two ways that must not be averaged away:

1. Installer compilation rejected an AppId-dependent Inno configuration because
   UsePreviousLanguage was not explicitly disabled. The installer agent is fixing
   this, then genuine Windows install/update/uninstall must rerun.
2. The Windows Go lifecycle test reported gateway.db still in use during temporary
   directory cleanup after TestDesktopInferenceAcrossProcessLifecycle. That is a
   lifecycle defect until the database-owning goroutines/handlers/child handles
   have stopped and native regression evidence passes. In particular desktop
   launches controller.Monitor without joining it, while Store.Close explicitly
   requires its callers to stop request/monitor goroutines first. Close errors
   must not be ignored as proof of a completed shutdown.

Pending transport normalization appropriately replaces wrapped URL/connection
errors with fixed browser error codes and has a regression test for private URL
redaction. The new timeout code also needs its user-facing message; the main
agent reports that mapper is being added.

## Rating

Current code-readiness is **8.8/10, conditional/interim**, with all round-1 source
findings resolved but native Windows lifecycle/installer failures still open.
Do not label it 9/10 before those corrections and their reruns pass.

The actual Windows product acceptance score remains **not established**. Even a
subsequent successful CI install/update/uninstall/service-restart suite does not
prove interactive UAC/SmartScreen behavior, publisher signing/reputation, a
Windows reboot without login or real Gemma inference on the user's target
hardware. Those release acceptance items must remain explicit.
