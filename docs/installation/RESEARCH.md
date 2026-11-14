# Installation research for KI-Basis

Research date: 2026-11-14. Primary sources only. This is a design recommendation, not a claim that those products or this implementation have been manually tested here.

## Findings and transferable lessons

| Reference | Verified behavior | Lesson for KI-Basis |
|---|---|---|
| [Ollama Windows](https://docs.ollama.com/windows) | Native setup EXE, per-user installation without administrator privileges, background operation, installed CLI, Windows Settings uninstaller. Binaries and models have separate locations. Standalone ZIP is offered for service integration. Listed OS requirement is Windows 10 22H2 Home/Pro or newer. | One obvious download and one obvious launcher; no Go/Python/Docker or archive extraction for ordinary users. Keep program files separate from durable data. Do not imply that Ollama's desktop background mode proves logout-safe Windows Server operation. |
| [LM Studio getting started](https://lmstudio.ai/docs/app/basics) | Install application, discover curated models, download a model, load it, chat. Advanced load configuration is optional. | Offer a recommended tested default; hide engine paths, ports and expert configuration behind an advanced section. Distinguish downloaded from loaded and ready. |
| [LM Studio runtime management](https://lmstudio.ai/docs/cli/runtime/runtime), [offline operation](https://lmstudio.ai/docs/app/offline) | Runtime listing/downloading/switching/removal is managed by the application tooling; core use can work offline once required files exist. | The application owns runtime acquisition. The user should not hunt GitHub release assets. Preserve manual archive import for disconnected or operator-controlled environments. Separate application, engine and model lifecycle. |
| [Tailscale Windows](https://tailscale.com/docs/install/windows) | EXE installer chooses appropriate architecture; Windows Server 2016+ is explicitly supported. A tray menu exposes configuration/status and launches browser authentication. | A small native launcher can open a browser UI without leaving terminal windows. Publish a specific supported-OS matrix rather than extrapolating desktop success to Server. |
| [Tailscale unattended mode](https://tailscale.com/docs/how-to/run-unattended) | Explicit unattended choice persists after logout/restart; verification requires logout/reboot and a remote reachability check. | Server mode is a product feature with an observable status, not merely a hidden process. Offer it explicitly and prove its lifecycle. Closing the browser must not stop the server. |
| [Docker Desktop Windows](https://docs.docker.com/desktop/setup/install/windows-install/) | Distinguishes per-user versus all-user installation, paths, privileges and backends. Docker Desktop is unsupported on Windows Server. | Separate desktop convenience from server installation requirements. Do not introduce Docker Desktop/WSL as a prerequisite on Lightsail Windows Server; that would add complexity and an unsupported dependency. |

The design inference from these products is not “put the existing script behind a button.” The professional pattern is a stable product entry point that owns prerequisites, defaults, progress, recovery, restart behavior, upgrade and uninstall. Installation ends only when the selected capability works.

## Windows packaging and operational requirements

[Microsoft Windows Installer best practices](https://learn.microsoft.com/en-us/windows/win32/msi/windows-installer-best-practices) recommends native installer functionality rather than unnecessary custom actions. [Rollback installation](https://learn.microsoft.com/en-us/windows/win32/msi/rollback-installation) describes automatic rollback and explicitly notes that custom actions require additional rollback authoring. Therefore a setup must not claim transactional behavior merely because files are packaged: service creation, ACL changes and data migration need corresponding failure handling.

[ServiceInstall](https://learn.microsoft.com/en-us/windows/win32/msi/serviceinstall-table) provides system-start service configuration; its ServiceControl counterpart must also cover removal. [Service user accounts](https://learn.microsoft.com/en-us/windows/win32/services/service-user-accounts) establishes that services run in an account security context. For KI-Basis choose the least privileged workable service account and grant access only to owned data directories. Do not make the management web interface externally reachable just because the service runs without login.

[Microsoft signing guidance](https://learn.microsoft.com/en-us/windows/apps/package-and-deploy/code-signing-options) distinguishes publisher identity/integrity from reputation: a valid certificate does not instantly guarantee no SmartScreen prompt. Sign the installer and shipped executables with a stable publisher identity; keep credentials in release secrets, never source. Unsigned test artifacts must be labeled honestly. Do not tell users to disable security protections.

An EXE installer built with Inno Setup is a pragmatic small-project option; an MSI is advantageous for enterprise deployment but not itself necessary for a simple interactive experience. [Inno AppId](https://jrsoftware.org/inno-setup/documentation/topic_setup_appid.htm) and [FAQ](https://jrsoftware.org/inno-setup/faq) describe stable identity/install-directory handling across upgrades. [SignedUninstaller](https://jrsoftware.org/inno-setup/documentation/topic_setup_signeduninstaller.htm) covers signed removal binaries. Either packaging choice needs a tested install/upgrade/uninstall matrix. Pick one, do not build two parallel product paths for the first increment.

## Recommended user journey

1. Download a versioned Windows setup EXE from the project's release page. Display supported CPU/OS, publisher, application size and the separate model-download requirement. Keep Actions artifacts for developers.
2. Run setup. Standard path, Start-menu shortcut, Windows Settings uninstaller. Choose desktop or unattended server mode, with a plain-language explanation of when the application runs. Elevate only for machine-wide/server operations.
3. Launch KI-Basis. Automatically prepare the local central configuration, CA and credentials; authenticate the local management session without asking the user to open JSON files. Defaults stay loopback-only. Adding another machine is a separate explicit flow.
4. Choose the recommended model and displayed storage location. Present download size and hardware recommendation, and ask before downloading. Detect the platform, offer a pinned compatible runtime, and show download/verification/start stages. No external release browsing or copy/paste.
5. Automatically connect the local agent using the existing trusted local setup contract; no intermediate connection-file handling in the same-machine path. Retain a downloadable one-time connection file for a separate machine.
6. Show readiness and a real short generated response. Offer API connection details and a copy action afterwards. Background/reboot behavior is clearly shown separately from model readiness.
7. Relaunch reopens the same installation. Failed download, interrupted setup or occupied UI port must not generate another identity. Repair/retry stays in the same UI.

Updates should initially use the same installer with an explicit confirmation and maintenance window. Preserve PKI, configuration, audit and downloaded models; reject unsafe downgrades/data incompatibility. Automatic executable self-update is a separate trust/rollback feature and should not be bolted on within this UX improvement. Application uninstall should remove installed programs/startup registration while preserving data unless the user separately chooses verified data deletion.

## Download trust boundary

Automatic runtime acquisition is a new executable trust boundary. Use application-owned, versioned metadata mapping platform to an HTTPS asset and exact SHA-256, not arbitrary URLs supplied by remote agents/model plans. Resolve redirects with bounded policy, size/time limits, cancellation and explicit user initiation. Verify bytes before extraction/start; stage into a temporary owned directory and atomically promote only after extraction and executable validation succeed. Preserve the last working engine on failures. A digest fetched beside an untrusted binary is integrity metadata, not independent publisher authentication. Version pinning also needs a reviewed maintenance process and a documented compatibility test.

## Suggested review rubric: 100 points

| Area | Points | Evidence required |
|---|---:|---|
| Fresh-install simplicity | 20 | Clean supported Windows desktop/Server install from one artifact, no terminal, no JSON editing, no required ZIP extraction, no developer runtimes. Measure clicks and time excluding model transfer. |
| First useful inference | 20 | Recommended engine/model installed in-product; clear consent/progress; same-machine agent auto-connected; real response shown. |
| Lifecycle | 15 | Relaunch reuses state; UI close does not stop opted-in service; logout/reboot without login restores selected server roles. |
| Recovery and data safety | 15 | Interrupted/corrupt downloads, unavailable network, low disk, occupied ports and failed upgrade preserve existing working data; retry is usable. |
| Trust and local security | 15 | Digest mismatch blocks execution; bounded extraction; loopback/session protection; least privileges; no secrets in logs; signing status stated accurately. |
| Packaging and maintenance | 10 | Repeatable package build, upgrade preserves state, uninstall removes owned registrations, model retention documented. |
| Clarity and accessibility | 5 | Keyboard-usable flow, actionable errors, truthful readiness and lifecycle states, expert controls optional. |

9/10 means at least 90/100 **with evidence**, not reviewer optimism. A failed safety gate (arbitrary executable execution, leaked credentials, destructive upgrade, public unauthenticated management) prevents a pass regardless of numerical total. If real Windows install/reboot or publisher signing cannot be exercised, report implementation readiness separately and leave those production acceptance items unproven. Do not award “Ollama-equivalent, production-ready” based on Linux unit tests and cross-compilation alone.

## Repository-specific observations

Initial inspection of default `main` at `84cc728`: `SETUP-ASSISTENT.md` already documents a native manager launcher, staged agent flow, importable runtimes, safe resume and backups. `FIRST-SERVER.md` still leads through Actions artifacts and multiple terminals. `runtime_install.go` deliberately accepts operator-selected archives only; automatic runtime download therefore needs an explicit new policy rather than bypassing this guard. `autostart_windows.go` provides per-user registration, while that baseline setup document lists complete Windows services as missing.

The main agent subsequently selected the newer requirements branch at `948e674` as implementation base. This branch already contains `service_windows.go`, including automatic-start native services with virtual service accounts and absolute-path validation. Therefore reuse and integrate that service foundation rather than creating a second Windows service implementation. Installer/service ACLs, ownership, status and stop/recovery behavior still require validation. Highest-value work: package the existing capabilities into a single supported entry point, simplify same-machine setup, make trusted engine installation internal, integrate the existing unattended lifecycle into the normal UI, then replace conflicting first-run documentation.
