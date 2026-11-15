(() => {
  "use strict";
  const byId = (id) => document.getElementById(id);
  let lastStatus;
  let requesting = false;
  let closed = false;
  let timer;
  let failures = 0;
  let applicationAccess;
  const messages = {
    runtime_download_failed:
      "Die Laufzeit konnte nicht heruntergeladen werden. Prüfe die Internetverbindung und versuche es erneut.",
    runtime_checksum_mismatch:
      "Die Laufzeit hat die Sicherheitsprüfung nicht bestanden. Sie wurde nicht gestartet. Wiederhole den Download.",
    runtime_digest_mismatch:
      "Die Prüfsumme der Laufzeit stimmt nicht. Die Datei wurde verworfen. Wiederhole den Download.",
    runtime_unsupported:
      "Für diesen Rechner gibt es noch keine geprüfte automatische Laufzeit. Verwende den bisherigen Manager mit einem eigenen Laufzeitpaket.",
    runtime_platform_unsupported:
      "Für dieses Betriebssystem ist noch keine automatische Laufzeit hinterlegt. Ein eigenes Paket kannst du im bisherigen Manager importieren.",
    runtime_incompatible:
      "Die Laufzeit kann auf diesem Rechner nicht gestartet werden. Prüfe die Systemanforderungen oder verwende eine eigene Laufzeit im Manager.",
    runtime_archive_invalid:
      "Das Laufzeitpaket konnte nicht gelesen werden. Wiederhole den Download.",
    runtime_download_too_large:
      "Der Laufzeitdownload überschreitet die erlaubte Größe. Das Paket wurde nicht verwendet.",
    canceled:
      "Der Vorgang wurde abgebrochen. Du kannst ihn hier erneut starten.",
    operation_cancelled:
      "Der Vorgang wurde abgebrochen. Deine vorhandene Einrichtung bleibt erhalten.",
    operation_in_progress:
      "Ein anderer Vorgang läuft bereits. Warte auf seinen Abschluss oder brich ihn ab.",
    storage_failed:
      "KI-Basis kann nicht in den Datenordner schreiben. Prüfe Schreibrechte und freien Speicher.",
    settings_storage_failed:
      "Die Einstellungen konnten nicht gespeichert werden. Prüfe Schreibrechte und freien Speicher.",
    enrollment_failed:
      "Der lokale Modellserver konnte nicht verbunden werden. Starte KI-Basis erneut; die vorhandene Einrichtung bleibt erhalten.",
    enrollment_failed_check_address_certificate_and_code:
      "Die lokale Verbindung konnte nicht hergestellt werden. Starte KI-Basis erneut und wiederhole die Vorbereitung.",
    model_not_ready:
      "Das Modell ist noch nicht bereit. Warte auf das Laden oder starte es erneut.",
    node_not_ready_or_busy:
      "Das Modell ist noch nicht bereit oder bearbeitet eine Anfrage. Warte kurz und versuche es erneut.",
    node_test_no_complete_answer:
      "Das Modell hat keine vollständige Testantwort geliefert. Prüfe den Modellstatus und wiederhole den Test.",
    test_failed:
      "Der Test hat keine vollständige Antwort erhalten. Auf einer CPU kann die Verarbeitung länger dauern. Prüfe den Modellstatus und versuche es erneut.",
    model_action_failed:
      "Der Modellauftrag konnte nicht gestartet werden. Prüfe die Internetverbindung und versuche es erneut.",
    catalog_resolution_failed:
      "Die Modellinformationen sind derzeit nicht erreichbar. Prüfe die Internetverbindung und wiederhole den Start.",
    service_cannot_quit:
      "KI-Basis läuft als Windows-Dienst. Verwende die Windows-Diensteverwaltung, um den Server gezielt anzuhalten.",
    authentication_required:
      "Der lokale Zugang ist abgelaufen. Öffne KI-Basis erneut über das Startmenü.",
    desktop_operation_cancelled:
      "Der Vorgang wurde abgebrochen. Deine vorhandene Einrichtung bleibt erhalten; du kannst erneut starten.",
    desktop_operation_timed_out:
      "Der Vorgang hat zu lange gedauert. Prüfe den Modellstatus und wiederhole ihn. Deine vorhandene Einrichtung bleibt erhalten.",
    desktop_model_not_ready:
      "Das Modell ist noch nicht bereit. Warte auf das Laden oder starte es erneut.",
    desktop_test_failed:
      "Das Modell hat keine vollständige Testantwort geliefert. Prüfe den Modellstatus und wiederhole den Test.",
    runtime_storage_failed:
      "Die Laufzeit konnte nicht gespeichert werden. Prüfe Schreibrechte und freien Speicher im Datenordner.",
    runtime_download_size_mismatch:
      "Der Laufzeitdownload ist unvollständig. Prüfe die Internetverbindung und wiederhole den Download.",
    runtime_source_invalid:
      "Die hinterlegte Laufzeitquelle ist ungültig. Verwende eine aktuelle KI-Basis-Version oder importiere ein eigenes Paket im Manager.",
    desktop_operation_in_progress:
      "Ein Vorgang läuft bereits. Warte auf seinen Abschluss oder brich ihn ab.",
    desktop_session_required:
      "Dein lokaler Zugang ist abgelaufen. Öffne KI-Basis erneut über das Startmenü.",
    desktop_prepare_first:
      "Bereite zuerst die Laufzeit und den lokalen Modellserver vor.",
    desktop_gateway_unavailable:
      "Die lokale Zentrale antwortet nicht rechtzeitig. Warte kurz und versuche es erneut oder starte KI-Basis neu.",
    desktop_gateway_request_failed:
      "Die lokale Zentrale konnte den Auftrag nicht abschließen. Prüfe den Modellstatus und versuche es erneut.",
    desktop_gateway_response_invalid:
      "Die lokale Zentrale hat eine unvollständige Antwort geliefert. Wiederhole den Vorgang.",
    desktop_agent_enrollment_failed:
      "Der lokale Modellserver konnte nicht angemeldet werden. Wiederhole die Vorbereitung. Deine vorhandenen Daten bleiben erhalten.",
    desktop_agent_settings_storage_failed:
      "Die Einstellungen konnten nicht gespeichert werden. Prüfe Schreibrechte und freien Speicher im Datenordner.",
    desktop_agent_start_failed:
      "Der lokale Modellserver konnte nicht gestartet werden. Starte KI-Basis erneut und prüfe den Modellstatus.",
    desktop_runtime_verification_failed:
      "Die Laufzeit konnte nicht erfolgreich geprüft werden. Prüfe die Systemanforderungen und wiederhole die Vorbereitung.",
    desktop_engine_port_unavailable:
      "Für den Modellserver ist kein lokaler Netzwerkport verfügbar. Schließe andere Modellserver und versuche es erneut.",
    desktop_test_no_complete_answer:
      "Das Modell hat keine vollständige Testantwort geliefert. Wiederhole den Test, sobald das Modell bereit ist.",
    desktop_test_response_too_large:
      "Die Testantwort ist unerwartet groß. Sie wurde verworfen. Wiederhole den kurzen Funktionstest.",
    desktop_model_stop_failed:
      "Der Modellserver hat den Stopp noch nicht bestätigt. Warte kurz und prüfe den Status erneut.",
    desktop_stop_service_with_system_manager:
      "KI-Basis läuft als Windows-Dienst. Halte den Dienst bei Bedarf über die Windows-Diensteverwaltung an.",
    catalog_unreachable:
      "Die Modellquelle ist nicht erreichbar. Prüfe deine Internetverbindung und versuche es erneut.",
    catalog_access_required:
      "Die Modellquelle verlangt eine Freigabe oder Anmeldung. Prüfe die Zugangsbedingungen auf der verlinkten Modellseite.",
    catalog_invalid:
      "Die Modellquelle liefert derzeit keine passenden Modelldaten. Verwende eine aktuelle KI-Basis-Version oder versuche es später erneut.",
    catalog_missing_integrity:
      "Für dieses Modell fehlen verlässliche Prüfdaten. Der Download wird aus Sicherheitsgründen nicht gestartet.",
    model_hash_conflict:
      "Die Modelldatei widerspricht der gespeicherten Revision. Verwende die erweiterte Verwaltung, um die Modellrevision zu prüfen.",
    desktop_application_credentials_unavailable:
      "Der Anwendungszugang wurde entfernt oder widerrufen. Er wird hier nicht automatisch erneuert. Erstelle einen neuen Zugang über die erweiterte Administration.",
    desktop_model_operation_in_progress:
      "Das Modell wird bereits geladen oder angehalten. Warte auf den Abschluss oder brich den Vorgang ab.",
  };
  const progressLabels = {
    downloading: "Laufzeit wird heruntergeladen …",
    download: "Download läuft …",
    verifying: "Prüfsumme wird geprüft …",
    extracting: "Laufzeit wird eingerichtet …",
    probing: "Laufzeit wird geprüft …",
    installing: "Laufzeit wird installiert …",
    preparing: "Lokaler Modellserver wird verbunden …",
    enrolling: "Lokaler Modellserver wird verbunden …",
    loading: "Modell wird in den Speicher geladen …",
    testing: "Deine Testantwort wird erzeugt …",
    starting: "Modellstart wird vorbereitet …",
    stopping: "Modell wird angehalten …",
    restarting: "Modellserver wird neu gestartet …",
    ready: "Bereit",
  };
  const bytes = (value) => {
    const number = Number(value);
    if (!Number.isFinite(number) || number <= 0) return "Größe wird ermittelt";
    return number >= 1024 ** 3
      ? `${(number / 1024 ** 3).toLocaleString("de-DE", { maximumFractionDigits: 1 })} GB`
      : `${Math.ceil(number / 1024 ** 2).toLocaleString("de-DE")} MB`;
  };
  const show = (id, visible) => {
    byId(id).hidden = !visible;
  };
  const text = (id, value) => {
    byId(id).textContent = value;
  };
  function notice(id, value) {
    text(id, value || "");
    show(id, Boolean(value));
  }
  function friendlyError(code) {
    if (/^runtime_http_/.test(code))
      return "Der Laufzeitserver ist derzeit nicht erreichbar oder verweigert den Download. Prüfe die Internetverbindung und versuche es später erneut.";
    return (
      messages[code] ||
      "Der Vorgang konnte nicht abgeschlossen werden. Wiederhole ihn. Falls der Fehler bleibt, prüfe die Einrichtung im bisherigen Manager."
    );
  }
  // Only product-owned HTTPS source links and loopback management links belong
  // in this local view. Status strings are never interpreted as markup.
  function sourceLink(id, value, label) {
    const element = byId(id);
    element.removeAttribute("href");
    if (label) element.textContent = label;
    try {
      const url = new URL(value);
      if (url.protocol === "https:" && !url.username && !url.password)
        element.href = url.href;
    } catch {
      /* An unavailable recommendation is displayed without a link. */
    }
  }
  async function request(path, body, token) {
    const headers = {};
    if (body !== undefined) headers["Content-Type"] = "application/json";
    if (token) headers["X-Desktop-Token"] = token;
    const response = await fetch(`/desktop/${path}`, {
      method: body === undefined ? "GET" : "POST",
      credentials: "same-origin",
      cache: "no-store",
      headers,
      body: body === undefined ? undefined : JSON.stringify(body),
    });
    if (!response.ok) {
      let code =
        response.status === 401 ? "authentication_required" : "request_failed";
      try {
        const data = await response.json();
        code = data.error || code;
      } catch {
        /* A proxy may return an empty body. */
      }
      const error = new Error(friendlyError(code));
      error.status = response.status;
      throw error;
    }
    return response.json();
  }
  function render(status) {
    lastStatus = status;
    const runtime = status.runtime || {};
    const report = status.report || {};
    const prepared = Boolean(status.connected);
    const ready = status.ready === true;
    const modelBusy = [
      "downloading",
      "verifying",
      "loading",
      "starting",
      "stopping",
      "restarting",
    ].includes(report.state);
    const busy = Boolean(status.busy) || modelBusy;
    const tested = Boolean(status.answer) && ready && !status.error;
    const stage = tested ? 2 : prepared ? (ready ? 2 : 1) : 0;
    ["runtime", "model", "test"].forEach((name, index) => {
      const item = byId(`step-${name}`);
      item.classList.toggle("complete", index < stage || tested);
      if (index === stage) item.setAttribute("aria-current", "step");
      else item.removeAttribute("aria-current");
    });
    text(
      "stage-label",
      tested
        ? "EINRICHTUNG GEPRÜFT"
        : ready
          ? "FAST GESCHAFFT"
          : prepared
            ? "DEIN MODELL"
            : "DEIN ERSTER SCHRITT",
    );
    text(
      "setup-title",
      tested
        ? "Deine KI antwortet."
        : ready
          ? "Zeit für die erste Antwort."
          : prepared
            ? "Jetzt bekommt deine KI ihr Modell."
            : "Bereiten wir deinen Rechner vor.",
    );
    text(
      "stage-description",
      tested
        ? "Dein lokaler KI-Zugang hat den Funktionstest bestanden. Du kannst den Test jederzeit wiederholen."
        : ready
          ? "Das Modell ist geladen. Erzeuge eine kurze Antwort, um den vollständigen Weg zu prüfen."
          : prepared
            ? "Die Laufzeit ist vorbereitet und der lokale Modellserver verbunden. Lade jetzt das empfohlene Modell."
            : "KI-Basis richtet die Zentrale und den lokalen Modellserver gemeinsam ein. Du brauchst keine Terminalbefehle oder Verbindungsdateien.",
    );
    text(
      "state-badge",
      busy
        ? "Wird eingerichtet"
        : tested
          ? "Test bestanden"
          : ready
            ? "Modell bereit"
            : prepared
              ? "Verbunden"
              : "Noch einzurichten",
    );
    byId("state-badge").classList.toggle("ready", tested || ready);
    text(
      "runtime-description",
      runtime.supported === false
        ? "Kein geprüftes Paket für diesen Rechner verfügbar."
        : `Empfohlen: llama.cpp für ${runtime.platform || "diesen Rechner"} · CPU`,
    );
    text(
      "runtime-state",
      runtime.installed ? "Installiert" : "Noch nicht installiert",
    );
    text("runtime-version", runtime.version || "–");
    text("runtime-size", runtime.size ? bytes(runtime.size) : "–");
    text("runtime-sha", runtime.sha256 || "–");
    text(
      "data-directory",
      status.data_directory || "Wird automatisch verwaltet",
    );
    sourceLink(
      "runtime-source",
      runtime.source,
      runtime.source || "Keine Quelle verfügbar",
    );
    show("runtime-panel", !prepared);
    const model = status.model || {};
    text("model-name", model.name || "Empfohlenes Modell");
    text(
      "model-description",
      [model.quantization, "CPU-Betrieb · ein Anfrageplatz"]
        .filter(Boolean)
        .join(" · "),
    );
    text(
      "model-state",
      ready
        ? "Bereit"
        : modelBusy
          ? "Wird geladen"
          : report.state === "error"
            ? "Fehler"
            : "Noch nicht bereit",
    );
    sourceLink("model-source", model.source, "Modellquelle und Lizenz ansehen");
    show("model-panel", prepared && !tested);
    const progress = status.progress || {};
    const downloaded =
      Number(modelBusy ? report.downloaded : progress.downloaded) || 0;
    const total = Number(modelBusy ? report.total : progress.total) || 0;
    show("progress-panel", busy);
    text(
      "progress-label",
      progressLabels[
        modelBusy ? report.state : progress.stage || status.stage
      ] || "Einrichtung läuft …",
    );
    text(
      "progress-detail",
      total > 0
        ? `${bytes(downloaded)} / ${bytes(total)}`
        : downloaded > 0
          ? `${bytes(downloaded)} heruntergeladen`
          : "Bitte warten",
    );
    const meter = byId("operation-progress");
    if (total > 0) {
      meter.max = total;
      meter.value = Math.min(downloaded, total);
    } else meter.removeAttribute("value");
    text("test-answer", status.answer || "");
    show("answer-panel", tested);
    show("access-section", prepared);
    show("prepare-button", !prepared);
    byId("prepare-button").disabled =
      busy || requesting || runtime.supported === false;
    text(
      "prepare-button",
      runtime.installed ? "Lokalen Modellserver verbinden" : "Vorbereiten",
    );
    show("model-button", prepared && !ready && !modelBusy);
    byId("model-button").disabled = busy || requesting;
    show("test-button", ready || tested);
    text("test-button", tested ? "Test wiederholen" : "Testantwort erzeugen");
    byId("test-button").disabled = busy || requesting;
    show("cancel-button", busy);
    byId("cancel-button").disabled = requesting;
    show("stop-button", ready && !busy);
    byId("stop-button").disabled = requesting;
    const lifecycle = status.lifecycle || {};
    text(
      "lifecycle-description",
      lifecycle.mode === "service"
        ? "KI-Basis läuft als Windows-Dienst – auch nach Abmeldung und Neustart. Diese Browserseite kannst du schließen."
        : "Diese Browserseite kannst du schließen. KI-Basis läuft bis zum Beenden des Programms oder zur Abmeldung weiter. Für Betrieb ohne Anmeldung wähle bei der Installation den Servermodus.",
    );
    show("quit-button", Boolean(lifecycle.can_quit));
    byId("quit-button").disabled = requesting;
    const error =
      status.error ||
      (report.state === "error" ? report.error || "model_action_failed" : "");
    notice("error-notice", error ? friendlyError(error) : "");
    show("retry-button", false);
  }
  async function refresh() {
    if (closed) return;
    try {
      const status = await request("status");
      failures = 0;
      notice("connection-notice", "");
      render(status);
    } catch (error) {
      failures += 1;
      notice(
        "connection-notice",
        error.status === 401
          ? error.message
          : "Die Verbindung zu KI-Basis ist unterbrochen. Die Seite versucht es erneut. Falls das Programm beendet wurde, öffne es über das Startmenü.",
      );
      for (const id of [
        "prepare-button",
        "model-button",
        "test-button",
        "stop-button",
        "cancel-button",
        "quit-button",
      ])
        byId(id).disabled = true;
      show("retry-button", true);
      if (error.status === 401) {
        clearAccess();
        closed = true;
        return;
      }
    }
    timer = setTimeout(
      refresh,
      failures ? Math.min(15000, 2000 * failures) : 1500,
    );
  }
  async function perform(path, body = {}) {
    if (requesting) return;
    requesting = true;
    clearTimeout(timer);
    if (lastStatus) render(lastStatus);
    notice("error-notice", "");
    try {
      await request(path, body);
      if (path === "quit") {
        clearAccess();
        closed = true;
        notice(
          "connection-notice",
          "KI-Basis wurde zum Beenden aufgefordert. Du kannst diese Seite schließen und das Programm später über das Startmenü öffnen.",
        );
        for (const button of document.querySelectorAll("button"))
          button.disabled = true;
        return;
      }
    } catch (error) {
      notice("error-notice", error.message);
      requesting = false;
      // Keep an operation error visible until the next user action; background
      // polling must not immediately erase a failed POST's explanation.
      if (lastStatus) {
        const value = byId("error-notice").textContent;
        render(lastStatus);
        notice("error-notice", value);
      }
      timer = setTimeout(refresh, 6000);
      return;
    } finally {
      requesting = false;
    }
    await refresh();
  }
  byId("prepare-button").addEventListener("click", () => perform("prepare"));
  byId("model-button").addEventListener("click", () =>
    perform("model", { action: "start" }),
  );
  byId("test-button").addEventListener("click", () => perform("test"));
  byId("cancel-button").addEventListener("click", () => perform("cancel"));
  byId("stop-button").addEventListener("click", () =>
    perform("model", { action: "stop" }),
  );
  byId("quit-button").addEventListener("click", () => {
    if (
      window.confirm(
        "KI-Basis und das lokale Modell beenden? Verbundene Anwendungen können dann keine Anfragen bearbeiten.",
      )
    )
      perform("quit");
  });
  byId("retry-button").addEventListener("click", () => {
    closed = false;
    clearTimeout(timer);
    refresh();
  });
  function clearAccess() {
    applicationAccess = undefined;
    byId("access-key").type = "password";
    for (const id of ["base", "model", "user", "key"])
      byId(`access-${id}`).value = "";
    show("access-fields", false);
    show("access-button", true);
    text("access-notice", "");
  }
  byId("access-button").addEventListener("click", async () => {
    byId("access-button").disabled = true;
    try {
      applicationAccess = await request("access", {});
      for (const field of ["base", "model", "user", "key"]) {
        byId(`access-${field}`).value =
          applicationAccess[field === "base" ? "base_url" : field] || "";
      }
      show("access-fields", true);
      show("access-button", false);
      text("access-notice", "");
    } catch (error) {
      text("access-notice", error.message);
    } finally {
      byId("access-button").disabled = false;
    }
  });
  byId("hide-access-button").addEventListener("click", clearAccess);
  byId("copy-key-button").addEventListener("click", async () => {
    if (!applicationAccess) return;
    try {
      await navigator.clipboard.writeText(applicationAccess.key);
      text(
        "access-notice",
        "Anwendungsschlüssel kopiert. Füge ihn in deiner vertrauenswürdigen Anwendung ein.",
      );
    } catch {
      byId("access-key").type = "text";
      byId("access-key").select();
      text(
        "access-notice",
        "Die Zwischenablage ist nicht verfügbar. Der Schlüssel ist ausgewählt; kopiere ihn mit Strg+C.",
      );
    }
  });
  byId("save-ca-button").addEventListener("click", () => {
    if (!applicationAccess?.ca_pem) return;
    const url = URL.createObjectURL(
      new Blob([applicationAccess.ca_pem], { type: "application/x-pem-file" }),
    );
    const link = document.createElement("a");
    link.href = url;
    link.download = "ki-basis-ca.crt";
    link.click();
    setTimeout(() => URL.revokeObjectURL(url), 1000);
  });
  window.addEventListener("pagehide", clearAccess);
  async function start() {
    const fragment = window.location.hash.slice(1);
    if (fragment) {
      // Remove the local launch capability before any external link is opened.
      history.replaceState(null, "", window.location.pathname);
      try {
        await request("session", {}, fragment);
      } catch (error) {
        notice("connection-notice", error.message);
        return;
      }
    }
    await refresh();
  }
  start();
})();
