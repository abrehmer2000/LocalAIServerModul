# Prozesslog: Gateway-Test und Rückweg

Dieses Profil erweitert den bisherigen Texteinstieg um eingebettete Bilder und
strukturierte, nicht gestreamte Antworten. Es ist ein Testkandidat; reale
MoE-/Vision-Qualität, GPU-Speicherbedarf und betriebliche Freigabe bleiben separat
nachzuweisen. Die Prozesslog-Nachtprüfung bleibt ohne Gateway.

## Versionspaar festhalten

Prozesslog bleibt in seinem eigenen Repository auf
`qualitaet/gateway-navigator-moe` mit den dort zu prüfenden Commits `4a25904`,
`89f1b93`, `89bc626`. Diese Änderungen gehören nicht in dieses Repository.
Für jeden Test vollständigen Prozesslog- und Gateway-Commit, Build-Prüfsumme,
Betriebssystem, Engine-Version, Modell-/Projektor-SHA-256, Quantisierung,
Chat-Template und bereinigte Konfiguration dokumentieren. API-Schlüssel,
Kundendaten, Zertifikatsschlüssel und lokale Konfigurationen nicht einchecken.

## Bestehende Installation vorbereiten

1. Bestehendes Binary, Gateway-Konfiguration und Agent-Einstellungen privat
   sichern; reguläre Datensicherung und Audit-Prüfung durchführen.
2. Neue, getrennte Konfiguration erstellen:

   ```sh
   ./bin/ki-basis prozesslog-profile --config config.local.json --output config.prozesslog.local.json
   ```

   Der Befehl überschreibt keine Datei. Er setzt 300 Sekunden Inferenzbudget,
   16 MiB Anfragegröße und mindestens 8192 erlaubte Ausgabetoken. Schlüssel,
   Datenbank, Zertifikate und Knoten-Kapazitäten bleiben erhalten. Relative Pfade
   beziehen sich weiterhin auf das Arbeitsverzeichnis des gestarteten Dienstes.
   Die Quelldatei ist unverändert. Neue Defaults migrieren keine gespeicherten Werte.
3. Engine und Gateway-Kapazität zusammen konfigurieren. Im statischen Betrieb
   die Kapazität des gewählten Modells auf mindestens vier setzen, nachdem vier
   echte Engine-Slots verfügbar sind. Bei eingeschriebenen Agenten ist die
   Kapazität Teil der Identität: über den bestehenden Anmeldeablauf eine passende
   Identität erstellen, umstellen und die alte widerrufen. `capacity` nicht nur in
   einer privaten Agent-Datei ändern. Mehrere Knoten desselben Alias müssen
   dieselbe deklarierte Modellrevision und dieselben Fähigkeiten haben.
4. Gateway mit der geprüften neuen Konfiguration starten. Keine Datenbankmigration
   wird durch dieses Profil eingeführt.

## Engine, Kontext und Bilder

Der verwaltete Start verwendet standardmäßig **32768 Kontexttoken pro Slot**.
Bei vier Slots wird `--ctx-size 131072 --parallel 4` angefordert. Der wirksame
Kontext muss an der festgelegten llama.cpp-Version geprüft werden; die Startflags
allein belegen weder Modellunterstützung noch genügend RAM/VRAM. Ein 70000-Zeichen-
Prompt ist kein Tokenmaß: Systemprompt, Bildtoken und Ausgabebudget benötigen
zusätzlich Platz. Das Modell muss passende Kontextlänge und Vision unterstützen.

In der Agent-Einrichtung unter „Erweiterte Einstellungen“ lassen sich Kontext je
Anfrage sowie optional ein lokaler Vision-Projektor mit SHA-256 einstellen.
Dieselben Werte stehen in der privaten `settings.json` unter `runtime`:

```json
{
  "runtime": {
    "context_per_request": 32768,
    "projector": "/absoluter/pfad/mmproj.gguf",
    "projector_sha256": "64_kleine_hexadezimale_Zeichen"
  }
}
```

Vor dem Start wird der Projektor gegen die Prüfsumme geprüft. Er muss genau zum
Modell passen. Modell und Projektor bleiben operatorseitig ausgewählte Artefakte;
es gibt keinen automatischen Projektor-Download. Ein Modell mit integrierter
Vision benötigt eventuell keinen separaten Projektor. Die UI-Katalogauswahl
bleibt auf die vorhandenen freigegebenen Modelle begrenzt; andere Kombinationen
können über einen separat betriebenen, statisch angebundenen Backendserver getestet werden.

Beim CLI-Agenten gelten `--mmproj`, `--mmproj-sha256` und `--context-size`.
`--context-size` bleibt aus Kompatibilitätsgründen der **Gesamtkontext**;
weglassen berechnet 32768 × Kapazität. Bestehende explizite kleine Werte ändern
sich nicht automatisch.

