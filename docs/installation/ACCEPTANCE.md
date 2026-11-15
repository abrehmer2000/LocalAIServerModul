# Installationsabnahme und Nachweise

Stand: 15. November 2026. Entwicklungsgrundlage: `948e674`.
Die Zahlen bewerten nachgewiesene Eigenschaften; ein erfolgreicher Build allein
ist keine Produktfreigabe.

## Unabhängige Prüfung

- [Runde 1](REVIEW-ROUND-1.md): 8,0/10 Implementierungsreife; konkrete Befunde zu
  Bereitschaft, Sitzungsisolation, Wiederaufnahme und Kontrasten.
- Runde 2 prüfte diese Änderungen unabhängig erneut. Alle Befunde der ersten
  Runde sind behoben; gezielte Race- und Browser-/WCAG-Prüfungen bestanden.
  Zwischenstand 8,8/10, bis die echten Windows-Fehler behoben und geprüft sind.
- Die abschließende Bewertung und Windows-Nachweise werden nach dem letzten
  Prüflauf ergänzt. Dieser Zwischenstand behauptet keine 9/10-Produktabnahme.

## Bereits ausgeführte Prüfungen

| Nachweis | Ergebnis und Aussagegrenze |
| --- | --- |
| Vollständige Linux-Go-Tests mit Race Detector | Erfolgreich: `go test -race -count=1 -timeout=180s ./...`; schließt Gateway, Audit, Agenten, Steuerung und neuen Einstieg ein. |
| Statische Go-Prüfung | `go vet ./...` erfolgreich. |
| Lokaler TLS-/Agent-/Modellprozessweg | `TestDesktopInferenceAcrossProcessLifecycle` startet einen deterministischen Engine-Testprozess, prüft echtes Enrollment, Heartbeats, Modellbereitschaft, Tokenisierung, Gatewayantwort und Wiederanlauf mit derselben Identität. Keine Aussage zur Qualität von Gemma. |
| Browser und Zugänglichkeit | Echter Chromium mit Playwright und axe: erster Start, Einwilligung, Fortschritt, Abbruch, erneuter Versuch, sichere Textdarstellung, Fehler, Geheimnisausblendung und schmale Ansicht. WCAG-A/AA-Prüfungen ohne gemeldete Verstöße. Das ersetzt keine vollständige menschliche Barrierefreiheitsabnahme. |
| Tatsächlich gestartete Linux-Anwendung | Fest gepinnte Laufzeit aus dem Netz geladen, SHA-256 geprüft, Laufzeit gestartet, lokaler Agent automatisch angemeldet; modellgebundener Anwendungsschlüssel durfte `/v1/models` lesen und wurde an `/admin/overview` mit 403 abgewiesen. Keine Modellgewichte geladen. |
| Echte Laufzeitarchive | Windows- und Linux-Asset separat heruntergeladen und Digest verglichen; Linux `llama-server --version` erfolgreich. Einzelheiten in [RUNTIME-CATALOG.md](RUNTIME-CATALOG.md). |
| Widerruf/Wiederaufnahme | Tests prüfen autoritative persistente Schlüssel, gesperrte Wiederherstellung widerrufener Schlüssel, unterbrochene Ersteinrichtung und zwei parallele Installationssitzungen. |
| Plattformpakete | Linux-/Windows-Pilotpakete und macOS-Paketlauf erfolgreich; dies ist kein nativer Windows-Installationsnachweis. |

## Native Windows-Prüfung

Die Workflow-Datei `windows-installer.yml` baut das Setup auf Windows Server 2025
und führt den tatsächlichen Installer und Uninstaller aus. Der Prüfumfang umfasst:

- Desktopinstallation ohne Erzeugen eines Systemdiensts; erneutes Öffnen derselben
  Instanz und Erhalt der Konfiguration nach Aktualisierung.
- Serverinstallation mit virtuellem Dienstkonto, automatischem Starttyp,
  geschützten Programm-/Datenpfaden und restriktiven Dateirechten.
- Dienst anhalten und neu starten, Aktualisierung, Deinstallation beider Modi und
  Erhalt von CA und Konfiguration.
- Ablehnung eines unsicheren Serverinstallationspfads vor Erzeugung des Diensts.

Die ersten nativen Läufe zeigten echte Fehler bei Inno-Setup-Einstellungen und
Dateifreigabe nach dem Anwendungsneustart unter Windows. Sie wurden nicht als Erfolg gewertet.
Die Behebung und der finale erfolgreiche Lauf werden hier nachgewiesen.

## Noch getrennt am Zielsystem abzunehmen

1. Startmenü und interaktiver UAC-Ablauf auf dem vorgesehenen Windows-Server.
2. Echtes empfohlenes Gemma-Modell: Download, RAM-/Plattenbedarf, erste vollständige
   Antwort und Laufzeit auf der Zielhardware; kein Testdouble als Ersatz.
3. Server abmelden und neu starten; ohne erneute Anmeldung die konfigurierte
   Erreichbarkeit und tatsächliche Inferenz prüfen.
4. Einbindung von Prozesslog mit dem gesonderten Anwendungszugang und CA-Zertifikat.
5. Wiederherstellung einer geprüften Sicherung bei einem fehlgeschlagenen Update.
6. Herausgebersignierung mit echtem Zertifikat und anschließende Prüfung des
   Windows-Vertrauensdialogs. Derzeit erzeugte Pakete sind unsignierte Testbuilds.

Weder Codebewertung noch diese Tests ersetzen die genannten externen Nachweise.
Es wurde nichts auf dem vorhandenen Lightsail-Server installiert oder neu gestartet.
