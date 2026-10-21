"use strict";
(() => {
  const token = location.hash.slice(1);
  history.replaceState(null, "", location.pathname);
  const $ = (id) => document.getElementById(id);
  async function api(path, body) {
    const response = await fetch(path, {
      method: body ? "POST" : "GET",
      headers: { "X-Setup-Token": token, "Content-Type": "application/json" },
      body: body ? JSON.stringify(body) : undefined,
      cache: "no-store",
    });
    const data = await response.json();
    if (!response.ok) throw Error(data.error || "Verbindung fehlgeschlagen");
    return data;
  }
  async function status() {
    try {
      const s = await api("/setup/status");
      $("status").textContent = s.connected
        ? "Verbunden: " +
          s.node_id +
          "\nModellstatus: " +
          (s.report.state || "idle")
        : "Noch nicht verbunden.";
      $("submit").disabled = s.connected;
      $("bundle").required = !s.connected;
      $("save-settings").hidden = !s.connected;
      if (!document.activeElement.closest("form")) {
        for (const [id, key] of [
          ["binary", "binary"],
          ["engine", "engine"],
          ["models", "models"],
          ["existing-model", "existing_model"],
        ])
          $(id).value = s.settings[key] || "";
      }
    } catch {
      $("status").textContent =
        "Einrichtung nicht erreichbar. Öffne den vom Agenten bereitgestellten lokalen Link erneut.";
    }
  }
  $("enroll-form").addEventListener("submit", async (e) => {
    e.preventDefault();
    $("submit").disabled = true;
    try {
      const bundle = JSON.parse(await $("bundle").files[0].text());
      await api("/setup/connect", {
        bundle,
        settings: {
          binary: $("binary").value,
          engine: $("engine").value,
          models: $("models").value,
          existing_model: $("existing-model").value,
        },
      });
      $("bundle").value = "";
      $("result").textContent =
        "Verbunden. Weiter geht es im zentralen Dashboard.";
    } catch (error) {
      $("result").textContent = error.message;
      $("submit").disabled = false;
    }
    await status();
  });
  $("save-settings").addEventListener("click", async () => {
    try {
      await api("/setup/settings", {
        binary: $("binary").value,
        engine: $("engine").value,
        models: $("models").value,
        existing_model: $("existing-model").value,
      });
      $("result").textContent =
        "Einstellungen gespeichert. Agent neu gestartet.";
    } catch (e) {
      $("result").textContent = e.message;
    }
    await status();
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
      } catch {
        $("autostart-result").textContent =
          "Autostart konnte nicht eingerichtet werden. Auf diesem System ist eine separate Dienstinstallation erforderlich.";
      }
    });
  void status();
  setInterval(status, 5000);
})();
