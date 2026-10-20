'use strict';
(() => {
  const byId = (id) => document.getElementById(id);
  let apiKey = '';
  let userId = '';
  let loading = false;
  let session = 0;
  const message = (text, error = false) => {
    byId('connection-message').textContent = text;
    byId('connection-message').classList.toggle('error', error);
  };
  async function health() {
    try {
      const response = await fetch('/healthz', {cache: 'no-store', signal: AbortSignal.timeout(5000)});
      if (!response.ok) throw new Error();
      const data = await response.json();
      if (data.status !== 'ok') throw new Error();
      byId('gateway-status').textContent = 'Erreichbar';
      byId('gateway-note').textContent = 'API-Prozess antwortet';
    } catch {
      byId('gateway-status').textContent = 'Nicht erreichbar';
      byId('gateway-note').textContent = 'Gateway-Verbindung prüfen';
    }
  }
  function clear() {
    session += 1;
    apiKey = ''; userId = '';
    byId('api-key').value = '';
    byId('ready-count').textContent = '—';
    byId('model-count').textContent = '—';
    byId('node-note').textContent = 'Zugang erforderlich';
    byId('nodes').replaceChildren();
    byId('nodes').className = 'empty';
    byId('nodes').textContent = 'Verbinde dich mit dem Gateway, um die registrierten Knoten zu sehen.';
    byId('refresh').disabled = true;
    message('Zugangsdaten aus diesem Tab entfernt.');
  }
  async function load() {
    if (loading || !apiKey || !userId) return;
    loading = true;
    const currentSession = session;
    byId('connect-button').disabled = true;
    byId('refresh').disabled = true;
    try {
      const response = await fetch('/v1/status', {cache: 'no-store', headers: {Authorization: 'Bearer ' + apiKey, 'X-User-ID': userId}, signal: AbortSignal.timeout(10000)});
      if (!response.ok) {
        const messages = {401: 'API-Key ungültig.', 400: 'Benutzer-ID ungültig.', 503: 'Gateway oder Audit-Speicherung nicht verfügbar.'};
        throw new Error(messages[response.status] || 'Status konnte nicht geladen werden.');
      }
      const data = await response.json();
      if (currentSession !== session) return;
      byId('ready-count').textContent = data.nodes.filter((node) => node.state === 'ready').length;
      byId('model-count').textContent = data.models.length;
      byId('node-note').textContent = data.nodes.length + ' Knoten konfiguriert';
      byId('nodes').replaceChildren();
      byId('nodes').className = '';
      if (!data.nodes.length) {
        byId('nodes').className = 'empty';
        byId('nodes').textContent = 'Noch keine Modellserver angemeldet. Füge den ersten Agenten per Join-Token hinzu.';
      }
      for (const node of data.nodes) {
        const row = document.createElement('div'); row.className = 'node-row';
        const name = document.createElement('div'); name.className = 'node-name'; name.textContent = node.id;
        const model = document.createElement('span'); model.className = 'node-model'; model.textContent = node.model; name.append(model);
        const capacity = document.createElement('span'); capacity.textContent = node.active + ' / ' + node.capacity + ' Anfragen';
        const state = document.createElement('span'); state.className = 'node-state' + (node.state === 'ready' ? ' ready' : '');
        state.textContent = ({ready: 'Bereit', unavailable: 'Nicht verfügbar', configured: 'Statisch konfiguriert'})[node.state] || 'Unbekannt';
        row.append(name, capacity, state); byId('nodes').append(row);
      }
      message('Status geladen · ' + new Date().toLocaleTimeString('de-DE') + ' · Abfrage im Audit erfasst.');
    } catch (error) {
      if (currentSession !== session) return;
      message(error.name === 'TimeoutError' ? 'Zeitüberschreitung beim Laden.' : error.message, true);
    } finally {
      loading = false; byId('connect-button').disabled = false; byId('refresh').disabled = !apiKey;
    }
    await health();
  }
  byId('connect-form').addEventListener('submit', (event) => {
    event.preventDefault(); session += 1; apiKey = byId('api-key').value; userId = byId('user-id').value;
    byId('api-key').value = ''; void load();
  });
  byId('refresh').addEventListener('click', () => {void load();});
  byId('disconnect').addEventListener('click', clear);
  void health();
})();
