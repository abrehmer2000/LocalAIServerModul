> Neuer geführter Ablauf: [SETUP-ASSISTENT.md](SETUP-ASSISTENT.md). Die folgenden Kommandozeilen-Schritte bleiben als administrative Alternative und für bestehende Pilotinstallationen erhalten.

# Dashboard und weitere Server (Pilot)

Diese Version erweitert die bestehende Installation. Das Mieten und Bereitstellen
von Rechnern erfolgt beim Anbieter oder in der eigenen Infrastruktur.

## Was ohne Terminal bedienbar ist

- Status und Kapazität der Modellserver ansehen.
- Einen Serverzugang und eine zehn Minuten gültige, einmal verwendbare
  `connection.json` erzeugen.
- Diese Datei im lokalen Agentenfenster importieren.
- Das Standardmodell auf dem Zielrechner herunterladen, Fortschritt ansehen,
  den eigenen Modellprozess starten und stoppen.
- Einen Serverzugang sperren, Audit-Metadaten ansehen und die Streaming-API testen.
- Den Agenten lokal für den automatischen Start nach Benutzeranmeldung eintragen.

Der Modellkatalog enthält zunächst Gemma 4 26B A4B QAT Q4_0. Andere API-Modelle
können für bestehende lokale Engines angemeldet werden; deren automatischer Download
ist noch nicht Bestandteil dieses kleinen Katalogs.

## Zentrale starten

Neue lokale Installation:

```sh
./ki-basis quickstart --dir ./Daten --open-browser
```

Das Dashboard öffnet sich auf `http://127.0.0.1:3000`. Der erstmals erzeugte lokale
Zugang hat Administratorrechte. Der Key bleibt im Browser nur im Speicher des Tabs;
keine Cookies, kein Local Storage. `quickstart` erhält bei weiteren Starts die
vorhandene Konfiguration und reaktiviert keine alten Schlüssel.

Für eine bestehende Installation werden Anwendungs-Keys **nicht automatisch** zu
Administratoren. Ein lokaler Betreiber erzeugt einen separaten Zugang:

```sh
./ki-basis admin-key --config ./Daten/config.json --out ./Daten/admin-access.json
```

Danach Gateway neu starten. Der Schlüssel wird ausschließlich in die neue Datei
mit privaten Dateirechten geschrieben, nicht ins Terminal. Der dortige `key` ist
für die Anmeldung im Dashboard bestimmt, die Benutzer-ID zum Beispiel `admin-01`.
Windows: `ki-basis.exe` statt `./ki-basis` verwenden; das Datenverzeichnis über seine
Windows-Zugriffsrechte schützen.

Wenn das Gateway separat mit `serve` läuft, ist das Dashboard auch direkt über
seine HTTPS-Adresse erreichbar. Für einen zusätzlichen lokalen Browserzugang:

```sh
./ki-basis web-preview --listen 127.0.0.1:3000 \
  --backend https://127.0.0.1:8443 --ca ./Daten/pki/ca.crt --management
```

Der bisherige `web-preview`-Modus bleibt ohne `--management` schreibgeschützt.
Mit Management ist ausschließlich ein Loopback-Listener erlaubt. Keine internen
Geräte-Endpunkte werden über diesen Browser-Proxy weitergeleitet.

## Zusätzlichen Rechner verbinden

1. Im Dashboard mit einem Administratorzugang und eigener Benutzer-ID anmelden.
2. „Server hinzufügen“ öffnen und einen eindeutigen Namen vergeben.
3. Die vom neuen Rechner erreichbare **HTTPS-Adresse der Zentrale** eintragen.
4. Für Replikate eines vorhandenen API-Modells exakt dessen Modellalias und Revision
   eintragen. Für eine neue Standardgruppe die Vorgaben belassen.
5. Verbindungsdatei herunterladen und auf den gewünschten Rechner übertragen.
6. Passendes Linux-/Windows-Paket entpacken. `Start-Agent.cmd` (Windows) oder
   `Start-Agent.sh` (Linux) starten. Unter Linux hängt das Öffnen ausführbarer
   Starter von den Desktop-Sicherheitseinstellungen ab.
7. Im lokalen Browserfenster `connection.json` importieren. Unter „Lokale
   Einstellungen“ gegebenenfalls den vollständigen Pfad zu `llama-server` und das
   Modellverzeichnis einstellen. Die Laufzeit wird im Paketverzeichnis, im PATH
   und im bekannten RunPod-Buildpfad gesucht.
   Eine bereits heruntergeladene GGUF-Datei kann unter „Bereits vorhandene GGUF-Datei“
   eingetragen werden. Der Agent prüft die Katalog-Prüfsumme und verwendet sie direkt,
   ohne eine zweite Kopie anzulegen.
8. Im zentralen Dashboard den Server aktualisieren, Ausführung wählen und
   „Herunterladen und starten“ anklicken.

Der Agent benötigt ausschließlich ausgehendes HTTPS zur Zentrale. Es gibt keinen
von außen zu öffnenden Agenten-Port. Anfragen werden über authentifizierte
Long-Poll-Verbindungen zugestellt, Antworten sofort gestreamt. Der separate
Abbruchkanal beendet die lokale Inferenz bei Client-Abbruch. Kein Wiederholen
bereits zugestellter Anfragen, keine Speicherung der Inferenzinhalte im Tunnel.