Der verwaltete Start setzt `--jinja --reasoning-format deepseek`,
`--cache-ram 0` und `--no-context-shift`. Die ausgewählte llama.cpp-Version muss
alle Flags unterstützen. Keine automatische Ausweichkonfiguration: Ein Startfehler
wird als Fehler gemeldet. Thinking-Kwargs und Schema-Decoding müssen mit genau dem
Modell/Chat-Template getestet werden. Die Modellrevision soll auch Projektor,
Engine und Template eindeutig festhalten.

## Zeitlimits

| Grenze | Profil |
| --- | --- |
| Verbindungsaufbau/TLS | kurze bestehende Grenzen, typischerweise 3–10 s |
| Healthcheck | 3 s |
| Upload zum Gateway/Agenten | 60 s |
| Inferenz im Gateway | 300 s nach Requestvalidierung und Kapazitätsvergabe |
| Interne Inferenztransporte | Request-Kontext statt früherer 30-s-Antwortkopfgrenze |
| Browser-Managementproxy | 330 s nach gepuffertem, größenbegrenztem Upload |
| Externer Reverse Proxy | mindestens 330 s für Backend-Antworten; Upload separat begrenzen |
| Prozesslog-Client | ungefähr 360 s; tatsächliche Client-Semantik prüfen |

Bei langsamen großen Uploads kann ein Client-Gesamtbudget von 360 s knapp werden:
60 s Upload plus 300 s Inferenz lassen keine Reserve. Dann etwa 420 s verwenden.
Ein beliebig höher konfiguriertes Gateway-Limit erfordert entsprechend angepasste
Proxy-/Clientgrenzen; das Browser-Testprofil ist auf 300 s Gateway ausgelegt.
Gesamtbudget und Wiederholungen mehrstufiger Navigator-Aufrufe bleiben Aufgabe
von Prozesslog. Sechs sequenzielle Aufrufe mit jeweils 300 s könnten 30 Minuten
dauern; dieses Gateway führt keine automatischen Inferenzwiederholungen aus.

## API-Vertrag und Fehler

- Prozesslog-Basis-URL: `https://gateway.example/v1`. Das Gateway bedient
  `POST /v1/chat/completions`; keine automatische Weiterleitung ergänzen.
- Bearer-Key und genau eine pseudonyme `X-User-ID`: 1–128 ASCII-Zeichen, beginnend
  mit Buchstabe/Ziffer, danach zusätzlich Punkt, Unterstrich, Doppelpunkt, Bindestrich.
  Eine optionale Body-`user` muss dazu passen. Identität wird nicht an die Engine gegeben.
- `content` kann Text oder eine Liste aus `text` und `image_url` sein. Bilder sind
  ausschließlich eingebettete Base64-Data-URLs für PNG, JPEG, WebP oder GIF in
  User-Nachrichten, maximal 8 MiB je dekodiertem Bild; der gesamte Request maximal
  16 MiB. MIME-Typ und Dateisignatur werden geprüft. Pixel-/Animationsunterstützung
  muss die Engine leisten. Keine entfernten Bild-URLs und keine Bildpersistenz.
- `chat_template_kwargs.enable_thinking` wird je Aufruf weitergereicht. Der
  Gateway-Client verwendet `cache_prompt: false`, auch wenn der Aufrufer `true`
  verlangt. Leere Textantworten bleiben für Prozesslogs einmaligen Wiederholungsaufruf
  ohne Thinking erkennbar. Thinking muss im separaten Feld stehen; bekannte
  `<think>`-Tags im nicht gestreamten `content` führen zu 502 statt einer Auslieferung.
  Eine universelle Erkennung frei formulierter Denktexte ist damit nicht möglich.
- `response_format`: `text`, `json_object`, `json_schema`. Das Format wird an die
  Engine gegeben; zusätzlich validiert das Gateway die vollständige Antwort, bei
  Schema auch `anyOf`. Externe Schema-Referenzen werden nicht geladen. Schema maximal
  64 KiB. Ungültige Schemas ergeben 400, nicht passende Engine-Ausgaben 502.
  Strukturierte Ausgabe erfordert `stream=false` (sonst 400). Bestehendes Text-SSE
  bleibt unterstützt; Reasoning-Trennung im Stream hängt vom geprüften Engineformat ab.
- Überlast: 429/503 mit `error.code=model_busy` und `Retry-After` von 1–30 s.
  Der Header wird auch durch ausgehende Agenten transportiert. Backend-400/413/415/422
  bleiben 4xx. Backend-Authentifizierungsfehler, Weiterleitungen und sonstige
  technische Backendfehler werden als inhaltsfreier 502 gemeldet. Zeitüberschreitung
  ergibt 504. Ein Audit-Ausfall ergibt weiterhin 503 mit `audit_unavailable` und
  erfordert Betreiberbehebung. Deshalb bedeutet nicht jedes 5xx Überlast.

## Datenschutz

