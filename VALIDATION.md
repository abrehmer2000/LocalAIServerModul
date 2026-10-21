# Prüfstand – 20.10.2026

## Ergebnis

**Meilenstein 1 ist technisch nachgewiesen:** Ein trainiertes Modell liefert eine echte,
gestreamte Antwort über das Gateway; API-Key und verpflichtende Benutzer-ID werden geprüft;
Annahme und Abschluss stehen mit Modell, Knoten und Benutzer-ID in SQLite.

Zusätzlich wurden **zwei unabhängige llama-server-Prozesse auf demselben Linux-Rechner**
über dasselbe Gateway angesprochen. Die Audit-Auswertung bestätigt beide Knoten.
Zusätzlich ist inzwischen auch der automatische Agent-Join mit zwei verwalteten Modellprozessen nachgewiesen (siehe unten). Getrennte Rechner sind noch nicht getestet.

## Ausgeführt und bestanden

Werkzeuge: Go 1.27.3, Linux amd64; offizielles OpenAI-Python-SDK 3.35.1.

```text
go vet ./...
[keine Befunde]

go test -race -count=1 -timeout=60s ./...
?    localaiserver/cmd/ki-basis [no test files]
ok   localaiserver/internal/audit
ok   localaiserver/internal/config
?    localaiserver/internal/event [no test files]
ok   localaiserver/internal/gateway
ok   localaiserver/internal/pool

go build -trimpath -o bin/ki-basis ./cmd/ki-basis
[erfolgreich]

GOOS=windows GOARCH=amd64 CGO_ENABLED=0 go build -trimpath -o bin/ki-basis.exe ./cmd/ki-basis
[erfolgreich; Cross-Build, kein Windows-Laufzeittest]
```

Die Tests prüfen insbesondere:

- Authentifizierung, verpflichtende Benutzer-ID und ungültige Anfragen.
- Live-Streaming vor Ende der Generierung und Modellalias in Antworten.
- Kapazitätsgrenzen bei 40 konkurrierenden Auswahlversuchen und Trennung verschiedener Modelle.
- Zwei getrennte Backends; keine Weiterleitung von Gateway-Key oder Benutzer-ID an die Engine.
- Unterbrochene Streams, Client-Abbruch und Zeitüberschreitung.
- Dauerhafte Ablehnung neuer Anfragen nach Audit-Schreibfehler.
- Parallele SQLite-Einträge, Wiederöffnung, Hash-Verkettung und erkannte Manipulation.
- Keine Prompts, Antworten oder geheimen Keys in den Audit-Ereignissen.

## Echte Inferenz

Engine: llama.cpp 0.7.0-dev, Build b11669, Commit 620d2225c.
Quelle: https://github.com/ggml-org/llama.cpp/releases/tag/b11669
SHA-256 des Linux-Archivs:
`cf07c1c112d709bd67e522017471de57ed0ec8bea031d732c5422351c07f65ac`

Modell: Karpathy TinyStories 15M, ein trainiertes kleines Llama-Sprachmodell.
Bezugsquelle: https://github.com/yeokm1/dosllam2/releases/tag/v0.2
Das dort vorhandene float32-Checkpoint wurde mit `scripts/prepare_test_model.py` ohne Training
in GGUF überführt. Die Checkpoint- und Archivprüfsummen sind im Skript festgehalten.
Das Modell ist eine Entwicklungsfixture, kein empfohlenes Kundenmodell.
Es erzeugt einfache Geschichten und beantwortet fachliche Fragen nicht zuverlässig.

GGUF-SHA-256:
`58ec5c3cd09ef9fd197cdbf16ee563b4acf9f419936f20b02e6ebb7617d8452d`

Geprüfte Aufrufwege:

1. Echte SSE-Anfragen mit Python-Standardbibliothek über Gateway zu zwei Modellinstanzen.
2. Echte Streaming-Aufrufe mit dem offiziellen OpenAI-Python-SDK zu beiden Instanzen.
3. Audit-Export: Benutzer-IDs, Anfrage-IDs, Knoten und UTC-Zeitstempel vorhanden;
   keine Prompttexte oder geheimen Keys; vollständige Kette verifiziert.

Das Ergebnis des SDK-Laufs steht in `validation/real-smoke-sdk.json`.
Die darin enthaltenen Antworttexte sind ausdrücklich Testausgaben, kein Audit-Export.
Der Lauf enthält fünf Audit-Ereignisse: Gateway-Start, zwei Annahmen und zwei Abschlüsse.

## Bezugsweg der Go-Abhängigkeiten

