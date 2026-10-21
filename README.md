# KI-Basis – lokaler LLM-Zugang

Erste Implementierung eines OpenAI-kompatiblen Gateways für getrennte Kundeninstallationen.
Go, SQLite und vorhandene Inferenz-Engines; keine Usecase-Bewertung, keine Software-Lizenzprüfung, keine Telemetrie.

## Aktueller Umfang

- `POST /v1/chat/completions`: Textnachrichten, JSON-Antworten und echtes SSE-Streaming.
- `GET /v1/models`: konfigurierte Modellnamen.
- Bearer API-Keys, nur als SHA-256-Hash in der Konfiguration.
- Verpflichtende pseudonyme `X-User-ID` für beide API-Endpunkte.
- SQLite-Audit mit angenommenen, abgeschlossenen, fehlgeschlagenen und abgewiesenen Anfragen.
- Hash-Verkettung, Integritätsprüfung beim Start, JSONL-Export und Prüfanker.
- Mehrere statisch konfigurierte Modellserver; kapazitätsgewichtete Auswahl und Überlastgrenzen.
- TLS für entfernte Backends und nichtlokale Gateway-Listener erzwungen.

**Entwicklungsstand:** Der Node-Agent unterstützt jetzt einmaligen Join, mTLS, Heartbeats,
dynamische Anmeldung ohne Gateway-Neustart, Widerruf und automatische Gerätezertifikatserneuerung.
Optional startet er llama-server mit Modell-Prüfsummenprüfung und begrenzten Neustartversuchen.
Siehe `AGENT-SETUP.md` und `examples/config-agents.json`.
Die direkte statische Backend-Konfiguration bleibt für einfache Einzelserver nutzbar.
Das Dashboard unterstützt jetzt Serveranmeldung per Verbindungsdatei, ausgehende Agentenverbindungen,
Modell-Downloads mit Fortschritt und SHA-256-Prüfung, Start/Stopp eigener Modellprozesse,
Audit-Ansicht und einen Streaming-API-Test. Administratorrechte sind von Anwendungs-Keys getrennt.
Die lokale Agenteneinrichtung öffnet sich per Starter im Browser. Siehe [DASHBOARD-SETUP.md](DASHBOARD-SETUP.md).
Vollständige Benutzerkontenverwaltung, zusätzliche Downloadkataloge, Embeddings, Tool-Calling,
signierte Systeminstaller und die automatische Installation von GPU-Treibern/Laufzeiten fehlen noch.
Eine Kundenauslieferung benötigt weitere Betriebssystem-, Installations- und Sicherheitstests.

## Bauen und prüfen

Go 1.23 oder neuer, empfohlen die aktuelle gepflegte Go-Version. Kein C-Compiler für SQLite notwendig.

```sh
go mod download
go test -race -count=1 ./...
go vet ./...
go build -o bin/ki-basis ./cmd/ki-basis
```

Windows PowerShell:

```powershell
go build -o bin/ki-basis.exe ./cmd/ki-basis
```

## Erster echter Modellaufruf

1. Eine aktuelle llama.cpp-Version mit `llama-server` und ein dazu passendes GGUF-Modell bereitstellen.
   Modellquelle und Modelllizenz prüfen. Engine-Version und SHA-256 der Gewichte dokumentieren.
2. Engine starten (unter Windows `llama-server.exe`):

```sh
llama-server --model /pfad/modell.gguf --alias gemma-4-26b-a4b --host 127.0.0.1 --port 8081 --ctx-size 4096 --parallel 1
```

3. Schlüssel **einmal lokal** erzeugen:

```sh
./bin/ki-basis keygen
```

Die Ausgabe enthält `key` und `sha256`. Den Schlüssel sicher speichern; nur den Hash in die Konfiguration übernehmen.
Ausgaben nicht in Git, Supportpakete oder gemeinsam genutzte Logs kopieren.

4. `examples/config.json` nach `config.local.json` kopieren, Hash und `revision` ersetzen.
   Die Revision soll Modellprüfsumme, Quantisierung und Engine-Version eindeutig bezeichnen.
5. Gateway starten:

```sh
./bin/ki-basis serve --config config.local.json
```

6. Anfrage senden; `KI_BASIS_API_KEY` vorher über die lokale sichere Umgebung setzen:

```sh
curl --no-buffer http://127.0.0.1:8080/v1/chat/completions \
  -H "Authorization: Bearer $KI_BASIS_API_KEY" \
  -H 'X-User-ID: test-user-01' \
  -H 'Content-Type: application/json' \
  -d '{"model":"gemma-4-26b-a4b","messages":[{"role":"user","content":"Antworte kurz auf Deutsch: Was ist eine lokale KI?"}],"stream":true,"max_tokens":64}'
```

7. Audit prüfen und exportieren:

```sh
./bin/ki-basis audit-verify --config config.local.json
./bin/ki-basis audit-export --config config.local.json
```

