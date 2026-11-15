# Independent review, round 1

Reviewer: separate read-only agent. Scope: installation changes against `948e674`
on `codex/easy-installation`; implementation was still being completed during this
review. Findings refer to the observed version, not a frozen final commit.

## Result

Provisional implementation readiness: **8.0/10**, before the fixes below are
verified. Product acceptance: **not established**; a numerical 9/10 for the actual
Windows product is unsupported without native installation/lifecycle evidence and
target-machine inference/reboot validation. This is not a failed claim of
security; it is a distinction between implemented behavior and demonstrated
behavior.

## Findings

### P2: process existence is incorrectly called model readiness

`cmd/ki-basis/desktop.go` status logic around lines 410-415 and
`desktop_setup.go` testAnswer around lines 167-173 inspect the model manager's
`running` state. `internal/agent/models.go` sets that state immediately after
`startProcess`, before model loading, and explicitly delegates actual readiness
to `Runner.Ready`. `desktop.js` presents it as “Das Modell ist geladen” and enables
the test. A large model can need minutes to load; a user can be told it is ready
and immediately receive a not-ready gateway rejection.

Required fix: use actual engine/node readiness, keep loading distinct from process
started, and gate the test consistently. Verify with a process that is alive while
its health/slots endpoint remains unavailable, then becomes ready.

### P2: canonical restarting/stopping states are absent from frontend progress

`desktop.js` modelBusy recognizes downloading/verifying/loading/starting but not
`restarting` (real manager state) or `stopping` (desktop synthetic state). During
engine restarts, the UI offers a fresh start while the backend rejects it as an
operation in progress; cancel/stop is hidden. Include all in-progress states and
preserve an available cancellation/stop route.

### P2: desktop and server browser sessions collide across ports

Observed `desktop_http.go` used the same cookie name `ki_desktop`, hostname
127.0.0.1 and Path `/desktop/` for every installation. Cookies are not port scoped.
Opening a second installation replaces the first one's capability; the first
page gets 401 and stops polling. This matters because separate desktop/server
AppIds and data directories are supported.

Required fix: per-installation or per-listener cookie names, with a shared browser
cookie jar exercising two live installations. A cookie-name fix is already
visible in the working tree, not yet independently reverified.

### P2: interrupted first initialization can permanently lose application export

`startDesktop` decides `fresh` from missing central/config.json, then
`localConfiguration` writes that file before audit.Open and application-key
provisioning. A crash or recoverable I/O error in that interval leaves a valid
central configuration. Next launch has fresh=false and skips
`provisionApplicationAccess`, so export always returns credentials unavailable.
This breaks the promised recoverable setup, even though ordinary startup can
otherwise complete.

Required fix: recoverable provisioning transaction or explicit safe first-time
creation. Never recreate revoked keys. Test an interruption after central config
write and before application credential commit.

### P2: automated accessibility acceptance currently fails

The real browser check with axe reported WCAG contrast violations for the intro
eyebrow, inactive step labels, privacy note and footer. This was independently
reproduced with `node scripts/desktop_browser_test.cjs`. The completed mobile
state still needs examination after first-screen fixes. Main agent is correcting
contrast.

## Installer path-trust check

An initial concern that `/ALLUSERS /DIR=<user-writable folder>` would place a
service executable in an untrusted directory was **retracted after verification**:
`installer.iss` PrepareToInstall already restricts the server installation to
`{autopf}\\KI-Basis` before lifecycle calls. The installer agent is adding the same
restriction to the helper plus a negative native CI check. It should remain a
release gate, not an open P1 against the current installer entry point.

## Additional verification concern

`desktopCommand` initially resolves os.UserConfigDir before parsing an explicit
`--dir`. On Windows Go resolves that from `%AppData%`; service accounts must not
require an interactive user profile when an absolute service data path is already
provided. Resolve the default only when needed, and let native SCM CI verify
service-account startup.

## Tests independently run

- `go test -race ./cmd/ki-basis -run 'TestDesktop|TestRuntime' -count=1 -timeout=120s`
  passed (3.227 seconds).
- Real Playwright browser check ran and failed the accessibility assertions above;
  the failure is not hidden or treated as success.
- Read runtime downloader and fixtures: pinned official asset URL/hash/size,
  bounded HTTPS redirects, checksum before extraction/launch, private extraction,
  cancellation cleanup and preservation of previous runtime are coherent. No
  arbitrary runtime URL from the browser or central plan was found.
- Read application access backend/tests: distinct nonadmin model-scoped credential,
  no polling-status export, durable authoritative key-set checks and revocation
  persistence are present. The interrupted-init case remains the gap.

## Remaining product evidence

Native Windows installer compilation, actual per-user/server install, upgrade,
service account ACL, service restart and uninstall must pass the new workflow.
An unsigned artifact must remain labeled as such. Real target Windows reboot
without login, interactive UAC/SmartScreen experience and actual large-model
inference remain distinct from cross-compilation and synthetic browser fixtures.