**Netzwerkvoraussetzung:** Eine Zentrale mit `127.0.0.1` als Listener oder
Zertifikatsadresse ist nur für denselben Rechner geeignet. Für weitere Maschinen
muss der Betreiber einmalig eine erreichbare Zentrale mit passendem Zertifikat
und HTTPS-Listener einrichten; alternativ VPN. Ausgehende Verbindungen ersetzen
keine erforderliche Verbindung zur Zentrale. TLS darf für diesen Zweck nicht
abgeschaltet werden. Ein öffentlicher HTTP-Proxy reicht für Geräte-mTLS nicht aus:
TLS muss bis zur Zentrale durchgereicht werden.

## Modellbetrieb

- Die Zentrale liest die offiziellen Metadaten des Katalogeintrags, fixiert Commit,
  Dateiname, Größe und SHA-256 dauerhaft in ihrer Datenbank und verteilt Aufträge.
- Der jeweilige Agent lädt selbst von Hugging Face. HTTPS-Weiterleitungen werden
  auf die Modellquelle und ihre Hugging-Face-Downloadhosts begrenzt.
- Teil-Downloads sind fortsetzbar. Freier Plattenplatz wird vorher geprüft.
  Erst nach Größen- und SHA-256-Prüfung wird die Datei atomar aktiviert.
- Ein Modellauftrag bleibt gespeichert, wenn der Agent vorübergehend offline ist.
  Nach einem Neustart wird der gewünschte Zustand erneut hergestellt.
- „Herunterladen und starten“ verwendet eine vorhandene passende Datei wieder.
  Standard: 2048 Kontext-Tokens und Kapazität 1; CPU ist explizit auswählbar.
- GPU-Treiber und die passende CPU-/CUDA-/Vulkan-Laufzeit müssen bereits installiert
  sein. Diese Version installiert keine fremden ausführbaren Dateien oder Treiber.
- „Stoppen“ beendet nur einen vom Agenten gestarteten Prozess und bricht einen
  laufenden Download ab. Andere lokale Modellserver werden nicht beendet.
- Download und Betrieb erfolgen ohne Prüfung der Softwarelizenz oder Ablaufdatum.
  Eine vom Modellanbieter geforderte Zugangsfreigabe wird nicht umgangen. Ein
  integrierter Hugging-Face-Login für geschützte Dateien ist noch nicht enthalten.

## Agent und automatischer Start

Das lokale Agentenfenster ist nur auf `127.0.0.1` erreichbar. Seine Aktionen benötigen
zusätzlich einen zufälligen Sitzungsschlüssel aus dem Startlink. Dieser wird aus
der Browseradresse entfernt. Keine Verwaltung über eine ungeschützte lokale API.

Über „Bei Anmeldung starten“ wird unter Windows ein Eintrag unter dem aktuellen
Benutzer angelegt. Unter Linux wird eine systemd-Benutzereinheit eingerichtet.
Die Anwendung benötigt keine Administratorrechte dafür. Unter Linux ohne
systemd-Benutzersitzung, beispielsweise in vielen Cloud-Containern, wird die Aktion
abgelehnt. Ein durchgehend laufender Serverdienst nach dem Abmelden benötigt eine
separate systemweite Dienstinstallation beziehungsweise den Container-Startprozess.

Für Rechner ohne Desktop bleibt ein einmaliger Einstieg verfügbar:

```sh
./ki-basis agent-ui --dir /dauerhafter/pfad/agent --open-browser=false
```

Der ausgegebene lokale Browserlink kann über einen geschützten Zugang geöffnet
werden. Alternativ ist die bestehende CLI-Anmeldung mit `join-token --connection
outbound` und anschließend `agent run` verwendbar. Ein fertiger Cloud-init-Installer,
Windows-MSI und eine automatische Installation beliebiger GPU-Laufzeiten sind
noch nicht enthalten. Nach der Anmeldung läuft die normale Modellverwaltung im
Dashboard. Für Docker-/RunPod-Container Daten außerhalb des flüchtigen
Container-Dateisystems speichern oder vor dem Stoppen sichern.

## Audit und Updates

Zentrale Verwaltungsaktionen verwenden Administratorrechte und Benutzer-ID.
Anmeldung, Sperrung, Modellaufträge sowie Modellzustandswechsel werden als
Metadaten protokolliert; Downloadfortschritt wird nicht pro Datenblock auditiert.
Die Dashboard-Ansicht zeigt die letzten 100 Einträge. Eine vollständige Prüfung
bleibt mit `audit-verify` und vollständiger Export mit `audit-export` möglich.
Lokale Änderungen an Agenteneinstellungen und Autostart werden vor ihrer Ausführung
unter der authentifizierten Geräteidentität als Absicht protokolliert. Ist die
Zentrale nicht erreichbar, werden diese lokalen Verwaltungsaktionen abgelehnt;
die Installation kann anschließend wiederholt werden. Ein Offline-Audit-Outbox ist
noch nicht enthalten.

Die Datenbank wird kompatibel um Management-Tabellen auf Schema 3 erweitert.
Vor einem Update Datenbank und PKI sichern, laufende Anfragen abschließen und die
Zentrale inklusive zugehöriger CLI aktualisieren. Alte Zentrale-Binaries mit
Schema-Unterstützung bis 2 können die aktualisierte Datenbank nicht mehr öffnen.
Bestehende direkte Agenten bleiben unterstützt. Neue Dashboard-Anmeldungen nutzen
den ausgehenden Modus. Direkte Agenten werden nicht stillschweigend migriert.
