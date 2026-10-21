"use strict";
(() => {
  const byId = (id) => document.getElementById(id);
  let startupFragment = location.hash;
  if (startupFragment)
    history.replaceState(null, "", location.pathname + location.search);
  let apiKey = "";
  let userId = "";
  let loading = false;
  let session = 0;
  let bundleURL = "";
  let adminAllowed = false;
  let inferenceController = null;
  let setupStep = 0;
  let setupNode = "";
  let latestOverview = null;
  const gpuChoices = new Map();
  const pendingActions = new Set();
  const message = (text, error = false) => {
    byId("connection-message").textContent = text;
    byId("dashboard-message").textContent = text;
    byId("connection-message").classList.toggle("error", error);
  };
  async function health() {
    try {
      const response = await fetch("/healthz", {
        cache: "no-store",
        signal: AbortSignal.timeout(5000),
      });
      if (!response.ok) throw new Error();
      const data = await response.json();
      if (data.status !== "ok") throw new Error();
      byId("gateway-status").textContent = "Erreichbar";
      byId("gateway-note").textContent = "API-Prozess antwortet";
    } catch {
      byId("gateway-status").textContent = "Nicht erreichbar";
      byId("gateway-note").textContent = "Gateway-Verbindung prüfen";
    }
  }
  function clear() {
    document.body.classList.remove("connected-dashboard");
    byId("dashboard-nav").hidden = true;
    session += 1;
    apiKey = "";
    userId = "";
    gpuChoices.clear();
    pendingActions.clear();
    byId("api-key").value = "";
    byId("ready-count").textContent = "—";
    byId("model-count").textContent = "—";
    byId("node-note").textContent = "Zugang erforderlich";
    byId("nodes").replaceChildren();
    byId("nodes").className = "empty";
    byId("nodes").textContent =
      "Verbinde dich mit dem Gateway, um die registrierten Knoten zu sehen.";
    byId("refresh").disabled = true;
    if (inferenceController) inferenceController.abort();
    byId("test-panel").hidden = true;
    byId("test-answer").textContent = "";
    byId("test-prompt").value = "";
    byId("test-model").replaceChildren();
    byId("admin-access-note").textContent = "";
    byId("admin-tools").hidden = true;
    byId("audit-panel").hidden = true;
    byId("backup-panel").hidden = true;
    byId("managed-devices").replaceChildren();
    byId("audit-rows").replaceChildren();
    byId("bundle-download").hidden = true;
    setupNode = "";
    latestOverview = null;
    showServerStep(0);
    adminAllowed = false;
    if (bundleURL) {
      URL.revokeObjectURL(bundleURL);
      bundleURL = "";
    }
    message("Zugangsdaten aus diesem Tab entfernt.");
  }
  async function load() {
    if (loading || !apiKey || !userId) return;
    loading = true;
    const currentSession = session;
    byId("connect-button").disabled = true;
    byId("refresh").disabled = true;
    try {
      const response = await fetch("/v1/status", {
        cache: "no-store",
        headers: { Authorization: "Bearer " + apiKey, "X-User-ID": userId },
        signal: AbortSignal.timeout(10000),
      });
      if (!response.ok) {
        const messages = {
          401: "API-Key ungültig.",
          400: "Benutzer-ID ungültig.",
          503: "Gateway oder Audit-Speicherung nicht verfügbar.",
        };
        throw new Error(
          messages[response.status] || "Status konnte nicht geladen werden.",
        );
      }
      const data = await response.json();
      if (currentSession !== session) return;
      document.body.classList.add("connected-dashboard");
      byId("dashboard-nav").hidden = false;
      byId("signed-in-as").textContent = " · " + userId;
      byId("ready-count").textContent = data.nodes.filter(
        (node) => node.state === "ready",
      ).length;
      byId("model-count").textContent = data.models.length;
      byId("test-panel").hidden = false;
      const chosen = byId("test-model").value;
      byId("test-model").replaceChildren();
      for (const model of data.models) {
        const option = document.createElement("option");
        option.value = model;
        option.textContent = model;
        byId("test-model").append(option);
      }
      if (data.models.includes(chosen)) byId("test-model").value = chosen;
      byId("node-note").textContent =
        data.nodes.length + " Knoten konfiguriert";
      byId("nodes").replaceChildren();
      byId("nodes").className = "";
      if (!data.nodes.length) {
        byId("nodes").className = "empty";
        byId("nodes").textContent =
          "Noch keine Modellserver angemeldet. Öffne unten „Server hinzufügen“.";
      }
      for (const node of data.nodes) {
        const row = document.createElement("div");
        row.className = "node-row";
        const name = document.createElement("div");
        name.className = "node-name";
        name.textContent = node.id;
        const model = document.createElement("span");
        model.className = "node-model";
        model.textContent = node.model;
        name.append(model);
        const capacity = document.createElement("span");
        capacity.textContent =
          node.active + " / " + node.capacity + " Anfragen";
        const state = document.createElement("span");
        state.className =
          "node-state" + (node.state === "ready" ? " ready" : "");
        state.textContent =
          {
            ready: "Bereit",
            unavailable: "Nicht verfügbar",
            configured: "Statisch konfiguriert",
          }[node.state] || "Unbekannt";
        row.append(name, capacity, state);
        byId("nodes").append(row);
      }
      await loadAdmin(currentSession);
      message(
        "Status geladen · " +
          new Date().toLocaleTimeString("de-DE") +
          " · Abfrage im Audit erfasst.",
      );
    } catch (error) {
      if (currentSession !== session) return;
      message(
        error.name === "TimeoutError"
          ? "Zeitüberschreitung beim Laden."
          : error.message,
        true,
      );
    } finally {
      loading = false;
      byId("connect-button").disabled = false;
      byId("refresh").disabled = !apiKey;
    }
    await health();
  }

  const errorMessages = {
    admin_required: "Dieser Zugang hat keine Administratorrechte.",
    gateway_certificate_host_mismatch:
      "Die Gateway-Adresse passt nicht zum Zertifikat. Verwende eine im Zertifikat enthaltene Adresse.",
    node_or_revision_conflict:
      "Servername bereits vorhanden oder Modellversion stimmt nicht mit vorhandenen Replikaten überein.",
    enrollment_not_created:
      "Anmeldung nicht erstellt. Verwende einen neuen Servernamen.",
    catalog_unreachable:
      "Modellquelle nicht erreichbar. Netzwerkzugang der Zentrale prüfen.",
    catalog_access_required:
      "Die Modellquelle verlangt eine Freigabe. Download wurde nicht gestartet.",
    outbound_agent_required:
      "Dieser Server verwendet noch den direkten Verbindungsmodus. Für Modellverwaltung einen neuen Agenten im ausgehenden Modus anmelden.",
    model_not_in_catalog:
      "Dieses Modell ist noch nicht im Downloadkatalog enthalten.",
    node_not_ready_or_busy:
      "Der Server ist noch nicht bereit oder bearbeitet gerade eine Anfrage. Kurz warten und erneut prüfen.",
    node_test_no_complete_answer:
      "Es kam keine vollständige Textantwort an. Laufzeit und Ausgabelimit prüfen und den Test wiederholen.",
    model_hash_conflict:
      "Die Modelldatei stimmt nicht mit der Anmeldung überein.",
  };
  async function adminRequest(path, body) {
    const response = await fetch(path, {
      method: body ? "POST" : "GET",
      headers: {
        Authorization: "Bearer " + apiKey,
        "X-User-ID": userId,
        "Content-Type": "application/json",
      },
      body: body ? JSON.stringify(body) : undefined,
      cache: "no-store",
      signal: AbortSignal.timeout(path.endsWith("/test") ? 60000 : 30000),
    });
    const data = await response.json();
    if (!response.ok) {
      const code =
        typeof data.error === "string" ? data.error : data.error?.code;
      throw Error(
        errorMessages[code] ||
          "Verwaltungsaktion fehlgeschlagen (HTTP " + response.status + ").",
      );
    }
    return data;
  }
  const states = {
    idle: "Verbunden · noch kein Modellauftrag",
    external: "Externer Modellserver",
    downloading: "Wird heruntergeladen",
    verifying: "Prüfsumme wird geprüft",
    downloaded: "Heruntergeladen",
    starting: "Modell wird gestartet",
    running: "Modellprozess läuft",
    stopped: "Gestoppt",
    error: "Aktion fehlgeschlagen",
  };
  const modelErrors = {
    download_failed:
      "Download fehlgeschlagen. Erneut versuchen setzt einen Teil-Download fort.",
    disk_full: "Nicht genügend Speicherplatz auf dem Server.",
    hash_mismatch: "Prüfsumme stimmt nicht. Datei wurde nicht aktiviert.",
    engine_missing:
      "llama-server fehlt. Im lokalen Agenten eine passende Laufzeit einrichten.",
    engine_failed:
      "Modellprozess konnte nicht dauerhaft gestartet werden. Laufzeit und Speicher prüfen.",
    external_engine_running:
      "Der Modellserver-Port ist bereits belegt. Vorhandenen Prozess zunächst lokal beenden.",
    invalid_model: "Modellauftrag ungültig.",
    model_not_downloaded: "Modell ist noch nicht heruntergeladen.",
  };
  async function loadAdmin(currentSession) {
    let data;
    try {
      data = await adminRequest("/admin/overview");
    } catch {
      if (currentSession === session) {
        byId("admin-tools").hidden = true;
        byId("audit-panel").hidden = true;
        byId("backup-panel").hidden = true;
        byId("admin-access-note").textContent =
          "Verwaltung benötigt einen Administratorzugang und einen aktivierten Dashboard-Endpunkt.";
      }
      return;
    }
    if (currentSession !== session) return;
    latestOverview = data;
    updateSetup(data);
    adminAllowed = true;
    byId("admin-access-note").textContent = "Administratorzugang aktiv.";
    byId("admin-tools").hidden = false;
    byId("audit-panel").hidden = false;
    byId("backup-panel").hidden = false;
    if (!byId("gateway-address").value && location.protocol === "https:")
      byId("gateway-address").value = location.origin;
    const container = byId("managed-devices");
    container.replaceChildren();
    container.className = "device-grid";
    if (!data.devices.length)
      container.textContent =
        "Noch keine Server angemeldet. Beginne mit „Server hinzufügen“.";
    for (const device of data.devices) {
      const card = document.createElement("article");
      card.className = "device-card";
      const title = document.createElement("h3");
      title.textContent = device.id;
      card.append(title);
      const info = document.createElement("p");
      const report = device.report || {};
      info.textContent =
        device.model +
        " · " +
        (device.connection === "outbound"
          ? "Ausgehende Verbindung"
          : "Direkte Verbindung");
      card.append(info);
      const version = document.createElement("p");
      version.className = "muted";
      version.textContent = "Version: " + device.revision;
      card.append(version);
      const replicate = document.createElement("button");
      replicate.type = "button";
      replicate.className = "secondary";
      replicate.textContent = "Weiteren Server für dieses Modell hinzufügen";
      replicate.addEventListener("click", () => {
        byId("add-server").open = true;
        byId("server-name").value = "";
        byId("server-model").value = device.model;
        byId("server-revision").value = device.revision;
        byId("server-capacity").value = "1";
        showServerStep(0);
        byId("server-name").focus();
      });
      card.append(replicate);
      const state = document.createElement("p");
      state.textContent = device.revoked
        ? "Zugang gesperrt"
        : states[report.state] || "Warte auf Agentenmeldung";
      card.append(state);
      if (!device.revoked && device.connection === "outbound") {
        const resume = document.createElement("button");
        resume.type = "button";
        resume.className = "secondary";
        resume.textContent = "Einrichtung fortsetzen";
        resume.addEventListener("click", () => {
          setupNode = device.id;
          byId("server-name").value = device.id;
          byId("setup-finish").hidden = true;
          byId("setup-test-result").textContent = "";
          byId("add-server").open = true;
          showServerStep(
            data.nodes.find((n) => n.id === device.id)?.state === "ready"
              ? 4
              : 3,
          );
          updateSetup(data);
          byId("add-server").scrollIntoView({ behavior: "smooth" });
        });
        card.append(resume);
      }
      if (report.os) {
        const hardware = document.createElement("p");
        hardware.className = "muted";
        hardware.textContent =
          report.os +
          " / " +
          report.arch +
          " · " +
          report.cpus +
          " vom System gemeldete CPU-Threads" +
          (report.gpu
            ? " · Erkannt: " +
              report.gpu +
              " (" +
              (report.vram_bytes / 1e9).toFixed(1) +
              " GB VRAM)"
            : " · Keine NVIDIA-GPU erkannt") +
          (report.free_bytes
            ? " · " + (report.free_bytes / 1e9).toFixed(1) + " GB frei"
            : "");
        card.append(hardware);
      }
      if (report.total) {
        const progress = document.createElement("progress");
        progress.max = report.total;
        progress.value = report.downloaded;
        progress.setAttribute("aria-label", "Downloadfortschritt");
        card.append(progress);
        const size = document.createElement("p");
        size.textContent =
          (report.downloaded / 1e9).toFixed(2) +
          " / " +
          (report.total / 1e9).toFixed(2) +
          " GB";
        card.append(size);
      }
      if (report.error) {
        const error = document.createElement("p");
        error.className = "error";
        error.textContent =
          modelErrors[report.error] || "Agent meldet einen Fehler.";
        card.append(error);
      }
      const node = data.nodes.find((n) => n.id === device.id);
      if (node) {
        const ready = document.createElement("p");
        ready.textContent =
          "API: " +
          ({
            ready: "Bereit",
            unavailable: "Nicht verfügbar",
            configured: "Konfiguriert",
          }[node.state] || node.state);
        card.append(ready);
      }
      if (!device.revoked) {
        if (device.connection === "outbound") {
          const mode = document.createElement("select");
          mode.setAttribute("aria-label", "Ausführung auf " + device.id);
          for (const [value, label] of [
            ["auto", "GPU verwenden, falls unterstützt"],
            ["cpu", "Nur CPU"],
          ]) {
            const option = document.createElement("option");
            option.value = value;
            option.textContent = label;
            mode.append(option);
          }
          mode.value = gpuChoices.get(device.id) || device.plan?.gpu || "auto";
          mode.addEventListener("change", () =>
            gpuChoices.set(device.id, mode.value),
          );
          card.append(mode);
          const actions = document.createElement("div");
          actions.className = "actions";
          for (const [action, label] of [
            ["download", "Nur herunterladen"],
            ["start", "Herunterladen und starten"],
            ["stop", "Stoppen"],
          ]) {
            const button = document.createElement("button");
            button.type = "button";
            button.className = "secondary";
            button.textContent = label;
            button.disabled = pendingActions.has(device.id);
            button.addEventListener("click", async () => {
              button.disabled = true;
              pendingActions.add(device.id);
              const s = session;
              try {
                await adminRequest(
                  "/admin/nodes/" + encodeURIComponent(device.id) + "/action",
                  { action, gpu: mode.value },
                );
                if (s === session)
                  byId("admin-message").textContent =
                    "Auftrag gespeichert. Der Agent übernimmt ihn bei der nächsten Verbindung.";
              } catch (e) {
                if (s === session)
                  byId("admin-message").textContent = e.message;
              } finally {
                if (s === session) {
                  button.disabled = false;
                  pendingActions.delete(device.id);
                  void load();
                }
              }
            });
            actions.append(button);
          }
          card.append(actions);
          const test = document.createElement("button");
          test.type = "button";
          test.className = "secondary";
          test.textContent = "Testantwort prüfen";
          test.disabled = node?.state !== "ready";
          test.addEventListener("click", async () => {
            test.disabled = true;
            try {
              const result = await adminRequest(
                "/admin/nodes/" + encodeURIComponent(device.id) + "/test",
                {},
              );
              byId("admin-message").textContent =
                device.id + ": " + result.message;
            } catch (error) {
              byId("admin-message").textContent = error.message;
            } finally {
              test.disabled = false;
            }
          });
          card.append(test);
        }
        const revoke = document.createElement("button");
        revoke.type = "button";
        revoke.className = "secondary danger";
        revoke.textContent = "Serverzugang sperren";
        revoke.addEventListener("click", async () => {
          if (
            !confirm(
              "Zugang von " +
                device.id +
                " sperren? Neue Anfragen an diesen Server werden verhindert.",
            )
          )
            return;
          try {
            await adminRequest(
              "/admin/nodes/" + encodeURIComponent(device.id) + "/revoke",
              {},
            );
            void load();
          } catch (e) {
            byId("admin-message").textContent = e.message;
          }
        });
        const danger = document.createElement("details");
        danger.className = "danger-zone";
        const summary = document.createElement("summary");
        summary.textContent = "Zugang verwalten";
        danger.append(summary, revoke);
        card.append(danger);
      }
      container.append(card);
    }
  }
  function showServerStep(step) {
    setupStep = step;
    document
      .querySelectorAll("[data-server-step]")
      .forEach(
        (panel) => (panel.hidden = Number(panel.dataset.serverStep) !== step),
      );
    [...byId("server-steps").children].forEach((item, index) => {
      item.classList.toggle("complete", index < step);
      if (index === step) item.setAttribute("aria-current", "step");
      else item.removeAttribute("aria-current");
    });
    byId("server-back").hidden = step === 0 || step >= 3;
    byId("server-next").hidden = step === 4 || (step === 2 && !setupNode);
    byId("server-next").disabled = step === 3;
    byId("server-next").textContent =
      [
        "Weiter zur Modellauswahl",
        "Weiter zur Anmeldung",
        "Weiter zum Verbindungsstatus",
        "Weiter zum Funktionstest",
      ][step] || "Weiter";
    byId("enroll-summary").textContent =
      byId("server-name").value + " · " + byId("server-model").value;
  }
  function updateConnectionHelp() {
    const kind = byId("connection-kind").value;
    byId("connection-help").textContent =
      kind === "ssh"
        ? "Öffne den KI-Basis-Manager auf deinem Rechner und wähle „Anderen Linux-Server über SSH einrichten“. Zugang und Laufzeitpaket werden dort ausgewählt; die Verbindungsdatei erstellst du hier im nächsten Schritt."
        : kind === "headless"
          ? "Cloud-VM oder eigener Linux-Server: Das Agent-Paket einmalig über SSH oder Cloud-init starten. Danach erfolgt die Einrichtung im Browser. Alternativ den SSH-Assistenten im KI-Basis-Manager verwenden."
          : kind === "local"
            ? "Öffne das Agent-Paket auf dem Rechner der Zentrale. Die Laufzeit wird im Agent-Assistenten geprüft."
            : "Öffne das Agent-Paket auf dem gewünschten Windows-, Linux- oder macOS-Rechner. Der Agent verbindet sich ausgehend; ein öffentlich erreichbarer Agent-Port ist nicht nötig.";
    let loopback = false;
    try {
      loopback = ["127.0.0.1", "localhost", "[::1]"].includes(
        new URL(byId("gateway-address").value).hostname,
      );
    } catch {}
    byId("loopback-confirm").hidden = !loopback;
  }
  function updateSetup(data) {
    if (!setupNode) return;
    const device = data.devices.find((d) => d.id === setupNode);
    const node = data.nodes.find((n) => n.id === setupNode);
    const report = device?.report || {};
    byId("setup-node-name").textContent = setupNode;
    byId("setup-progress").textContent = device?.revoked
      ? "Zugang gesperrt. Einen neuen Server anmelden."
      : report.error
        ? modelErrors[report.error] ||
          "Einrichtung fehlgeschlagen. Agent-Einstellungen prüfen."
        : report.os
          ? states[report.state] || "Agent verbunden"
          : "Warte auf den Agenten. Verbindungsdatei auf dem gewünschten Rechner öffnen.";
    byId("setup-start").disabled =
      !report.os ||
      device?.revoked ||
      ["downloading", "verifying", "starting", "running"].includes(
        report.state,
      );
    byId("setup-download").hidden = !report.total;
    byId("setup-download").max = report.total || 1;
    byId("setup-download").value = report.downloaded || 0;
    if (setupStep === 3) byId("server-next").disabled = node?.state !== "ready";
  }
  byId("server-next").addEventListener("click", () => {
    if (setupStep === 0) {
      updateConnectionHelp();
      for (const id of ["server-name", "gateway-address"])
        if (!byId(id).reportValidity()) return;
      if (!byId("loopback-confirm").hidden && !byId("loopback-ack").checked) {
        byId("loopback-ack").focus();
        return;
      }
      const existing = latestOverview?.devices.find(
        (d) => !d.revoked && d.model === byId("server-model").value,
      );
      if (existing) byId("server-revision").value = existing.revision;
    }
    if (setupStep === 1)
      for (const id of ["server-model", "server-revision", "server-capacity"])
        if (!byId(id).reportValidity()) return;
    if (setupStep < 4) showServerStep(setupStep + 1);
    if (latestOverview) updateSetup(latestOverview);
  });
  byId("server-back").addEventListener("click", () =>
    showServerStep(Math.max(0, setupStep - 1)),
  );
  byId("connection-kind").addEventListener("change", updateConnectionHelp);
  byId("gateway-address").addEventListener("input", updateConnectionHelp);
  byId("setup-renew").addEventListener("click", () => showServerStep(2));
  byId("setup-start").addEventListener("click", async () => {
    byId("setup-start").disabled = true;
    try {
      await adminRequest(
        "/admin/nodes/" + encodeURIComponent(setupNode) + "/action",
        { action: "start", gpu: byId("setup-gpu").value },
      );
      byId("setup-progress").textContent =
        "Auftrag gespeichert. Der Agent beginnt mit Download und Start.";
    } catch (error) {
      byId("setup-progress").textContent = error.message;
      byId("setup-start").disabled = false;
    }
  });
  byId("setup-test").addEventListener("click", async () => {
    const current = session;
    byId("setup-test").disabled = true;
    byId("setup-test-result").textContent = "Testanfrage läuft …";
    try {
      const result = await adminRequest(
        "/admin/nodes/" + encodeURIComponent(setupNode) + "/test",
        {},
      );
      if (current !== session) return;
      byId("setup-test-result").textContent = result.message;
      byId("setup-finish").hidden = false;
    } catch (error) {
      if (current === session)
        byId("setup-test-result").textContent = error.message;
    } finally {
      byId("setup-test").disabled = false;
    }
  });
  byId("setup-finish").addEventListener("click", () => {
    byId("add-server").open = false;
    byId("managed-devices").scrollIntoView({ behavior: "smooth" });
    setupNode = "";
    showServerStep(0);
  });
  byId("new-server").addEventListener("click", () => {
    if (!setupNode && setupStep === 0) {
      const existing = new Set(
        (latestOverview?.devices || []).map((d) => d.id),
      );
      let number = 1;
      while (existing.has("server-" + String(number).padStart(2, "0")))
        number++;
      byId("server-name").value = "server-" + String(number).padStart(2, "0");
    }
    byId("add-server").open = true;
    byId("add-server").scrollIntoView({ behavior: "smooth" });
  });
  byId("disconnect-top").addEventListener("click", clear);
  byId("refresh-top").addEventListener("click", load);
  for (const id of [
    "server-name",
    "gateway-address",
    "server-model",
    "server-revision",
    "server-capacity",
  ])
    byId(id).addEventListener("input", () => {
      if (setupStep >= 3 || !setupNode) return;
      setupNode = "";
      byId("bundle-download").hidden = true;
      if (bundleURL) {
        URL.revokeObjectURL(bundleURL);
        bundleURL = "";
      }
      byId("enroll-result").textContent =
        "Angaben geändert. Eine neue Verbindungsdatei erstellen.";
      if (setupStep === 2) byId("server-next").hidden = true;
    });
  byId("enroll-server").noValidate = true;
  showServerStep(0);
  updateConnectionHelp();
  byId("enroll-server").addEventListener("submit", async (e) => {
    e.preventDefault();
    if (!adminAllowed) return;
    const s = session;
    byId("enroll-button").disabled = true;
    try {
      if (setupStep !== 2) return;
      const bundle = await adminRequest("/admin/enroll", {
        id: byId("server-name").value,
        gateway: byId("gateway-address").value.replace(/\/$/, ""),
        model: byId("server-model").value,
        revision: byId("server-revision").value,
        capacity: Number(byId("server-capacity").value),
      });
      if (s !== session) return;
      setupNode = bundle.node_id;
      byId("setup-finish").hidden = true;
      byId("setup-test-result").textContent = "";
      byId("server-next").hidden = false;
      if (bundleURL) URL.revokeObjectURL(bundleURL);
      bundleURL = URL.createObjectURL(
        new Blob([JSON.stringify(bundle, null, 2)], {
          type: "application/json",
        }),
      );
      byId("bundle-download").href = bundleURL;
      byId("bundle-download").hidden = false;
      byId("enroll-result").textContent =
        "Datei erstellt. Gültig bis " +
        new Date(bundle.expires_at).toLocaleTimeString("de-DE") +
        ". Vertraulich behandeln und nur auf dem gewünschten Rechner importieren.";
    } catch (error) {
      if (s === session) byId("enroll-result").textContent = error.message;
    } finally {
      byId("enroll-button").disabled = false;
    }
  });
  byId("backup-central").addEventListener("click", async () => {
    byId("backup-central").disabled = true;
    const current = session;
    try {
      const response = await fetch("/admin/backup", {
        headers: { Authorization: "Bearer " + apiKey, "X-User-ID": userId },
        cache: "no-store",
        signal: AbortSignal.timeout(60000),
      });
      if (!response.ok) throw Error("Sicherung konnte nicht erstellt werden.");
      const blob = await response.blob();
      if (current !== session) return;
      const url = URL.createObjectURL(blob),
        link = document.createElement("a");
      link.href = url;
      link.download = "ki-basis-zentrale-backup.zip";
      link.click();
      setTimeout(() => URL.revokeObjectURL(url), 60000);
      byId("backup-central-result").textContent =
        "Sicherung heruntergeladen. Vertraulich aufbewahren und zusätzlich die Agent-Einstellungen sichern.";
    } catch (error) {
      if (current === session)
        byId("backup-central-result").textContent = error.message;
    } finally {
      byId("backup-central").disabled = false;
    }
  });
  byId("load-audit").addEventListener("click", async () => {
    const s = session;
    try {
      const records = await adminRequest("/admin/audit");
      if (s !== session) return;
      const rows = byId("audit-rows");
      rows.replaceChildren();
      for (const r of records) {
        const row = document.createElement("tr");
        for (const value of [
          new Date(r.time).toLocaleString("de-DE"),
          r.event.type,
          r.event.user_id || r.event.actor || "—",
          r.event.node_id || "—",
          r.event.outcome || r.event.status || "—",
        ]) {
          const cell = document.createElement("td");
          cell.textContent = value;
          row.append(cell);
        }
        rows.append(row);
      }
    } catch (error) {
      message(error.message, true);
    }
  });
  setInterval(() => {
    if (apiKey && !document.hidden) void load();
  }, 10000);

  byId("test-cancel").addEventListener("click", () =>
    inferenceController?.abort(),
  );
  byId("test-form").addEventListener("submit", async (e) => {
    e.preventDefault();
    if (inferenceController) return;
    const current = session;
    const controller = new AbortController();
    inferenceController = controller;
    byId("test-send").disabled = true;
    byId("test-cancel").disabled = false;
    byId("test-answer").textContent = "";
    byId("test-message").textContent = "Anfrage läuft …";
    let timer = setTimeout(() => controller.abort(), 180000);
    let done = false;
    let finishReason = null;
    try {
      const response = await fetch("/v1/chat/completions", {
        method: "POST",
        headers: {
          Authorization: "Bearer " + apiKey,
          "X-User-ID": userId,
          "Content-Type": "application/json",
        },
        body: JSON.stringify({
          model: byId("test-model").value,
          messages: [{ role: "user", content: byId("test-prompt").value }],
          stream: true,
          max_tokens: 1024,
          ...(byId("test-model").value === "gemma-4-26b-a4b"
            ? { chat_template_kwargs: { enable_thinking: false } }
            : {}),
        }),
        signal: controller.signal,
      });
      if (!response.ok)
        throw Error("API-Anfrage abgelehnt (HTTP " + response.status + ").");
      const reader = response.body.getReader();
      const decoder = new TextDecoder();
      let buffer = "";
      for (;;) {
        const chunk = await reader.read();
        if (chunk.done) break;
        buffer += decoder.decode(chunk.value, { stream: true });
        let boundary;
        while ((boundary = /\r\n\r\n|\n\n|\r\r/.exec(buffer))) {
          const item = buffer.slice(0, boundary.index);
          buffer = buffer.slice(boundary.index + boundary[0].length);
          for (const line of item.split(/\r\n|\n|\r/)) {
            if (!line.startsWith("data:")) continue;
            const value = line.slice(5).trim();
            if (value === "[DONE]") {
              done = true;
              continue;
            }
            const data = JSON.parse(value);
            if (data.error)
              throw Error(data.error.message || "Stream unterbrochen");
            for (const choice of data.choices || []) {
              if (choice.finish_reason) finishReason = choice.finish_reason;
              if (current === session && choice.delta?.content)
                byId("test-answer").textContent += choice.delta.content;
            }
          }
        }
        if (current !== session) {
          controller.abort();
          break;
        }
      }
      if (current === session) {
        const hasText = byId("test-answer").textContent.trim().length > 0;
        byId("test-message").textContent = !done
          ? "Stream ohne Abschluss beendet."
          : finishReason === "length"
            ? "Ausgabelimit erreicht. Die Antwort kann fehlen oder unvollständig sein."
            : !hasText
              ? "Stream beendet, aber keine Textantwort empfangen."
              : "Antwort vollständig empfangen.";
      }
    } catch (error) {
      if (current === session)
        byId("test-message").textContent =
          error.name === "AbortError"
            ? "Anfrage abgebrochen oder Zeitlimit erreicht."
            : error.message;
    } finally {
      clearTimeout(timer);
      inferenceController = null;
      byId("test-send").disabled = false;
      byId("test-cancel").disabled = true;
    }
  });
  byId("connect-form").addEventListener("submit", (event) => {
    event.preventDefault();
    session += 1;
    apiKey = byId("api-key").value;
    userId = byId("user-id").value;
    byId("api-key").value = "";
    void load();
  });
  byId("refresh").addEventListener("click", () => {
    void load();
  });
  byId("disconnect").addEventListener("click", clear);
  if (startupFragment.startsWith("#access=")) {
    try {
      const raw = startupFragment
        .slice(8)
        .replace(/-/g, "+")
        .replace(/_/g, "/");
      const login = JSON.parse(atob(raw));
      if (
        typeof login.key === "string" &&
        login.key.startsWith("kb_") &&
        typeof login.user === "string"
      ) {
        apiKey = login.key;
        userId = login.user;
        byId("user-id").value = userId;
        void load();
      }
    } catch {
      message(
        "Lokaler Zugang konnte nicht geöffnet werden. Zugangsdaten erneut eingeben.",
        true,
      );
    }
    startupFragment = "";
  }
  void health();
})();
