# Benutzerfreundliche Installation: Plan und Abnahme

Stand: 14. November 2026. Ausgangspunkt: `feat/requirements-v2` (`948e674`),
weil `main` (`84cc728`) die neueren Gateway- und Windows-Dienstkorrekturen nicht enthält.
Dieser Branch integriert keine fremden Änderungen durch einen stillen Merge in main.

## Ziel

Ein Windows-Setup installieren, KI-Basis über das Startmenü öffnen, eine geprüfte
Laufzeit im Programm installieren, Modell herunterladen und eine echte Antwort
sehen. Im normalen lokalen Ablauf keine Konsole, JSON-Datei, Zertifikatskopie
oder Suche nach einem externen Laufzeitarchiv. Vorhandene Expertenwege bleiben
verfügbar. Prozesslog selbst wird durch dieses Projekt nicht installiert.

## Was wir von professionellen Tools übernehmen

Siehe [Recherche mit Primärquellen](RESEARCH.md). Ollama: ein nativer Installer,
Hintergrundbetrieb und getrennte Programm-/Modelldaten. LM Studio: integrierte
Laufzeitverwaltung und Modellzustände. Tailscale: ausdrücklich wählbarer Betrieb
nach Abmeldung/Neustart. Windows Server verlangt einen Dienst; ein verstecktes
Terminal oder HKCU-Autostart reicht dafür nicht.

## Umsetzungspakete und Verantwortlichkeiten

1. **Ein gemeinsamer Produkteinstieg.** Neues `desktop`-Kommando mit geschützter
   lokaler Oberfläche. Es verwaltet Zentrale und lokalen Agenten, verwendet
   bestehende Protokolle, erzeugt lokale PKI/Zugänge und meldet den lokalen Agenten
   intern an. Persistente Identitäten bleiben bei erneutem Öffnen erhalten.
   Kurzlebige UI-Zugänge bleiben lokal, HttpOnly/SameSite und Origin-/Hostprüfung.
   Keine Administrator-Schlüssel im Browser oder Dienstlog.
2. **Laufzeitverwaltung.** Anwendungseigener fester CPU-Laufzeitkatalog mit
   Version, HTTPS-Quelle und SHA-256. Expliziter Download, Fortschritt, Abbruch,
   begrenzte Größen/Weiterleitungen und Probe vor Nutzung. Fehler ersetzen keine
   funktionierende Laufzeit. Archivimport bleibt Expertenoption.
3. **Geführte Oberfläche.** Voraussetzungen → Laufzeit → Modell → echte Testantwort.
   Fortschritt, verständliche Fehler und Wiederholen ohne Identitätsduplikate.
   Modellbereit und Installation vorhanden sind getrennte Zustände. Fortgeschrittene
   Verwaltung bleibt über einen angemeldeten lokalen Einstieg erreichbar.
4. **Windows-Setup und Lebenszyklus.** Ein Inno-Setup-EXE, stabile App-ID,
   Startmenüeintrag, Deinstallation und getrennte Datenordner. Desktopmodus pro
   Benutzer; Servermodus ausdrücklich mit Administratorrechten und nativem Dienst.
   Geschützte ProgramData-Daten bleiben auf Administratoren und Dienstkonto begrenzt;
   kein breites Leserecht für lokale Benutzer. Updates stoppen nur eigene Prozesse
   und erhalten Daten; Deinstallation bewahrt Modelle, PKI und Audit standardmäßig.
5. **Dokumentation und Verifikation.** Ein klarer Installationsweg statt widersprüchlicher
   Pilotanleitungen. Wiederholbare Paket-/Browser-/Integrationstests und unabhängige
   Review-Runden. Kommentare erklären Gründe und Sicherheits-/Lebenszyklusgrenzen;
   keine Tagebücher, redundanten Funktionsheader oder auskommentierten Altpfade.

## Prüfkriterien

Bewertung nach der gewichteten Tabelle in RESEARCH.md: Installation 20,
Erstinferenz 20, Lebenszyklus 15, Fehlerbehebung/Daten 15, Vertrauen/Sicherheit 15,
Paketwartung 10, Klarheit/Zugänglichkeit 5. Ziel mindestens 90/100.

Pflichtprüfungen: frische Installation und Wiederöffnung; Portkonflikt;
fehlendes Netzwerk/Abbruch/fehlerhafte Prüfsumme; keine fremde Origin oder Host;
kein Schlüssel im Log; bestehende Identität behalten; Build Linux und Windows;
Windows install/update/uninstall sowie Serverneustart ohne Anmeldung.
Automatisierte Engine-Doubles prüfen Orchestrierung, ersetzen keinen echten
Modelltest auf der Zielhardware.

Eine Codebewertung von 9/10 ersetzt keine Windows-Produktabnahme. Nicht ausführbare
Windows-/Codesignierungs-/Hardwareprüfungen werden als offene Nachweise ausgewiesen.
Keine erfundene Freigabe, keine Veröffentlichung als signierte Produktion ohne
entsprechende Belege.

## Git und Lieferung

Eigener Branch `codex/easy-installation`, kleine thematische Commits, keine
Modellgewichte/Zugangsdaten/Buildartefakte im Git. Ausgangsstand und Testergebnisse
werden dokumentiert. Entwurf-PR gegen den Entwicklungsstand, kein automatischer Merge
oder Deployment auf den Lightsail-Server. Offene Befunde werden vor Abschluss
korrigiert oder mit konkretem verbleibendem Nachweis benannt.
