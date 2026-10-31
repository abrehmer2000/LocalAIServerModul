# Kleiner, belastbarer Produktivumfang

## Produktgrenze

Eine getrennte Installation pro Kunde: eine Zentrale, mehrere Modell-Agenten,
ein OpenAI-kompatibler Texteinstieg, API-Schlüssel und pseudonyme Benutzer-ID,
Metadaten-Audit, ein gepflegter Modellkatalog sowie Sicherung/Wiederherstellung.
Keine Cloud-Verträge, VM-Erstellung, Kubernetes-Steuerung, Benutzerkontenplattform,
Embeddings, Tool-Calling oder beliebige automatische Treiberinstallation im ersten
Produktivumfang. Keine neuen Funktionen bis zur Abnahme dieses Kerns.

Die Einrichtung bleibt anbieterneutral. Unterstützt wird eine konkret getestete
Kombination aus Betriebssystem, Architektur und Laufzeit; ein Anbietername ist
kein Qualitätsnachweis. Die erste Freigabematrix soll klein bleiben: Linux amd64
auf einer festgelegten LTS-Distribution und Windows amd64 auf festgelegten
Windows-/Serverversionen; CPU und eine konkret geprüfte NVIDIA-Konfiguration.
macOS Apple Silicon dient zunächst als Manager. Weitere Kombinationen bleiben
Pilotstatus, bis sie dieselben Tests bestanden haben.

SSH-Installation und Desktop-Manager sind Einrichtungshilfen. Ihr Ausfall darf
keine bereits korrekt eingerichtete Zentrale oder Agent-Installation beschädigen.
Ein laufender Agent darf nicht von einer dauerhaften Desktop-Sitzung des
Installateurs abhängen. Erreichbares HTTPS/VPN und dauerhafter Speicher sind
Betriebsvoraussetzungen. Für Container ohne Dienstverwaltung muss ein expliziter
Startmechanismus der Infrastruktur festgelegt werden.

## Abnahmekriterien statt eines pauschalen Reifeversprechens

Vor einer Produktivfreigabe müssen folgende Nachweise für jede freigegebene
Kombination vorliegen:

- Frische Installation anhand des Assistenten durch einen zweiten Menschen ohne
  mündliche Hilfestellung; verständliche Diagnose bei fehlender Laufzeit,
  unpassender Architektur, belegtem Port, falschem Zertifikat und zu wenig Speicher.
- Neustart von Zentrale, Agent, Server und Manager. Kein zweiter Modellprozess
  gegen dieselbe Identität. Automatischer Start ohne aktive Installateursitzung.
- Mindestens 24 Stunden Testbetrieb mit wiederholten und parallelen Anfragen;
  anschließend keine weiterlaufenden Kindprozesse oder wachsenden Warteschlangen.
- Verbindungsabbruch, Agent-Ausfall, Client-Abbruch, Modellabsturz und Zeitlimit:
  Kapazität wird freigegeben, keine Antworten an falsche Nutzer, kein unbemerkter
  Wiederholungsversand bereits zugestellter Anfragen.
- Leere oder abgeschnittene Antworten werden nicht als vollständiger Erfolg
  dargestellt. Der abschließende Einrichtungstest verwendet genau den neuen Server.
- Sicherung einer laufenden Installation, Wiederherstellung auf einem frischen
  Rechner, gültige Audit-Kette, Anmeldung mit aktuellen Schlüsseln und echte
  Modellantwort. Ein wiederhergestellter Widerruf bleibt wirksam.
- Update und Rückweg mit gesichertem Datenstand; kein ungetesteter Datenbank-
  Downgrade. Ein fehlgeschlagenes Update darf die letzte funktionierende Version
  nicht kommentarlos ersetzen. Die Schema-Version muss dokumentiert sein.
- Sicherheitsprüfung der Schlüsselrollen, lokalen Setup-Sitzungen, Hostnamen-
  prüfung, SSH-Host-Schlüssel, Dateiimporte und Backup-Zugriffe. Keine Inhalte,
  Passphrasen oder privaten Schlüssel im Audit/normalen Service-Log.
- Geprüfte Laufzeit-Artefakte mit festgelegter Version/Prüfsumme und dokumentierter
  Quelle. Ein erfolgreicher `--version`-Aufruf allein ist keine Modell-/GPU-Abnahme.
- Kurze Betriebsanleitung für Start/Stopp, Wiederherstellung, Schlüsselwechsel,
  Zertifikatswechsel, Speicherknappheit und ein definiertes Support-Zeitfenster.

## Arbeitsfolge für die nächsten Wochen

1. **Stabilisieren:** Umfang einfrieren, Supportmatrix festlegen, die heute
   gesicherten Pilotinstallationen wiederherstellen und neue Assistenten prüfen.
   Gefundene Fehler beheben; keine zusätzlichen Plattformversprechen.
2. **Betrieb absichern:** Neustart, Systemdienste/Containerstart, Sicherung,
   Restore, Update/Rückweg und Fehlerfälle wiederholt testen.
3. **Begrenzter KMU-Pilot:** Eine konkrete Anwendung und wenige Nutzer,
   dokumentierte Abnahme und betreuter Betrieb. Ein Pilot ist noch keine
   allgemeine Produktionsfreigabe.
4. **Freigabe:** Nur die tatsächlich bestandenen Kombinationen als unterstützt
   anbieten. Offene Abnahmekriterien verhindern die Freigabe, unabhängig vom Datum.

## Bereits verfügbare technische Prüfungen

- Go-Tests mit Race Detector und `go vet`.
- `node scripts/dashboard_stream_test.cjs`: Stream-Zeilenenden, UTF-8 über
  Paketgrenzen, leere Ausgabe, Tokenlimit und fehlender Abschluss.
- `scripts/dashboard_smoke.py`: Browser-Schritte, echte TLS-Anmeldung,
  Laufzeitprüfung, Setup-Sitzung nach Reload, Backup-Downloads,
  Agent-Wiederherstellung und Audit-Prüfung einer restaurierten Zentrale.
- `scripts/ssh_setup_smoke.py`: echte isolierte OpenSSH-Verbindung,
  Host-Schlüsselprüfung, Programm-/Laufzeitübertragung, Agent-Anmeldung,
  Hintergrund-Heartbeat, verwalteter Tunnel und Wiederverwendung der Oberfläche.
- Native Betriebssystem-Prüfungen/Builds über GitHub Actions. Ein erfolgreicher
  Cross-Build ersetzt keine Bedienungs-, Treiber- oder Langzeittests auf Zielhardware.

Die technischen Tests schaffen eine Basis; die obigen betrieblichen und
menschlichen Abnahmen sind weiterhin erforderlich.

## Ergänzte Stabilisierung im Einrichtungsablauf

Strukturierte SSH-Diagnosen, identitätsgebundene Wiederaufnahme mit Prozesssperre,
einheitlicher Backup-Vertrag mit Prüfsummen, begrenzte Alt-Pilotmigration und
grafischer Import von Zentralendaten sind umgesetzt. Der angeforderte systemd-
Betrieb wird explizit geprüft; ein Fehlschlag fällt nicht still auf Hintergrund-
betrieb zurück. Wiederaufnahme eines beendeten Agent-Prozesses ist automatisiert
geprüft. Das ersetzt keinen vollständigen Maschinenneustart oder GPU-Test.

Die Netzwerkvarianten bleiben erreichbares HTTPS (ggf. vorhandenes VPN) und ein
bereits eingerichteter Agent-Tunnel. Anbieterintegration, Treiberinstallation und
allgemeine Laufzeitverwaltung bleiben außerhalb dieses Freigabeumfangs.
