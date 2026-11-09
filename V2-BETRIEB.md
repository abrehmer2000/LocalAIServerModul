# V2: Umsetzung, Migration und Abnahme

Dieser Branch setzt den Vertrag [ANFORDERUNGEN-V2.md](ANFORDERUNGEN-V2.md) um.
Er ist bis zur Abnahme der tatsächlichen Engine und Zielhosts ein Testkandidat.
Ein grüner synthetischer Test ersetzt weder eine Modellprobe noch den 24-h-Lauf.

## API und Scheduler

- `POST /v1/chat/completions`: Text oder ein eingebettetes Bild, nicht streamend
  oder SSE. Antwortalias bleibt stabil. `id`, `finish_reason` und echte Engine-
  Nutzungszähler sind im nicht streamenden Protokoll verpflichtend.
- `X-User-ID` optional; falls vorhanden exakt 1–64 Zeichen gemäß Vertrag.
  `X-Request-ID` wird bei gültigen 1–32 ASCII-Buchstaben/Ziffern/Bindestrichen
  übernommen, sonst neu erzeugt. Der Wert steht auch im Audit.
- Ein Slot genügt. Je Alias standardmäßig acht zusätzliche Wartende, 120 s
  Wartezeit. FIFO innerhalb einer Prioritätsklasse; `X-Priority: interactive`
  vor normal. Nach 30 s bekommt ein wartender normaler Aufruf Vorrang gegen
  Verhungern. Laufende Aufgaben werden nicht verdrängt.
- `model_busy` bei voller/abgelaufener Queue, `model_loading` beim Laden,
  `model_unavailable` bei fehlender Engine oder Drain. `Retry-After` ist eine
  aktuell berechnete, auf 1–30 s begrenzte Schätzung, kein exakter Fertigtermin.
- Kein 5xx wegen ungültigem **generiertem Text**. Insbesondere unvollständiges
  JSON mit `length` bleibt unverändert im HTTP-200. Eine vollständige, dennoch
  nicht konforme Ausgabe bekommt eine Metadaten-Qualitätsmarkierung; sie darf
  nicht als bestandene Modellabnahme gewertet werden. Ein beschädigtes äußeres
  Engine-Protokoll ohne Pflichtfelder ist dagegen ein technischer 502.
- Das gepinnte llama.cpp übernimmt `response_format`-Grammatik/Schema-Decoding.
  Die konkrete Engineversion muss die gewünschten Schemafunktionen tatsächlich
  unterstützen. Schemas >64 KiB: 413, ungültige Schemas: 400, externe Referenzen
  werden nicht geladen. Kein Reparieren oder verstecktes Nachgenerieren.
- Denken wird je Aufruf übergeben; das Runtimeprofil verlangt getrenntes
  Reasoning. Explizite Legacywrapper am Anfang werden getrennt, auch über
  SSE-Chunkgrenzen hinweg. Normale spätere Zitate von `<think>` bleiben Text.
- Keine feste Ausgabegrenze bei fehlendem `max_tokens`; Runtime `--predict -1`.
  `max_output_tokens` begrenzt explizite Clientlimits. Das freie Kontextfenster
  begrenzt weiterhin die tatsächliche Ausgabe.
- `engine_protocol: llama.cpp` aktiviert die Kontextvorprüfung über
  `/apply-template` und `/tokenize` desselben Backends, auch durch den Agenttunnel.
  Bildtokenprüfung bleibt Aufgabe der Engine, mit normiertem 4xx statt Kürzung.

## Profile, Zeit- und Speichergrenzen

Neue Standardwerte: 900 s Erzeugung, 120 s Queue, 16 MiB Body, 64 gleichzeitig
verarbeitete API-Requests. Das Gateway akzeptiert höchstens das Vierfache dieses
Requestlimits als aktive Sockets; weitere Sockets warten im begrenzten
Betriebssystem-Backlog. HTTP/2 ist im Produktions-Serve-Pfad deaktiviert, damit
Clients die HTTP/1-Requestgrenze nicht über unbegrenzte Streams umgehen.
Erreichbare HTTP-Überlast wird als 429 gemeldet. Interne Steuer-/Abbruchwege
teilen keine Modellwarteschlange.

`max_body_bytes` kann bewusst bis 128 MiB erhöht werden. Gateway, signierte
Agentidentität und Tunnel müssen denselben Wert haben; vorhandene Agenten mit
kleinerer signierter Grenze neu einschreiben. Bodypuffer und parallele große
Anfragen bei der RAM-Planung berücksichtigen. Ein Bild bleibt auf 8 MiB begrenzt.