Der normale Go-Paketdownload wird in dieser Umgebung beim Weiterleitungsziel blockiert.
Die Pakete konnten stattdessen aus den offiziellen GitLab-/GitHub-Quellcode-Repositories
bezogen und als lokaler Go-Modulcache bereitgestellt werden.
**Go hat die Modul-Inhalte gegen sum.golang.org geprüft; die Prüfung wurde nicht deaktiviert.**
`go.mod` und `go.sum` verwenden unverändert die regulären Modulpfade und festen Versionen;
keine lokalen replace-Anweisungen, kein anderer SQLite-Treiber.

## Noch offen

- Laufzeit-, Dienst- und Installationstests auf echtem Windows.
- Zwei getrennte Rechner einschließlich TLS, Netzwerkausfall und Wiederbeitritt.
- Lasttest mit 40 echten LLM-Anfragen auf repräsentativer Hardware.
- Produktionsmodelle, Sicherheitsprüfung, Upgrades und Kundenpilot aus dem Implementierungsplan.

## Zweiter Meilenstein: dynamischer Agent-Join

Zusätzlich implementiert und erfolgreich geprüft:

- Einmalige, zehn Minuten gültige Join-Tokens; an Gerät, Adresse, Modellrevision und Kapazität gebunden.
- Atomare SQLite-Transaktionen für Token-Ausgabe, Join, Zertifikatserneuerung und Widerruf samt Audit-Eintrag.
- Gleichzeitiger Zugriff von Gateway und lokaler Verwaltungs-CLI: Schreibtransaktionen sperren vor dem Lesen der Kettenspitze.
- mTLS zwischen Gateway und Agent; fehlende Zertifikate sowie andere Geräteidentitäten werden abgewiesen.
- Prüfung von Engine-Bereitschaft und erwartetem Modellalias; abgelaufene Heartbeat-Freigaben werden nicht geroutet.
- Persistente Geräteidentitäten und automatische Gerätezertifikatserneuerung vor Ablauf.
- Optional gestartete llama-server-Prozesse mit Modell-SHA-256-Prüfung und begrenzten Neustartversuchen.
- Modellprozesse werden bei regulärem Beenden ihres Agents ebenfalls beendet.

Aktueller vollständiger Testlauf: `go test -race -count=1 -timeout=60s ./...`,
statische Analyse mit `go vet ./...` und Linux-/Windows-amd64-Builds erfolgreich.

Der CLI-Inferenztest `scripts/agent_smoke.py` hat folgenden Ablauf mit echten Modellprozessen durchlaufen:

1. TLS-Installation und leeres Gateway ohne statische Knoten starten.
2. Ersten Agent registrieren, Modell durch den Agent starten, Antwort über das Gateway erhalten.
3. Zweiten Agent hinzufügen, ohne das Gateway neu zu starten.
4. Echte gestreamte Antworten auf beide Agenten verteilen.
5. Gateway anschließend neu starten; beide laufenden Agenten melden sich mit ihrer bestehenden Identität zurück.
6. Erneute Inferenz auf beiden Knoten ohne neue Registrierung.
7. Ersten Knoten widerrufen; neue Anfragen gehen ausschließlich an den zweiten.
8. Zweiten Agent regulär beenden; seine Engine ist danach nicht mehr erreichbar und das Gateway antwortet mit fehlender Kapazität.
9. Audit-Kette verifizieren und prüfen, dass Prompt und API-Key nicht enthalten sind.

Ergebnis: `validation/agent-smoke.json`.
Die Agenten und Engines liefen auf demselben Linux-Rechner mit getrennten Ports und gegenseitig geprüften TLS-Identitäten.
Die Tests behaupten keine physische Mehrserver-, WAN- oder Windows-Laufzeitabnahme.

Noch offen sind Dienstinstaller, Modelldownloads, Hardwareprofile/vLLM,
Gateway-/CA-Zertifikatserneuerung, Admin-Oberfläche, Pilotbetrieb und Produktions-Sicherheitsprüfung.

## Webpreview und Standardprofil

- Gemma 4 26B A4B als gewünschtes Standardprofil und Join-Default hinterlegt; keine Gewichte automatisch installiert.
- Eingebettete Statusoberfläche und authentifizierter, auditierter Status-Endpunkt hinzugefügt.
- Entwicklungs-Webpreview auf Port 3000 gegen ein echtes lokales TLS-Gateway gestartet.
- Browserprüfung mit Chromium/Playwright bestanden: Seitenaufruf, Gateway-Health, authentifizierter Status,
  leere Knotenliste, Entfernen des eingegebenen Keys aus dem Formular, kein Local-/Session-Storage,
  Trennen der Sitzung und mobile Darstellung ohne horizontalen Überlauf.
- Tests prüfen, dass Statusantworten keine Backend-Adressen enthalten und die Vorschau weder Inferenz- noch Kontrollaufrufe weiterleitet.
- TLS-Verifikation zum Vorschau-Backend getestet; ungültiges Vertrauen ergibt Fehler.
- Vollständige Go-Tests mit Race Detection und statische Analyse bestanden.