Gateway-Audit speichert weiterhin ausschließlich definierte Metadaten, z. B.
Benutzerpseudonym, Modellrevision, Status, Dauer und Tokenzahlen. Prompts, Bilder,
Antworten und Denktext werden nicht ins Audit aufgenommen. Nutzdaten werden
während der Verarbeitung im Arbeitsspeicher gehalten. Keine Inhaltsdateien oder
Inferenz-Retries im Gateway. HTTP-Antworten tragen `Cache-Control: no-store`.

`cache_prompt: false` und deaktivierter Engine-RAM-Cache ersetzen keine Prüfung
externer Komponenten. Betreiber müssen Inhaltslogs, Debug-Tracing, persistente
KV-/Slot-Caches, Proxy-Caching, Core-Dumps und Swap entsprechend der vereinbarten
Datenverarbeitung konfigurieren. Externe Engines dürfen ihre Konsolenausgaben
nicht mit Kundeninhalten speichern. AVV und Lösch-/Speicherzusagen sind separat
mit dem Betreiber zu klären; ein synthetischer Test belegt keinen AVV.

## Testfolge und Freigabe

1. Lokal: `go test -race -count=1 ./...`, `go vet ./...`, Build sowie bestehender
   Dashboard-Streamtest. Die Tests prüfen u. a. einen 8-MiB-Bildrequest, 70000 Zeichen,
   JSON-Schemaverletzungen, vier gleichzeitige Anfragen, Überlast und eine echte
   Antwortkopfverzögerung von 31 s. `go test -short` überspringt die beiden langen 31-s-Regressionstests.
2. Am echten Gateway, mit synthetischen Daten:

   ```sh
   python3 scripts/prozesslog_smoke.py --base https://gateway.example/v1 --model MODELLALIAS --parallel 4 --report /privater/pfad/gateway-test.json
   ```

   `OPENAI_API_KEY` sicher als Umgebungsvariable bereitstellen, eigene CA über
   `OPENAI_CA_BUNDLE` oder `--ca`. TLS-Prüfung bleibt aktiv, Redirects sind verboten.
   Das Skript prüft Chat, Thinking an/aus, JSON-Objekt, Schema mit `anyOf`, ein rotes
   PNG, langen Kontext und vier zeitgleich gestartete Aufrufe. Es protokolliert nur
   Status, Zeiten und Tokenzahlen. Es beweist keine 32k Token je Slot und keine
   300-s-Laufzeit: Dafür mit der echten Engine gezielt lange, tokenisierte Prompts
   und vier große Kontexte gleichzeitig prüfen. Sequentielle Engine-Abarbeitung
   muss anhand Laufzeiten/Engine-Metriken ausgeschlossen werden.
3. In Prozesslog: `tools/gateway_pruefen.py --denken --bild --parallel 4`.
   Rot oder Gelb vor weiteren Tests beheben. Bei fehlender Schema-Kompatibilität
   `OPENAI_JSON_MODE=json_object`; ohne JSON-Unterstützung `aus`.
4. Bilddokument importieren, kontrollierte Bildablehnung prüfen, bekannte Chatfrage
   und „Gründlich nachsehen“ ausführen. Fehlgeschlagene Bildbeschreibung darf den
   Import nicht abbrechen. `tools/last_pruefen.py` ausführen.
5. Latenzen je Aufgabentyp (Median/p95), Fehlerquote, Tokenverbrauch, RAM/VRAM und
   Navigator-Gesamtdauer erfassen. Normaler Chat sollte nicht regelmäßig Minuten
   benötigen. Keine Produktivfreigabe allein aufgrund bestandener Loopback-Tests.

Prozesslog-Einstellungen: `LLM_PROVIDER=openai`, `PROZESSLOG_CLOUD_ERLAUBT=1`,
`OPENAI_DENKEN=aufgabe`, stabiler `OPENAI_MODEL`, optional
`OPENAI_JSON_MODE=json_schema`. Damit werden auch eingestufte Inhalte zum Gateway
gegeben. Navigator/Bilder lassen sich mit `RAG_NAVIGATOR_GATEWAY=0` und
`OPENAI_BILDER=0` lokal halten; `RAG_NAVIGATOR_THINK=1` aktiviert Navigator-Thinking.
Ollama ab 0.40.1, das vorgesehene Embedding-Modell, Chroma und auf Linux/Windows
Tesseract sind Prozesslog-Voraussetzungen und werden hier nicht installiert.

## Git und Rückweg

Ein Integrationsbranch, Review-PR gegen `main`, kein Force-Push und kein Merge vor
realer Abnahme. Testprotokolle nennen exakte Commits beider Repositories. Erst nach
Abnahme einen freigegebenen Build markieren. Bei Fehlern den Test stoppen und
vorheriges Binary plus vorherige Gateway-/Agent-Einstellungen wiederherstellen.
Bei geändertem Anmeldestand aktuelle Widerrufe beachten; keine alte Identität
ungeprüft reaktivieren. Kein Datenbank-Downgrade vorgesehen. Neue Prüfberichte nur
mit synthetischen Daten und nach Prüfung auf vertrauliche Metadaten einchecken.