`timeout_seconds` muss beim Produktionsstart mindestens
`max(600, client_timeout_seconds + queue_wait_seconds)` sein. Vorgabe Client 420 s.
Das Browserbudget ist Queue + Erzeugung + 60 s Reserve, also standardmäßig 1080 s
nach dem Upload. Externe Reverse Proxies entsprechend einstellen. Für abweichende
Budgets beim eigenständigen Browserpreview dessen Profil ebenfalls anpassen.

`GET /v1/models/ALIAS/capabilities` liefert Kontext/Slot, Plätze frei/belegt,
Queue, Vision, Revision, Ausgabe- und Zeitgrenzen. `/healthz` benötigt keinen
Modellaufruf und liest Schedulerzustand ohne synchronen Auditcommit.
`GET /metrics` braucht einen Adminschlüssel und liefert feste Status-/Codezähler,
Dauerklassen, Tokens und Poolzustand. Keine Nutzer-/Request-IDs als Metriklabels.

## Installation migrieren

Vor der Umstellung liefert `ki-basis inspect --config CONFIG` schreibfrei Buildhash,
bereinigte Konfigurationswerte und deklarierte Profile ohne Zugangsdaten. `runtime_verified`
ist dabei bewusst false: tatsächliche Engineflags/Hostzustände separat erfassen.

1. Aktuelles Binary, private Gateway-/Agenteinstellungen und konsistente
   Auditdaten sichern; Build- und Modellrevision festhalten. Keine Umstellung
   während eines Imports.
2. Neues Profil separat erzeugen:

   ```sh
   ki-basis prozesslog-profile --config /privat/config.json --output /privat/config.v2.json
   ```

   Der Befehl überschreibt nichts, behält höhere gültige Grenzwerte bei und
   setzt mindestens das V2-Profil. Relative Dateipfade müssen zum Dienst-
   Arbeitsverzeichnis passen; für den Server absolute Pfade verwenden.
3. Reale Engine-Slotzahl und Kontext je Slot ≥32768 bestätigen. Keine Kapazität
   nur im Gateway hochsetzen. Modell-/Projektor-/Template-/Binaryhash festlegen.
4. Verwaltete Starts verlangen `runtime.engine_sha256`. Bewusstes Verbinden,
   Speichern der Agent-Einstellungen oder Bootstrap pinnt die explizit gewählte
   Datei. Ein später unter demselben Pin geändertes Binary wird nicht gestartet.
   CLI-Agent: `--engine-sha256 HASH`, optional `--chat-template-file PFAD`
   und `--chat-template-sha256 HASH`. Bestehende explizite Kontexte unter 32k
   müssen migriert werden; sie werden nicht heimlich weiterverwendet.
5. Neue Einladungen/Nodeprofile können `vision`, `context_per_request`,
   `engine_protocol`, `engine_sha256`, `template_sha256` und Bodygrenze festlegen.
   Replikate desselben Alias müssen gleiche Revision/Fähigkeiten haben.
   Llama-Agenten prüfen `/props` vor Bereitschaft gegen Slotzahl, Kontext und
   Vision. Die Templateprüfsumme wird im Startaudit mitgeführt.

Die Runtime verwendet `--jinja --reasoning-format deepseek --cache-ram 0
--no-context-shift --log-disable --predict -1`. Keine automatische Ausweichoption,
kein Slot-Speicherpfad, `cache_prompt:false` im Chat. Eine inkompatible Engine
wird als Fehler sichtbar. Gewichtsdateien dürfen auf Platte liegen, Nutzdaten
und KV-/Slotzustände nicht. Externe Engines müssen dieselben Zusagen nachweisen.

## Dienste und geordnetes Beenden

Windows: `packaging/windows/Install-Services.ps1` installiert Gateway/Agent als
automatische Dienste mit eigenen virtuellen Konten `NT SERVICE\KI-Basis-…` und
Wiederherstellung nach 5/10/30 s. Die angegebenen Datenordner bekommen das
Servicekonto als berechtigten Nutzer. Prüfen, dass Binary, Modelle und Zertifikate
für dieses Konto zugänglich, aber nicht allgemein lesbar sind. Die Installation
startet keinen Dienst und startet den Host nicht neu.

