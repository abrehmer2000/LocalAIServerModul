# Implementierungsstand und nächste Schritte

## Architekturentscheidungen

- Go für Gateway und später Agent, SQLite (modernc, ohne CGO) für lokale Speicherung.
- HTTP-API der Engine als Integrationsgrenze; keine Modellausführung im Gateway.
- Explizite Modellaliase und deklarierte Revisionen; keine automatische Modellersetzung.
- Begrenzte Kapazität je Replikat, sofortige Überlastantwort statt unbeschränkter Warteschlange.
- Zeitlich begrenzte Sperre nach Backendfehlern; keine Retries, die doppelte Berechnungen verursachen könnten.
- Kein Herstellerkontakt, keine Lizenzprüfung oder Usecase-Klassifikation.
- Audit-Metadaten erlauben keine Rekonstruktion der Gesprächsinhalte.

## Etappe 1: Gateway und erstes Modell

Implementiert: Authentifizierung, verpflichtende Benutzer-ID, Chat-Completions, SSE, Modellliste,
SQLite-Audit mit Integritätsprüfung, Ausgabe- und Zeitlimits, CLI und Startkonfiguration.
Abschlusskriterium: echtes Modell antwortet über Gateway, Audit enthält Annahme und Ergebnis.
Siehe `VALIDATION.md` für tatsächlich ausgeführte Nachweise.

## Etappe 2: Zweiter Modellserver

Implementiert: mehrere konfigurierte Backends, Auswahl nach relativer Belegung, Kapazitätsgrenzen,
HTTPS-Verifikation und optionaler separater Engine-API-Key.
Vorbereitet: Integrationstest mit zwei unabhängigen Engine-Testservern und echter Smoke-Test mit zwei Engine-Prozessen.
Zusätzlich implementiert und getestet: Agent, einmalige zehnminütige Join-Token, persistente
Geräteidentitäten, mTLS in beide Richtungen, dynamische Registrierung, Bereitschaftsprüfung,
Heartbeat-Freigabe, Widerruf und automatische Gerätezertifikatserneuerung.
Optionaler Start und Überwachung von llama-server mit SHA-256-Prüfung der Modellgewichte.
Registrierung und Verwaltungsänderungen sind mit ihrem Audit-Eintrag atomar gekoppelt.

Noch offen: Installation als Systemdienst, Kundenzertifikate getrennt von interner Geräte-PKI,
Gateway-/CA-Erneuerung, ein ausgehender Tunnel für nicht direkt erreichbare Agenten,
Hardwareoptionen und Tests auf getrennten physischen Rechnern.

## Etappe 3: Modellverwaltung

Downloadkontrolle, kleine Modellauswahl, GGUF/Safetensors-Prüfung, Integrität, Speicherbedarf,
atomare Aktivierung, Downloadfortsetzung und kontrollierter Engine-Neustart.
Danach vLLM auf Linux ergänzen.

## Vor Kundenpilot

- Lokale Administratoranmeldung und personengebundene Verwaltungsereignisse.
- Schlüsselverwaltung, Widerruf, Rate Limits, Audit-Rollen und Aufbewahrung.
- Installations- und Deinstallationstests mit realem Windows und Linux.
- Dienste, Backup/Restore, Updates und Wiederanlauf nach Strom-/Speicherausfällen.
- Lasttests mit vereinbarten Modellen, Kontextlängen und Zielhardware bis 40 gleichzeitige Anfragen.
- Engine-Versionen festlegen, SBOM, signierte Releases und Sicherheitsprüfung.
- Datenbankmigrationen und geschützte externe Audit-Prüfanker.