`sequence` und `head_hash` regelmäßig außerhalb des Gateway-Rechners sichern.
Eine Hash-Kette allein erkennt kein vollständiges Neuschreiben oder Abschneiden ihres Endes durch einen Angreifer mit vollständigem Dateizugriff.

### Offizielles Python-SDK

```python
import os
from openai import OpenAI

client = OpenAI(base_url="http://127.0.0.1:8080/v1", api_key=os.environ["KI_BASIS_API_KEY"])
stream = client.chat.completions.create(
    model="gemma-4-26b-a4b",
    messages=[{"role": "user", "content": "Erkläre lokale KI in einem Satz."}],
    max_tokens=64,
    stream=True,
    extra_headers={"X-User-ID": "test-user-01"},
)
for chunk in stream:
    if chunk.choices:
        print(chunk.choices[0].delta.content or "", end="", flush=True)
```

### Automatisierter echter Inferenztest

```sh
python scripts/real_smoke.py --gateway bin/ki-basis --engine /pfad/llama-server --model /pfad/modell.gguf
```

Optional kann derselbe Test mit dem offiziellen OpenAI-Python-SDK ausgeführt werden:

```sh
python -m pip install -r scripts/test-requirements.txt
python scripts/real_smoke.py --gateway bin/ki-basis --engine /pfad/llama-server --model /pfad/modell.gguf --sdk
```

Ein kleines trainiertes Testmodell lässt sich alternativ über einen GitHub-Release beziehen und in GGUF konvertieren:

```sh
python scripts/prepare_test_model.py --output work/tinystories15M.gguf
```

Das ist ausschließlich eine technische Testfixture; sie eignet sich nicht als Kundenmodell.
Herkunft und Prüfsummen sind in `VALIDATION.md` dokumentiert.

Das Skript startet zwei **echte** llama-server-Prozesse mit demselben Modell, ruft das Gateway auf,
prüft Streaming und Audit-Zuordnung zu beiden Knoten und beendet alle Prozesse.
Es benötigt genügend RAM für zwei Modellinstanzen. Es lädt keine Modelle herunter.

## Zweiten Server anbinden

Für den ersten lokalen Nachweis eine zweite Engine auf Port 8082 mit demselben Modell und Alias starten.
Einen zweiten Eintrag in `nodes` ergänzen:

```json
{
  "id": "local-02",
  "url": "http://127.0.0.1:8082",
  "model": "gemma-4-26b-a4b",
  "upstream_model": "gemma-4-26b-a4b",
  "revision": "GLEICHE_REVISION_WIE_LOCAL_01",
  "capacity": 1
}
```

Für einen anderen Rechner muss `url` eine HTTPS-Adresse sein. `ca_file` kann die vertrauenswürdige private CA enthalten.
`key_env` kann den Namen einer Umgebungsvariable mit dem Engine-API-Key enthalten.
Gateway-API-Key und Benutzer-ID werden niemals an die Engine weitergereicht.
Eine Engine nur im privaten Netz bereitstellen und auf Gateway-Zugriff begrenzen; der Prototyp hat noch für statische Backends keine automatische mTLS-Geräteregistrierung. Für Agenten `AGENT-SETUP.md` verwenden.

## API-Vertrag

Unterstützte Felder: `model`, `messages` (Text; Rollen system/developer/user/assistant; optional name),
`stream`, `max_tokens` oder `max_completion_tokens`, `temperature`, `top_p`, `stop`, `seed`,
`frequency_penalty`, `presence_penalty`, `stream_options.include_usage`, `n` (nur 1), optional `user`.
Ein gesetztes `user` muss `X-User-ID` entsprechen und wird vor der Weiterleitung entfernt.
Unbekannte Felder werden abgewiesen. Tool-Calling, Bilder und weitere Endpunkte werden noch nicht unterstützt.
Die tatsächliche Parameterunterstützung hängt außerdem von der verwendeten Engine ab.

- `X-User-ID`: 1–128 ASCII-Zeichen, Buchstaben/Ziffern sowie `._:-`; Beginn mit Buchstabe/Ziffer.
- Die Kundenanwendung muss die ID aus ihrer Anmeldung ableiten. Sie ist keine vom Gateway geprüfte Personenidentität.
- Jede Anfrage erhält `X-Request-ID` zur Audit-Zuordnung.
- Fehlende/falsche Keys: 401. Fehlende Benutzer-ID/ungültige Anfrage: 400. Unbekanntes Modell: 404.
- Belegte/temporär ausgefallene Knoten: 503 + `Retry-After: 1`. Noch keine Warteschlange.
- Engine-Fehler: 502; Zeitüberschreitung vor Streambeginn: 504.
- Bei Streamfehlern nach Antwortbeginn: Fehlerereignis ohne `[DONE]`. Keine automatische Wiederholung.
- Kein stiller Modellwechsel. Ein öffentlicher Modellalias muss auf allen Replikaten dieselbe deklarierte Revision haben.
- Ausgabe- und Körpergrößen sowie Laufzeiten sind begrenzt. Die tatsächliche Kontextgrenze muss zusätzlich in der Engine konfiguriert werden.
- Keine per-Key-Kontingente oder fein abgestuften Berechtigungen im ersten Stand.

