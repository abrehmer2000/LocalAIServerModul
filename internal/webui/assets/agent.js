"use strict";
(() => {
  const token = location.hash.slice(1);
  history.replaceState(null, "", location.pathname);
  const $ = (id) => document.getElementById(id);
  let step = 0,
    bundle = null,
    connected = false,
    checked = false,
    populated = false;
  const messages = {
    connection_file_expired:
      "Die Verbindungsdatei ist abgelaufen. In der Zentrale eine neue Datei erstellen und hier auswählen.",
    invalid_connection_file:
      "Die Datei ist keine gültige KI-Basis-Verbindungsdatei.",
    enrollment_failed_check_address_certificate_and_code:
      "Anmeldung fehlgeschlagen. Adresse, Zertifikat und Gültigkeit der Verbindungsdatei prüfen.",
    runtime_archive_invalid:
      "Das Laufzeitpaket ist ungültig oder wird nicht unterstützt. Ein vollständiges llama.cpp-ZIP- oder TAR.GZ-Paket auswählen.",
    runtime_incompatible:
      "Die Laufzeit startet nicht. Paket für dieses Betriebssystem, die Architektur und vorhandene Treiber auswählen.",
    runtime_upload_failed:
      "Paket konnte nicht übertragen werden (maximal 1 GB).",
    management_audit_unavailable:
      "Die Zentrale ist nicht erreichbar oder kann die Änderung nicht protokollieren. Verbindung prüfen und erneut versuchen.",
    engine_binary_not_found:
      "llama-server wurde am angegebenen Pfad nicht gefunden.",
    existing_model_not_found:
      "Die vorhandene Modelldatei wurde nicht gefunden.",
    already_connected: "Dieser Rechner ist bereits angemeldet.",
    autostart_unavailable_use_service_installation:
      "Automatischer Start ist hier nicht verfügbar. Für diesen Server ist ein Systemdienst erforderlich.",
  };
  async function api(path, body) {
    const response = await fetch(path, {
      method: body ? "POST" : "GET",
      headers: { "X-Setup-Token": token, "Content-Type": "application/json" },
      body: body ? JSON.stringify(body) : undefined,
      cache: "no-store",
    });
    if (response.status === 401)
      throw Error(
        "Einrichtungszugang fehlt oder ist abgelaufen. Den lokalen Startlink erneut öffnen.",
      );
    const data = await response.json();
    if (!response.ok)
      throw Error(
        messages[data.error] ||
          "Aktion fehlgeschlagen. Angaben prüfen und erneut versuchen.",
      );
    return data;
  }
  function settings() {
    return {
      binary: $("binary").value.trim(),
      engine: $("engine").value.trim(),
      models: $("models").value.trim(),
      existing_model: $("existing-model").value.trim(),
    };
  }
  function show(next) {
    step = next;
    document
      .querySelectorAll("[data-agent-step]")
      .forEach(
        (panel) => (panel.hidden = Number(panel.dataset.agentStep) !== step),
      );
    [...$("agent-steps").children].forEach((item, index) => {
      item.classList.toggle("complete", index < step);
      if (index === step) item.setAttribute("aria-current", "step");
      else item.removeAttribute("aria-current");
    });
    $("agent-back").hidden = step === 0 || step === 4;
    $("agent-next").hidden = step >= 3;
    $("agent-next").disabled = step === 2 && !checked;
    $("agent-next").textContent =
      ["Weiter zur Laufzeit", "Weiter zur Prüfung", "Weiter zur Anmeldung"][
        step
      ] || "Weiter";
    $("save-settings").hidden = !(connected && step === 1);
    $("operation-panel").hidden = !connected;
    $("result").textContent = "";
  }
  function displaySettings(s) {
    for (const [id, key] of [
      ["binary", "binary"],
      ["engine", "engine"],
      ["models", "models"],
      ["existing-model", "existing_model"],
    ])
      $(id).value = s[key] || "";
    $("runtime-note").textContent = s.binary
      ? "Vorhandene Laufzeit erkannt. Beim nächsten Schritt wird ihr Start geprüft."
      : "Noch keine Laufzeit erkannt. Ein passendes Paket importieren oder unter Erweitert eine vorhandene Installation auswählen.";
  }
  async function status() {
    try {
      const s = await api("/setup/status");
      connected = s.connected;
      $("restart-mode").textContent = {
        systemd:
          "Systemdienst eingerichtet: Start nach Serverneustart und Neustart bei Prozessfehlern konfiguriert. Einen echten Serverneustart im Betrieb testen.",
        systemd_pending:
          "Systemdienst-Einrichtung noch nicht bestätigt. Im SSH-Assistenten sicher fortsetzen; keinen zweiten Hintergrundprozess starten.",
        manual:
          "Hintergrundbetrieb: Nach einem Serverneustart muss der Agent erneut gestartet werden. Für Container einen Neustartmechanismus in der Infrastruktur einrichten.",
        unknown:
          "Automatischer Serverstart ist nicht bestätigt. Benutzer-Autostart unten ersetzt keinen Systemdienst.",
      }[s.restart_mode || "unknown"];
      $("tunnel-panel").hidden = !s.tunnel?.state;
      if (s.tunnel?.state) {
        $("tunnel-state").textContent =
          {
            connected: "SSH-Verbindung hergestellt.",
            connecting: "Verbindung wird aufgebaut …",
            reconnecting: "Verbindung unterbrochen. Wird erneut aufgebaut …",
            disconnected: "Verbindung getrennt.",
          }[s.tunnel.state] || s.tunnel.state;
        $("open-central").hidden = s.tunnel.state !== "connected";
        if (s.tunnel.browser?.startsWith("http://127.0.0.1:"))
          $("open-central").href = s.tunnel.browser;
      }
      $("status").textContent = connected
        ? "Verbunden: " +
          s.node_id +
          "\nModellstatus: " +
          (s.report.state || "idle")
        : "Noch nicht verbunden.";
      $("machine-label").textContent =
        "Agent-Einrichtung · " +
        location.host +
        " · Änderungen gelten auf dem Rechner dieses Agenten.";
      if (!populated) {
        displaySettings(s.settings);
        populated = true;
        if (connected) show(4);
      }
      $("connected-summary").textContent = connected
        ? "Servername: " + s.node_id
        : "";
      $("operation-panel").hidden = !connected;
    } catch (error) {
      $("status").textContent = error.message;
      $("result").textContent = error.message;
    }
  }
  $("bundle").addEventListener("change", async () => {
    bundle = null;
    checked = false;
    try {
      const file = $("bundle").files[0];
      if (!file) return;
      if (file.size > 65536) throw Error("Verbindungsdatei ist zu groß.");
      const value = JSON.parse(await file.text());
      if (
        value.version !== 1 ||
        !value.node_id ||
        !value.token ||
        !value.ca ||
        !Number.isFinite(Date.parse(value.expires_at))
      )
        throw Error("Keine gültige Verbindungsdatei.");
      if (Date.parse(value.expires_at) <= Date.now())
        throw Error(messages.connection_file_expired);
      bundle = value;
      $("bundle-info").textContent =
        "Server: " +
        value.node_id +
        " · Zentrale: " +
        value.gateway +
        " · gültig bis " +
        new Date(value.expires_at).toLocaleTimeString("de-DE");
    } catch (error) {
      $("bundle-info").textContent = error.message;
    }
  });
  $("agent-next").addEventListener("click", () => {
    if (step === 0 && !bundle) {
      $("bundle-info").textContent =
        "Bitte zuerst eine gültige Verbindungsdatei auswählen.";
      $("bundle").focus();
      return;
    }
    if (step === 2 && !checked) return;
    if (step === 1) checked = false;
    if (step === 2)
      $("connect-summary").textContent =
        "Server „" + bundle.node_id + "“ bei " + bundle.gateway + " anmelden.";
    show(step + 1);
  });
  $("agent-back").addEventListener("click", () =>
    show(connected ? 4 : Math.max(0, step - 1)),
  );
  $("edit-settings").addEventListener("click", () => show(1));
  $("advanced-settings").addEventListener("input", () => {
    checked = false;
  });
  $("run-checks").addEventListener("click", async () => {
    checked = false;
    $("agent-next").disabled = true;
    $("run-checks").disabled = true;
    $("setup-checks").replaceChildren();
    $("result").textContent = "Rechner und sichere Verbindung werden geprüft …";
    try {
      const report = await api("/setup/check", {
        settings: settings(),
        ...(bundle && !connected ? { bundle } : {}),
      });
      for (const check of report.checks) {
        const li = document.createElement("li"),
          title = document.createElement("strong"),
          text = document.createElement("span");
        title.textContent = {
          ok: "✓ In Ordnung",
          warning: "! Hinweis",
          error: "× Aktion erforderlich",
        }[check.state];
        text.textContent = check.message;
        li.append(title, text);
        $("setup-checks").append(li);
      }
      checked = report.ready;
      $("agent-next").disabled = !checked;
      $("run-checks").classList.toggle("secondary", checked);
      $("result").textContent = checked
        ? "Prüfung abgeschlossen. Du kannst fortfahren."
        : "Bitte die markierten Punkte beheben. Mit Zurück kannst du die Laufzeit ändern; danach erneut prüfen.";
    } catch (error) {
      $("result").textContent = error.message;
    } finally {
      $("run-checks").disabled = false;
    }
  });
  $("runtime-archive").addEventListener("change", () => {
    $("runtime-import").disabled = !$("runtime-archive").files.length;
  });
  $("runtime-import").addEventListener("click", async () => {
    const file = $("runtime-archive").files[0];
    if (!file) return;
    $("runtime-import").disabled = true;
    $("runtime-result").textContent = "Paket wird importiert und geprüft …";
    try {
      const response = await fetch("/setup/runtime", {
        method: "POST",
        headers: {
          "X-Setup-Token": token,
          "Content-Type": "application/octet-stream",
        },
        body: file,
      });
      const data = await response.json();
      if (!response.ok)
        throw Error(messages[data.error] || "Import fehlgeschlagen.");
      $("binary").value = data.binary;
      checked = false;
      $("runtime-result").textContent =
        "Laufzeit importiert und erfolgreich gestartet. Weiter zur Prüfung.";
    } catch (error) {
      $("runtime-result").textContent = error.message;
    } finally {
      $("runtime-import").disabled = false;
    }
  });
  $("enroll-form").addEventListener("submit", async (e) => {
    e.preventDefault();
    if (step !== 3 || !checked || !bundle) return;
    $("submit").disabled = true;
    try {
      await api("/setup/connect", { bundle, settings: settings() });
      bundle = null;
      $("bundle").value = "";
      await status();
      show(4);
      $("result").textContent =
        "Verbunden. Weiter geht es im zentralen Dashboard.";
    } catch (error) {
      $("result").textContent = error.message;
    } finally {
      $("submit").disabled = false;
    }
  });
  $("save-settings").addEventListener("click", async () => {
    $("save-settings").disabled = true;
    try {
      const report = await api("/setup/check", { settings: settings() });
      if (!report.ready)
        throw Error(
          report.checks
            .filter((c) => c.state === "error")
            .map((c) => c.message)
            .join(" "),
        );
      await api("/setup/settings", settings());
      show(4);
      $("result").textContent =
        "Einstellungen gespeichert. Agent neu gestartet.";
    } catch (error) {
      $("result").textContent = error.message;
    } finally {
      $("save-settings").disabled = false;
    }
  });
  for (const [id, enabled] of [
    ["autostart-enable", true],
    ["autostart-disable", false],
  ])
    $(id).addEventListener("click", async () => {
      try {
        await api("/setup/autostart", { enabled });
        $("autostart-result").textContent = enabled
          ? "Autostart bei Benutzeranmeldung eingerichtet."
          : "Autostart entfernt.";
      } catch (error) {
        $("autostart-result").textContent = error.message;
      }
    });
  $("backup-agent").addEventListener("click", async () => {
    try {
      const response = await fetch("/setup/backup", {
        headers: { "X-Setup-Token": token },
        cache: "no-store",
      });
      if (!response.ok) throw Error("Sicherung fehlgeschlagen.");
      const url = URL.createObjectURL(await response.blob());
      const link = document.createElement("a");
      link.href = url;
      link.download = "ki-basis-agent-backup.zip";
      link.click();
      setTimeout(() => URL.revokeObjectURL(url), 60000);
      $("backup-result").textContent =
        "Sicherung heruntergeladen. Vertraulich aufbewahren.";
    } catch (error) {
      $("backup-result").textContent = error.message;
    }
  });
  $("restore-agent").addEventListener("click", async () => {
    const file = $("restore-file").files[0];
    if (!file || !$("restore-confirm").checked) {
      $("restore-result").textContent =
        "Sicherung auswählen und bestätigen, dass der bisherige Agent ausgeschaltet ist.";
      return;
    }
    $("restore-agent").disabled = true;
    try {
      const response = await fetch("/setup/restore", {
        method: "POST",
        headers: { "X-Setup-Token": token, "Content-Type": "application/zip" },
        body: file,
      });
      if (!response.ok)
        throw Error(
          "Sicherung ungültig oder dieser Agent ist bereits angemeldet.",
        );
      await status();
      show(4);
      $("result").textContent =
        "Agent wiederhergestellt. Netzwerkverbindung und Laufzeit vor dem Modellstart prüfen.";
    } catch (error) {
      $("restore-result").textContent = error.message;
    } finally {
      $("restore-agent").disabled = false;
    }
  });
  let remoteStep = 0,
    fingerprint = "",
    checkedTarget = "";
  function target() {
    return {
      host: $("ssh-host").value.trim(),
      port: Number($("ssh-port").value),
      user: $("ssh-user").value.trim(),
      key: $("ssh-key").value.trim(),
    };
  }
  function showRemote(value) {
    remoteStep = value;
    document
      .querySelectorAll("[data-remote-step]")
      .forEach(
        (panel) => (panel.hidden = Number(panel.dataset.remoteStep) !== value),
      );
    [...$("remote-steps").children].forEach((item, index) => {
      item.classList.toggle("complete", index < value);
      if (index === value) item.setAttribute("aria-current", "step");
      else item.removeAttribute("aria-current");
    });
    $("ssh-back").hidden = value === 0;
  }
  $("choose-local").addEventListener("click", () => {
    $("local-panel").hidden = false;
    $("remote-panel").hidden = true;
  });
  $("choose-ssh").addEventListener("click", () => {
    $("local-panel").hidden = true;
    $("remote-panel").hidden = false;
  });
  const sshErrors = {
    agent_storage_unavailable:
      "Der Agent-Datenordner ist nicht beschreibbar oder unterstützt die erforderliche Prozesssperre nicht. Einen geeigneten lokalen, dauerhaften Datenträger und Zugriffsrechte bereitstellen.",
    agent_identity_missing:
      "Auf dem Zielserver ist noch keine Identität vorhanden. ‚Vorhandene Einrichtung fortsetzen‘ ausschalten und mit einer gültigen Verbindungsdatei installieren.",
    ssh_install_result_invalid:
      "Der Server hat keinen bestätigten Abschluss geliefert. Den Serverstatus in der Zentrale prüfen und mit derselben Identität fortsetzen; nicht neu anmelden.",
    agent_setup_in_progress:
      "Eine Einrichtung läuft bereits auf diesem Server. Kurz warten und erneut versuchen.",
    agent_identity_already_exists:
      "Dieser Server ist bereits angemeldet. Unten ‚Vorhandene Einrichtung fortsetzen‘ wählen und dieselbe Verbindungsdatei verwenden.",
    agent_identity_mismatch:
      "Die vorhandene Identität gehört nicht zu diesem Servereintrag oder dieser Zentrale. Es wurde nichts überschrieben. Den passenden Servereintrag in der Zentrale auswählen.",
    agent_identity_unreadable:
      "Die vorhandene Identität ist nicht lesbar. Zugriffsrechte prüfen; nicht durch eine neue Anmeldung ersetzen.",
    agent_settings_invalid:
      "Die gespeicherten Einstellungen sind beschädigt. Eine geprüfte Agent-Sicherung wiederherstellen.",
    agent_enrollment_failed:
      "Anmeldung nicht bestätigt. Den Serverstatus in der Zentrale prüfen, bevor eine neue Verbindungsdatei erstellt wird.",
    agent_service_unavailable_or_failed:
      "Systemdienst konnte nicht eingerichtet werden. Die Anmeldung bleibt erhalten. Bei Linux mit systemd und Root-Zugang ‚Fortsetzen‘ versuchen. Sonst bewusst Hintergrundbetrieb wählen; dieser startet nach einem Serverneustart nicht automatisch.",
    agent_service_start_failed:
      "Systemdienst antwortet noch nicht. Nach kurzer Wartezeit mit derselben Identität fortsetzen. Keinen zusätzlichen Hintergrundprozess starten.",
    agent_background_start_failed:
      "Der Agent konnte nicht im Hintergrund gestartet werden. Mit ‚Vorhandene Einrichtung fortsetzen‘ erneut versuchen; die Identität bleibt erhalten.",
    setup_checks_failed:
      "Bitte die folgenden Prüfungen auf dem Zielserver korrigieren und erneut versuchen.",
    invalid_ssh_target:
      "Bitte Serveradresse, Benutzername, Port und einen absoluten Schlüsselpfad prüfen.",
    ssh_host_unreachable:
      "Server nicht erreichbar oder ssh-keyscan fehlt auf diesem Rechner. Adresse und SSH-Port prüfen.",
    ssh_host_key_changed:
      "Serveridentität hat sich geändert. Fingerabdruck erneut prüfen.",
    ssh_auth_failed_unlock_key:
      "SSH-Anmeldung fehlgeschlagen. Zugang prüfen und einen verschlüsselten Schlüssel im SSH-Agenten deines Betriebssystems entsperren.",
    ssh_linux_required:
      "Dieser SSH-Assistent unterstützt Linux. Für Windows das lokale Agent-Paket verwenden.",
    ssh_architecture_unsupported:
      "Diese Prozessorarchitektur wird vom SSH-Assistenten noch nicht unterstützt.",
    ssh_agent_package_missing:
      "Das Agent-Paket für die Zielarchitektur fehlt im Unterordner agents des Assistenten. Das vollständige Manager-Paket verwenden.",
    ssh_install_failed_check_target:
      "Installation konnte nicht abgeschlossen werden. Laufzeit, Netzwerkverbindung zur Zentrale und vorhandene Agent-Installation auf dem Zielserver prüfen. Eine bereits erfolgte Anmeldung zuerst in der Zentrale kontrollieren.",
    runtime_archive_not_found:
      "Laufzeitpaket am angegebenen lokalen Pfad nicht gefunden. ZIP oder TAR.GZ auswählen.",
  };
  async function remoteAPI(path, body) {
    const response = await fetch(path, {
      method: "POST",
      headers: { "X-Setup-Token": token, "Content-Type": "application/json" },
      body: JSON.stringify(body),
    });
    const data = await response.json();
    if (!response.ok)
      throw Error(
        (sshErrors[data.error] ||
          messages[data.error] ||
          "SSH-Einrichtung fehlgeschlagen.") +
          (Array.isArray(data.checks)
            ? "\n" + data.checks.map((check) => check.message).join("\n")
            : ""),
      );
    return data;
  }
  for (const [button, kind, field] of [
    ["pick-ssh-key", "key", "ssh-key"],
    ["pick-ssh-runtime", "runtime", "ssh-runtime"],
  ])
    $(button).addEventListener("click", async () => {
      $(button).disabled = true;
      try {
        const value = await api("/setup/pick-file", { kind });
        $(field).value = value.path;
      } catch {
        $("ssh-result").textContent =
          "Dateiauswahl abgebrochen oder auf diesem Rechner nicht verfügbar. Bei Bedarf den vollständigen Pfad eintragen.";
      } finally {
        $(button).disabled = false;
      }
    });
  $("unlock-ssh-key").addEventListener("click", async () => {
    if (!$("ssh-key").value.trim()) {
      $("ssh-result").textContent = "Zuerst eine Schlüsseldatei auswählen.";
      return;
    }
    $("unlock-ssh-key").disabled = true;
    try {
      const value = await api("/setup/ssh/unlock", {
        key: $("ssh-key").value.trim(),
      });
      $("ssh-result").textContent = value.message;
    } catch {
      $("ssh-result").textContent =
        "Schlüssel konnte nicht entsperrt werden. SSH-Agent und native Dialogunterstützung des Betriebssystems prüfen. Unter Windows einen bereits entsperrten SSH-Agenten verwenden.";
    } finally {
      $("unlock-ssh-key").disabled = false;
    }
  });
  $("ssh-check").addEventListener("click", async () => {
    for (const id of ["ssh-host", "ssh-port", "ssh-user"])
      if (!$(id).reportValidity()) return;
    $("ssh-check").disabled = true;
    $("ssh-result").textContent = "Serveridentität wird abgerufen …";
    try {
      const t = target();
      const result = await remoteAPI("/setup/ssh/check", t);
      fingerprint = result.fingerprint;
      checkedTarget = JSON.stringify(t);
      $("ssh-fingerprint").textContent = fingerprint;
      $("ssh-trust").checked = false;
      showRemote(1);
      $("ssh-result").textContent = result.message;
    } catch (error) {
      $("ssh-result").textContent = error.message;
    } finally {
      $("ssh-check").disabled = false;
    }
  });
  $("ssh-confirm").addEventListener("click", () => {
    if (!$("ssh-trust").checked) {
      $("ssh-trust").focus();
      return;
    }
    showRemote(2);
    $("ssh-result").textContent = "";
  });
  $("ssh-back").addEventListener("click", () =>
    showRemote(Math.max(0, remoteStep - 1)),
  );
  $("ssh-role").addEventListener("change", () => {
    const central = $("ssh-role").value === "central";
    $("remote-agent-options").hidden = central;
    $("remote-central-options").hidden = !central;
    $("ssh-install").textContent = central
      ? "Zentrale verbinden"
      : "Agent installieren und verbinden";
  });
  $("reconnect-tunnel").addEventListener("click", async () => {
    try {
      await api("/setup/tunnel/reconnect", {});
      await status();
    } catch (error) {
      $("tunnel-state").textContent = error.message;
    }
  });
  $("disconnect-tunnel").addEventListener("click", async () => {
    try {
      await api("/setup/tunnel/disconnect", {});
      await status();
    } catch (error) {
      $("tunnel-state").textContent = error.message;
    }
  });
  $("ssh-network").addEventListener("change", () => {
    $("ssh-network-help").textContent =
      $("ssh-network").value === "tunnel"
        ? "Der Tunnel muss auf dem Zielserver laufen und nach dessen Neustart wieder starten. Der Manager-Tunnel verbindet nur deinen Rechner mit der Zentrale. Die Verbindung wird vor der Anmeldung auf dem Zielserver geprüft."
        : "Der Zielserver muss die HTTPS-Adresse selbst erreichen, direkt oder über ein bereits eingerichtetes VPN. Zertifikate werden geprüft. Netzwerk und Firewall werden nicht automatisch verändert.";
  });
  $("remote-form").addEventListener("submit", async (event) => {
    event.preventDefault();
    if (remoteStep !== 2) return;
    if (JSON.stringify(target()) !== checkedTarget) {
      showRemote(0);
      $("ssh-result").textContent =
        "Zugang wurde geändert. Serveridentität erneut prüfen.";
      return;
    }
    $("ssh-install").disabled = true;
    $("ssh-back").disabled = true;
    try {
      if ($("ssh-role").value === "central") {
        const p = {
          target: target(),
          fingerprint,
          web_port: Number($("ssh-web-port").value),
          gateway_port: Number($("ssh-gateway-port").value),
        };
        await remoteAPI("/setup/tunnel", p);
        $("ssh-result").textContent =
          "Verbindung wird aufgebaut. Unten erscheint der Link zur Zentrale.";
        await status();
        return;
      }
      const file = $("ssh-bundle").files[0];
      if (!file || file.size > 65536)
        throw Error("Eine gültige Verbindungsdatei auswählen.");
      const bundle = JSON.parse(await file.text());
      $("ssh-result").textContent =
        "System wird geprüft, Agent wird übertragen und angemeldet. Das kann einige Minuten dauern …";
      const result = await remoteAPI("/setup/ssh/install", {
        target: target(),
        fingerprint,
        bundle,
        runtime_archive: $("ssh-runtime").value.trim(),
        service: $("ssh-service").checked,
        resume: $("ssh-resume").checked,
      });
      $("ssh-result").textContent = result.message;
      // Keep the selected file for a safe retry; it is never persisted by the browser.
    } catch (error) {
      $("ssh-result").textContent = error.message;
    } finally {
      $("ssh-install").disabled = false;
      $("ssh-back").disabled = false;
    }
  });
  $("restore-central").addEventListener("click", async () => {
    const file = $("central-restore-file").files[0];
    const directory = $("central-restore-dir").value.trim();
    if (!file || !directory || !$("central-restore-confirm").checked) {
      $("central-restore-result").textContent =
        "Sicherung und leeren Zielordner auswählen und bestätigen, dass die alte Zentrale ausgeschaltet ist.";
      return;
    }
    $("restore-central").disabled = true;
    try {
      const response = await fetch("/setup/restore-central", {
        method: "POST",
        headers: {
          "X-Setup-Token": token,
          "X-Restore-Destination": encodeURIComponent(directory),
          "X-Restore-Confirmed": "yes",
        },
        body: file,
      });
      const data = await response.json();
      if (!response.ok)
        throw Error(
          data.error === "restore_destination_must_be_empty"
            ? "Der Zielordner enthält Daten. Einen anderen leeren Ordner auswählen. Es wurde nichts überschrieben."
            : "Sicherung konnte nicht wiederhergestellt werden. KI-Basis-Zentralen-ZIP, Prüfsummen, Audit-Kette, Zugriffsrechte und Zielordner prüfen.",
        );
      $("central-restore-result").textContent =
        data.message + " Ziel: " + data.directory;
    } catch (error) {
      $("central-restore-result").textContent = error.message;
    } finally {
      $("restore-central").disabled = false;
    }
  });
  showRemote(0);
  $("quit-manager").addEventListener("click", async () => {
    if (
      !confirm(
        "KI-Basis auf dem Rechner dieses Agenten beenden? Modellbetrieb und verwaltete SSH-Verbindungen werden beendet. Cloud-Server laufen weiter.",
      )
    )
      return;
    try {
      await api("/setup/quit", {});
      $("quit-result").textContent =
        "Programm wird beendet. Dieses Browserfenster kann geschlossen werden.";
    } catch (error) {
      $("quit-result").textContent = error.message;
    }
  });
  show(0);
  // Exchange the one-time browser fragment for a same-site HttpOnly session cookie;
  // no key/token is written to localStorage or sessionStorage.
  (async () => {
    try {
      if (token) await api("/setup/session", {});
    } catch {}
    await status();
  })();
  setInterval(status, 5000);
})();
