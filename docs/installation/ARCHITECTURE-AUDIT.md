# Installation architecture audit

Reviewed 2026-11-14. Repository `/workspace/LocalAIServerModul`. No AGENTS.md exists in checkout or workspace. Main is 84cc728; parent discovered and selected `origin/feat/requirements-v2` at 948e674 for implementation because main lacks already-developed protocol and service work. All recommendations below account for that branch. This report changes no product code.

## Current user journey and reusable foundations

| Capability | Existing path | Assessment |
|---|---|---|
| Small dependency-free Go binary + embedded web UI | `cmd/ki-basis/main.go`, `internal/webui/webui.go` | Strong foundation: do not add Electron/Node installation prerequisite. |
| GUI executable without console | `.github/workflows/pilot-packages.yml` builds `KI-Basis-Manager.exe` using windowsgui | Exists, but no-arguments dispatch is agent-only wizard. It cannot create the local central automatically. |
| Automatic local CA, configuration, dashboard, authentication | `cmd/ki-basis/quickstart.go` | Reuse rather than duplicate PKI/key lifecycle. Existing configuration and rotated credentials are authoritative. Local browser login uses fragment, no persisted browser storage. |
| Network central creation + separate app/admin credentials | requirements-v2 `cmd/ki-basis/central_init.go` | Reuse for an explicit advanced network/server mode; private hostname is currently a CLI requirement. Preserve existing identity and never silently regenerate CA. |
| Agent state + setup session reuse | `agentui.go`, `setup_session.go`, `bootstrap_lock_*.go` | Strong reusable protections: directory lock, reopen existing authenticated session, retained enrollment. Central quickstart lacks equivalent single-instance session reopening. |
| Checked enrollment | `internal/control/admin.go`, `bootstrap.go`, `agentui.go` | Short-lived one-time bundle, mTLS/CA and host checks. For same-host use, generate and consume internally; never require save/download/open of JSON or expose CA private key. Keep remote file transfer available. |
| Runtime archive import | `cmd/ki-basis/runtime_install.go` | Traversal/link/size protections, private versioned extraction, --version probe and library environment. Reuse extraction. No automatic trusted runtime download. Existing HTTP import holds `localAgent.mu` for extraction/probe; do not hold lock throughout a long network download. |
| Model catalogue/download/start/progress | `internal/catalog/*`, `internal/agent/models.go`, dashboard `app.js` | Reuse model flows and immutable digest verification. Runtime downloads need separate operator-approved catalogue; never take executable URL/hash from a model plan or arbitrary client. |
| Windows service primitives | requirements-v2 `service_windows.go`, `service_context.go`, `packaging/windows/Install-Services.ps1` | SCM virtual accounts, automatic start and crash recovery exist. Script creates services, grants Modify ACL to data, intentionally does not start. No GUI installer, uninstall or safe upgrade. Command allowlist excludes quickstart. SCM Running currently means run goroutine started, not application ready. |
| Windows user autostart | `autostart_windows.go` | HKCU Run executes agent on login, not server boot. Does not solve unattended Lightsail use. |
| Linux service support | `server_service_linux.go`, `packaging/systemd/*` | Existing explicit systemd path should remain. Do not claim Windows work enables other platforms automatically. |
| Distribution | `pilot-packages.yml`, `packaging/agent/Start-*.cmd` | ZIP nested in short-lived GitHub Actions artifact; launchers show console and pause. Need straightforward installer artifact + release-ready workflow. |

## Recommended minimum coherent architecture

1. Offer one Windows x64 Setup.exe. Use established installer technology (Inno Setup or WiX) for shortcuts, app registration, uninstall, upgrade and elevation instead of hand-writing shell UI. Program binaries in Program Files; unattended state in locked-down ProgramData. A user-mode choice can exist, but Windows Server default should be a system service that survives logout and reboot.
2. New top-level desktop launcher opens/reuses existing local onboarding. Explicit roles: recommended `KI auf diesem Rechner nutzen` (central + local agent), advanced existing central/remote agent. Same-host path provisions PKI, distinct admin/app keys, node enrollment and settings internally. User should only confirm install location/default storage and select/download model; no keys/JSON/certificate paths.
3. Separate browser/launcher lifetime from compute lifetime. A background runner owns central plus local agent; browser is a client and can close. Wrap runner with existing SCM adapter and terminate through `terminationContext`. Persist discoverable private UI session, authenticate reopen and prove expected state directory. Gate readiness on listeners and authenticated endpoint rather than PID existence or Windows service status.
4. Provide default CPU runtime catalogue entry with immutable version + upstream URL + independently pinned SHA-256 + expected platform. Display size/source/version; user approves download. Stream to staging with bounded disk/network budget, verify before extraction/execution, probe, atomically select binary/settings only after success. On failure preserve last runtime/settings. Progress, cancel and retry required. GPU remains advanced until tested; never silently replace drivers.
5. Completion must show actual `ready`/model loaded plus a nonempty test answer from exactly local node. Offer copyable application connection details/CA export only under connect application. Show explicit background operation semantics and state `service installed/running` only after verification.
6. Upgrade stops owned services, preserves state/models/CA/credentials, replaces binaries and restarts; installer failure must not delete data. Uninstall removes owned services/shortcuts/binaries, retains customer data by default, and names retained location. Never delete arbitrary selected directories from uninstaller.