## Audit und Betrieb

Audit enthält ausschließlich die Felder des strukturierten Event-Schemas: Zeit, Sequenz, Anfrage-ID,
Key-ID, Benutzer-ID, Modell, deklarierte Revision, Knoten, Status, Ergebnis, Laufzeit und Tokenzahlen, soweit geliefert.
Weder Inhalte noch Inhaltsprüfsummen oder rohe Engine-Fehlermeldungen werden gespeichert.
Abgewiesene unbekannte Keys werden ohne vom Aufrufer behauptete Identität erfasst.

Vor Inferenzbeginn muss der Annahmeeintrag dauerhaft gespeichert sein.
Schlägt eine Audit-Schreiboperation fehl, nimmt der Prozess bis zur Fehlerbehebung und einem Neustart keine neuen API-Anfragen an.
Eine bereits ausgegebene Antwort lässt sich bei nachträglichem Speicherausfall nicht zurücknehmen;
der vorherige Annahmeeintrag bleibt als unvollständiger Vorgang nachvollziehbar.

Der Gateway-Start wird erfasst. Agent-Registrierung, Token-Ausgabe und Widerruf werden zusätzlich mit Geräteidentität beziehungsweise lokalem OS-Benutzernamen protokolliert. Konfigurationsänderungen über eine Datei sind weiterhin keine personengebundenen Verwaltungsaktionen.
Manuelle Dateiänderungen müssen betrieblich kontrolliert werden; Dashboard-Aktionen werden mit Administrator-Benutzer-ID protokolliert.

SQLite im lokalen Datenverzeichnis betreiben, nicht auf einer Netzwerkfreigabe. Dienste ohne erhöhte Rechte ausführen.
Unter Windows die ACL des Datenverzeichnisses auf das Dienstkonto und berechtigte Administratoren beschränken.
Für den jetzigen Stand: Gateway für Backups beenden und das komplette Datenverzeichnis konsistent sichern.
Automatische Aufbewahrungsfristen, Backupbefehl und sichere Schema-Upgrades folgen vor dem Kundenpilot.

`/healthz` zeigt die Erreichbarkeit des HTTP-Prozesses, keine Garantie für Modell- oder Audit-Verfügbarkeit.

## Qualitätsnachweise

Tests prüfen Authentifizierung und Benutzer-ID, fail-closed bei Auditfehlern,
Überlastgrenzen, frühzeitige Übertragung eines SSE-Chunks, Verteilung auf zwei Backends,
unterbrochene Streams, Metadaten ohne Inhalte, parallele Audit-Schreibvorgänge und Manipulationserkennung.
Die HTTP-Testserver simulieren die Engine; ein bestandener Test ersetzt keinen echten Modelltest.

Siehe `IMPLEMENTATION.md` für Etappen und Grenzen.

## Standardmodell und Statusoberfläche

Gewünschter Standard ist Gemma 4 26B A4B (MoE), offizielles GGUF-Profil `google/gemma-4-26B-A4B-it-qat-q4_0-gguf`.
Der Alias `gemma-4-26b-a4b` ist in Beispielen und Join-Defaults hinterlegt.
Das hinterlegte Profil bedeutet keine erfolgte Installation und ersetzt keinen Hardwaretest.

Die Statusoberfläche ist im Gateway unter `/` eingebettet, ohne separate Frontend-Abhängigkeiten.
`GET /v1/status` benötigt Key und Benutzer-ID und wird im Audit erfasst. Es liefert Knotenstatus ohne Adressen oder Geheimnisse.
Statische Backends werden als konfiguriert angezeigt; als bereit zählen nur Agenten mit gültiger Bereitschaftsmeldung.
Der Key bleibt in der Oberfläche ausschließlich im Arbeitsspeicher des Tabs.

Lokales Webpreview: `python3 scripts/dev_preview.py` (Port 3000).
Vier-Plattform-Pilot und Gemma-Startparameter: `PILOT-TEST.md`.

## Mac mit Apple Silicon: Vorschau ohne Entwicklungswerkzeuge

Das Paket aus dem GitHub-Workflow „Mac Apple Silicon Preview“ vollständig entpacken und `Start.command` öffnen.
Es enthält eine native macOS-arm64-Datei. Die lokale Vorschau benötigt weder Go/Python noch Modellgewichte.
API-Key und Benutzer-ID erscheinen im lokalen Terminal. Daten und Schlüssel bleiben im Verzeichnis `Daten`.
Manueller Start: `./ki-basis quickstart --dir ./Daten --open-browser`.
Der Entwicklungsbuild ist unsigniert und kann eine macOS-Freigabe erfordern.