Direkter SCM-Aufruf:

```powershell
ki-basis.exe service install --name KI-Basis-Gateway -- serve --config C:\KI-Basis\central\config.json
ki-basis.exe service install --name KI-Basis-Agent -- agent-ui --dir C:\KI-Basis\agent --open-browser=false --quiet-setup
```

Linux: Vorlagen in `packaging/systemd/`, Konto `ki-basis`, absolute Pfade,
`LimitCORE=0`, geschützte Dateirechte. Das bisherige User-Autostart-/HKCU-Profil
ist kein Nachweis für Betrieb ohne Anmeldung. Dienstkonten/Dateipfade vor dem
Aktivieren vorbereiten; keine laufende Installation blind überschreiben.

Gateway/Agent ziehen sich vor geplantem Ende aus der Vergabe zurück und warten
auf laufende Erzeugungen. Das Gateway lässt während des Drains seinen Listener
offen, neue Modellaufträge erhalten 503 mit Retryheader. Queueeinträge werden
geweckt/beendet. Erst danach werden Server/Tunnel/Engine geschlossen. Windows
Engineprozesse gehören zu einem Job Object mit Kill-on-close, Unix zu einer
eigenen Prozessgruppe. Reboots, SIGKILL und Stromausfall benötigen gesonderte
Betriebs-/Recoverybelege; ein bereits getrenntes Netzwerk kann keinen HTTP-
Status mehr transportieren.

## Wartung und Importschutz

Ein bestehendes laufendes Modell stoppen/ersetzen benötigt ein angekündigtes
Fenster (`PUT /admin/maintenance-window`, Admin, JSON `from`/`to` als RFC3339).
Außerdem darf keine Importlease offen sein. Das gilt auch im Schutzfenster
20:00–06:00 Europe/Berlin; dort erfolgt keine automatische Wartung. Die Uhrzeit
allein gestattet auch tagsüber keinen unangekündigten Modelltausch.

Prozesslog erwirbt für Importarbeit `POST /v1/import-leases` mit
`{"ttl_seconds":120}`, erneuert über `PUT /v1/import-leases/ID` und gibt am Ende
mit DELETE frei. Es werden nur Lease-/Schlüsselkennungen und Zeit gespeichert.
**Ablauf entsperrt keine Wartung**: Verlust des Workers muss sichtbar bleiben.
Nach Prüfung des beendeten Imports kann Admin eine verwaiste Lease über
`DELETE /admin/import-leases/ID` auditierbar freigeben. Kein Datenbankeingriff nötig.

## Schlüssel und Audit

Schlüssel tragen nur Hash, ID, Adminrecht und optionale `models`-Liste. Fehlende/
null Liste erhält bisherige Rechte auf alle Aliase; `[]` erlaubt kein Modell.
`PUT /admin/keys` ersetzt die vollständige gültige Schlüsselliste atomar und
persistiert sie mit dem Auditereignis in einer Transaktion. Neue Requests sehen
Widerrufe sofort; laufende bereits genehmigte Aufträge dürfen fertig werden.
Nach der ersten Rotation ist der persistente Schlüsselsatz maßgeblich, auch
nach Neustart. Vor Rotation einen zweiten geprüften Adminzugang erhalten.

Requestmetadatenlogs rotieren bei höchstens 100 MB, sieben Dateien insgesamt.
Audit bleibt eine getrennte Append-only-Hashkette. Externen signierten Prüfpunkt
initialisieren:

```sh
ki-basis audit-checkpoint --config CONFIG --signing-key PRIVATEKEY --output EXTERNER-PRUEFPUNKT
ki-basis audit-checkpoint --config CONFIG --verify --public-key VERTRAUTER-PUBKEY --output EXTERNER-PRUEFPUNKT
```

`public_key` aus dem initialen Prüfpunkt unabhängig geschützt als Base64-Datei
ablegen. Private Signaturdatei und Prüfpunkt dürfen nicht gemeinsam mit der
Auditdatenbank beliebig zurückgerollt werden. Mit `audit_signing_key` und
`audit_checkpoint` in der Konfiguration verifiziert der Produktionsstart den
gespeicherten Anker und aktualisiert ihn minütlich. Ein Ankerfehler führt in den
Drain. Ein allein lokal gespeicherter Anker beweist keinen Schutz gegen einen
privilegierten Rollback des ganzen Rechners.

## Abnahme auf Zielhardware