Gemma selbst, GPU-Ausführung und Windows-Laufzeit sind weiterhin nicht abgenommen.
Die Pilotanleitung steht in `PILOT-TEST.md`.

## Dashboard und ausgehende Agenten (21.10.2026)

- `go test -race ./...` und `go vet ./...`: erfolgreich unter Linux.
- Neue Integrationstests: Verwaltungsrechte, einmalige Verbindungsdateien,
  Geräte-mTLS, zwei ausgehende Agenten, Streaming ohne Antwortpufferung,
  Abbruchweitergabe, Sperrung, persistente Modellaufträge, unveränderliche
  Modellversionen, fortsetzbare Downloads und Ablehnung beschädigter Dateien.
- Echter CLI-Test mit zwei getrennten llama.cpp-Prozessen und TinyStories 15M:
  `scripts/agent_smoke.py --outbound`; beide Agenten liefern Inferenz, zweiter Join
  ohne Gateway-Neustart, Erholung nach Gateway-Neustart, Sperrung und geordneter
  Engine-Stopp erfolgreich. Audit-Kette gültig (31 Einträge im Testlauf).
- Echter Chromium-Browsertest: `scripts/dashboard_smoke.py`; Verbindungsdatei im
  Dashboard erzeugt, im lokalen Agenten importiert, Geräteanmeldung über echtes
  TLS, Darstellung im Dashboard und Audit bestätigt. Keine JS-Laufzeitfehler,
  keine Zugangsdaten im Local Storage; Trennen leert sensible Ansichten.
- Modelldownloads werden mit kontrollierten HTTP-Antworten auf SHA-256, falsche
  Dateien und Range-Fortsetzung getestet. Ein neuer echter Hugging-Face-Download
  aus dieser Entwicklungsumgebung ist weiterhin durch deren Netzwerkpolicy
  eingeschränkt; dieser Test ersetzt keinen Gemma-Download auf Kundengeräten.
- Der bestehende Gemma/A40-Test im Nutzer-RunPod betraf die vorherige direkte
  Agentenverbindung. Das Dashboard-Update wurde dort noch nicht installiert.
- Windows-/Linux-Pakete enthalten Agent- und Zentralenstarter. Native Autostart-
  Integration, Desktopstarter und GPU-Laufzeiten benötigen weiterhin Tests auf
  den jeweiligen Zielrechnern. Cross-Builds sind keine nativen Betriebssystemtests.

## Geführte Einrichtung und SSH-Manager

Der anbieterneutrale Ablauf und seine Grenzen stehen in
[SETUP-ASSISTENT.md](SETUP-ASSISTENT.md). Der Produktivumfang und offene
Freigabekriterien stehen in [PRODUCTION-PLAN.md](PRODUCTION-PLAN.md).

Zusätzliche Prüfungen:

```sh
node scripts/dashboard_stream_test.cjs
python3 scripts/dashboard_smoke.py --gateway ./bin/ki-basis --runtime /pfad/zu/llama-server
python3 scripts/ssh_setup_smoke.py --gateway ./bin/ki-basis --runtime-archive /pfad/zu/llama-runtime.tar.gz
```

Die Browser-/SSH-Tests brauchen freie Loopback-Ports. Der gewählte temporäre
Datenträger muss die Vorprüfung für das Standardmodell bestehen (mindestens
etwa 14,7 GB frei); bei Bedarf `TMPDIR` auf einen ausreichend großen Datenträger
setzen. Das Laufzeitarchiv stammt im Test vom Betreiber, nicht aus einem
ungeprüften automatischen Download. Der SSH-Test verwendet eigene kurzlebige
Schlüssel und einen isolierten lokalen sshd, keine Cloud-Zugangsdaten.


## Stabilisierung der Einrichtung

`go test -race ./...` prüft zusätzlich den gemeinsamen Backup-Vertrag mit
Prüfsummen, Trennung der Sicherungsrollen, korruptes Gzip, begrenzte
Alt-Pilotmigration, bereinigte SSH-Diagnosen und Prozesssperren. Windows prüft die
native Dateisperre im normalen Testlauf.

Der SSH-Smoke prüft inzwischen auch eine fehlende Zielserver-Verbindung,
Wiederaufnahme mit abgelaufener passender Datei, Neustart des beendeten Agenten
und Abweisung einer fremden Identität. Der Browser-Smoke restauriert eine Zentrale
auch grafisch und prüft anschließend die Audit-Kette.
Ein Systemboot, eine tatsächlich laufende systemd-Installation und echte GPUs
werden damit nicht simuliert; dafür gilt [TEST-MORGEN.md](TEST-MORGEN.md).