## Implementation ownership boundaries

- **Packaging/service agent:** `packaging/windows/*`, installer build workflow, platform service/launcher helpers. Own binaries/data ACL, service install/start/query/stop/remove/update behaviour and native CI script. Coordinate the exact runner command/interface before edits.
- **Runtime agent:** new runtime catalogue/downloader/status Go files + tests, minimal integration hook exported to UI owner; avoid simultaneous edits of `agentui.go`. Expose start/status/cancel operations; keep trust data compile-time or separately authenticated.
- **Onboarding/primary:** top-level runner/launcher orchestration + `main.go` dispatch, `quickstart.go` extraction as necessary, web onboarding assets/handlers, same-host enrollment and readiness. Own shared routing/config transaction boundaries. Reuse remote advanced wizard untouched where possible.
- **Reviewer later:** read-only adversarial code/security/reliability review, usability rubric plus real browser journey. Reviewer should not implement their own requested fixes during review.

## Principal risks

- Running browser session as SCM virtual account makes session files unreadable to desktop launcher unless explicit least-privilege design exists. Do not grant all local users central secrets just to simplify login. Service-owned compute and user-specific management authentication need a deliberate rendezvous/access scheme. A localhost port alone is not authentication.
- Existing service adapter accepts only serve/agent-ui/agent. Whitelisting new runner command must validate absolute directories. Browser must never launch from service Session 0.
- No currently managed Windows target in this Linux environment. Cross-compilation validates types only, not installation, SCM account ACLs, uninstall or logout/reboot. Native Windows CI/VM evidence must be required and unresolved evidence must cap rating.
- Existing quickstart local frontend prints admin key in stdout. An unattended runner must suppress that setup-only output, avoid credential-bearing logs, and provide authenticated desktop reopening.
- Auto selecting public non-loopback listener without user acknowledgement broadens attack surface and TLS host requirements; keep default loopback and network setup explicit.
- Port changes must update TLS origin/bundles consistently. A free-port probe followed by reopen races; hold listeners or surface conflict with retry rather than claim success.
- A runtime --version success is not model/GPU capability proof. A configured Windows service is not reboot proof. A completed download is not inference readiness.
- Package signing requires actual signing identity. Do not add fake publisher/signature claims. Unsigned setup cannot honestly promise Ollama-equivalent Windows trust experience.

## Verification and suggested stop criteria

- Isolated temporary-directory integration: fresh local install, same identity on restart, duplicate launcher reuse, preservation of rotated credentials, process stop/restart, no orphan model child.
- Runtime HTTP fixtures: progress/content length absent/oversize/truncated/timeout/cancel/hash mismatch/wrong archive/platform rejection/retry, previous runtime preserved, path/link attacks.
- Browser: zero terminal steps/default path; no JSON or API-key handling; reload resumes; long downloads have clear progress and cancel/retry; initial failure actionable; completion waits for real result; narrow viewport/keyboard navigation.
- Native Windows CI: installer and uninstaller built and run silently, shortcuts and registration exist, real SCM starts with service identity, close browser/logout independence, service restart and reboot acceptance recorded separately, repair/update retain sentinel credentials/models, uninstall retains data and removes service. Re-run test only after meaningful changes.
- Gates for credible >=9/10: fresh Windows installation without manual commands; trusted runtime bundled/downloaded; no active desktop session needed; recoverable upgrade/uninstall; independent review no high-severity issues; native target evidence. Missing signing/native target reboot evidence must remain explicit release limitation, never averaged away by a subjective score.