1. `go test -race -count=1 -timeout=120s ./...`, `go vet ./...`, Builds für
   Linux/Windows sowie Dashboard-Streamtest. Lokale Tests bilden Lade-/Fehler-
   /Queue-/Abbruchzustände deterministisch ab.
2. Mit ausschließlich synthetischen Daten, Schlüssel über Umgebungsvariable:

   ```sh
   python scripts/acceptance_v2.py --base https://HOST/v1 --model ALIAS --report api-v2.json
   python scripts/host_preflight_v2.py --report host-v2.json
   python scripts/soak_v2.py --base https://HOST/v1 --model ALIAS --hours 24 --interval 10 --pids GATEWAY_PID AGENT_PID ENGINE_PID --report soak-v2.json
   ```

   Für Prozessbeobachtung benötigt das Lastwerkzeug `psutil`. Reports enthalten
   keine Prompt-/Antworttexte oder Schlüssel. Offene Hostnachweise bleiben
   ausdrücklich offen; Skripte erteilen keine automatische Kundenfreigabe.
3. Echte Engine: 3000-Token-Ausgabe ohne Limit, fünf Token mit `length`, Thinking
   an/aus (<50 % Completiontokens bei aus), echte 20000-Token-Eingabe und
   Überkontext, 20 JSON-Objekte (≥19 parsebar), Schemas, Bildauswertung bei
   passendem Profil. Gekürztes JSON bleibt unverändert/200/`length`. Unicode-
   Zitate exakt vergleichen. `temperature:0` fünfmal, mindestens vier identisch.
4. Faultinjektion: Ladephase, fehlende Engine, volles Modell und volle Queue,
   Queueabbruch, nach 3 s getrennter Langauftrag; echte Slotfreigabe ≤5 s.
   50 sequentielle ohne Abweisung, zwei parallele erfolgreich, ein laufender
   plus acht wartende Plätze, zehnter kontrolliert abgewiesen. Priorität und
   Audit-/HTTP-Codegleichheit kontrollieren.
5. 450-s-Erzeugung bei 700-s-Client, definierte Engine-Deadline, geplanter
   Neustart während 60-s-Aufruf → 200 oder 503, kein stiller Verbindungsabbruch.
6. Host neu starten ohne Anmeldung → Antwort ≤10 min, Engine beenden → wieder
   bereit ≤2 min. Wiederholte Ausfälle testen; keine Restkindprozesse.
7. TLS mit `openssl s_client`: Host, eigene CA und ≥1.2. Prompt-/Antwort-/Bild-
   /Denktext-Canaries danach in Audit, WAL, Logs und Datenordnern suchen: keine
   Treffer. WER/LocalDumps/Core-Dumps, Swap/Pagefile und Hibernation separat
   belegen: aus oder nachweislich geschützt verschlüsselt. Unknown ist kein Pass.
8. 100 `max_tokens:1` direkt/über Gateway, Median-Aufwand ≤100 ms, p99 ≤500 ms.
   24 h einschließlich vollständigem Nachtfenster: ab einer Aufwärmstunde RSS
   je Prozess ≤+10 %, keine wachsende Queue/Restprozesse, Gatewayfehler <0,5 %.
   Absichtliche Überlast/Abbrüche separat ausweisen, nicht verstecken.
9. Prozesslog: Import, Chat, Navigator, Bild an Textmodell, gekürztes JSON und
   Importschutz tatsächlich zusammen testen. Versionspaar dokumentieren.

Freigabe erst, wenn jede MUSS-ID einen bestandenen Beleg hat. Vorherige Binaries,
Konfiguration und konsistente Daten für Rückweg halten. Beim Rollback aktuelle
Widerrufe und Importleases beachten; keine alten Schlüssel/Identitäten still
reaktivieren. Der Host wurde durch diese Codeänderung nicht umgestellt.

## Optionale Embeddings

`POST /v1/embeddings` benötigt einen expliziten `kind: embedding`-Knoten mit
gepinntem geeignetem Modell. Text oder bis 128 Texte, Floatvektoren mit geprüfter
einheitlicher Dimension/Indexfolge. Derselbe TLS-/Schlüssel-/Audit-/Queuepfad,
kein Wechsel auf das Chatmodell. Ein passend gestarteter externer llama.cpp-
Embeddingserver kann statisch oder über den Agentproxy angebunden werden.
Der bisherige lokale Prozesslog-Embeddingpfad muss dafür nicht geändert werden.
