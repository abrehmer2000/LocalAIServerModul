# Geführte Einrichtung für Cloud und On-Premises

Der Assistent benötigt keine Anbieter-API. Hostinger-VPS, Hetzner-VMs,
dedizierte Server und eigene Linux-Rechner verwenden denselben SSH-Ablauf.
Windows- und macOS-Rechner werden mit dem lokalen Agent-Paket verbunden.
Gewöhnliches Webhosting ohne ausführbare eigene Prozesse ist kein Modellserver.

## Einstieg ohne eingegebene Terminalbefehle

- **Mac:** `KI-Basis Manager.app` öffnen. Alternativ gibt es
  `Server-einrichten.command`. Das Apple-Silicon-Paket enthält auch Linux-Agenten
  für amd64 und arm64. Die App ist eine noch nicht notarierte Pilotversion.
- **Windows:** `KI-Basis-Manager.exe` öffnen. Das Konsolenprogramm `ki-basis.exe`
  bleibt für Administratoren und Automatisierung enthalten.
- **Linux mit Desktop:** `Start-Agent.desktop` öffnen.
- **Linux ohne Desktop:** Den SSH-Assistenten auf einem vorhandenen Manager
  verwenden. Für Cloud-init und eigene Verteilwerkzeuge gibt es zusätzlich
  `agent-bootstrap`. Dieser Weg ist eine Administrator-Alternative.

Der Manager öffnet seine Browseroberfläche selbst. Schlüssel und Pfade in der
SSH-Einrichtung beziehen sich auf den Rechner des Managers, nicht auf den
Rechner eines über das Netzwerk verbundenen Browsers. Eine native Dateiauswahl
ist unter macOS, Windows und Linux mit Zenity vorgesehen. Auf Headless-Systemen
bleibt eine Pfadeingabe als Alternative verfügbar.

## Ein durchgängiger Ablauf

Die Zentrale führt durch fünf Schritte:

1. **Verbindung:** Verbindungsweg, Servername und vom Agenten erreichbare
   HTTPS-Adresse der Zentrale. Loopback-Adressen müssen ausdrücklich als lokale
   Verbindung bzw. bereits eingerichteter Tunnel bestätigt werden.
2. **Modell:** Gemma als vorhandener Katalogeintrag, CPU/GPU-Modus und bei Bedarf
   erweiterte Einstellungen. Replikate übernehmen die vorhandene Modellrevision.
3. **Anmeldung:** Eine einmalige Verbindungsdatei erzeugen und herunterladen.
   Die Datei ist zehn Minuten gültig; eine Erneuerung ist möglich.
4. **Einrichtung:** Auf die erste Agent-Meldung warten, Download und Modellstart
   auslösen und den gemeldeten Fortschritt beobachten.
5. **Test:** Genau diesen Server reservieren und eine vollständige, sichtbare
   Testantwort verlangen. Ein HTTP-Erfolg oder ein gestarteter Prozess genügt nicht.

Bei einem Neuladen ist der angemeldete Server weiterhin als Karte vorhanden.
**Einrichtung fortsetzen** öffnet seinen Verbindungsstatus oder Funktionstest.
Es wird keine zweite Identität angelegt. Der Funktionsnachweis ist ein Test zum
Ausführungszeitpunkt, keine Garantie für die spätere Verfügbarkeit.

Nach der Anmeldung zeigt die Zentrale eine kompakte Verwaltungsansicht mit
Navigation, Serverkarten, API-Test, Sicherung und Audit. Die öffentliche
Einführungsansicht und das Anmeldeformular treten in den Hintergrund.
Sperren liegt getrennt unter **Zugang verwalten**.

Der Agent führt ebenfalls schrittweise durch Dateiauswahl, Laufzeit, Prüfung,
Anmeldung und den Übergang zurück zur Zentrale. Technische Pfade sind unter
**Erweiterte Einstellungen**. Ein Neuladen erhält den lokalen Einrichtungszugang
über einen HttpOnly-Sitzungscookie (SameSite=Strict); API-Schlüssel und Tokens
werden nicht in localStorage oder sessionStorage gespeichert. Derselbe
Datenordner öffnet eine bereits laufende Einrichtung erneut, statt einen zweiten
Agenten gegen dieselbe Identität zu starten.

## Was automatisch geprüft oder vorbereitet wird

- Betriebssystem/Architektur, Laufzeitstart, freier Modellserver-Port,
  Speicherpfad und Schreibzugriff; NVIDIA-Metadaten und freier Plattenplatz.
- Gültigkeit der Verbindungsdatei sowie HTTPS-Erreichbarkeit mit ihrer CA und
  echtem Hostnamenabgleich, bevor der einmalige Code verbraucht wird.
- Ein freier Modellserver-Port wird für eine neue Installation vorgeschlagen.
  Ein belegter Standardport der Einrichtungsoberfläche weicht automatisch aus.
- Vorhandene Laufzeiten im Paket, PATH und bekannten Pilotpfaden werden gesucht.
  Ein vom Nutzer ausgewähltes ZIP/TAR.GZ-Laufzeitpaket kann im Browser importiert
  werden. Die Laufzeit wird mit `--version` gestartet, bevor sie angeboten wird.
- Mitgelieferte Bibliotheken werden über die Umgebung des Modellprozesses
  gefunden. `ldconfig` oder globale Änderungen an Bibliothekspfaden entfallen.
- Container erzeugen einen Speicherhinweis. Das ist keine Erkennung eines
  tatsächlich gebuchten persistenten Volumes; dessen Lebensdauer bestimmt die
  Infrastruktur. Eine GPU-Erkennung ist ebenfalls kein Nachweis der Beschleunigung.

**Bewusst keine automatische Änderung von GPU-Treibern, Firewallregeln oder
Cloud-Verträgen.** Laufzeitpakete stammen aus einer vom Betreiber ausgewählten,
vertrauenswürdigen Quelle. Die Anwendung lädt und startet keine beliebigen
Programme aufgrund eines Modellauftrags der Zentrale.

## Linux-Server über SSH

Im Manager **Anderen Linux-Server über SSH einrichten** wählen:

1. Adresse, Port und Benutzer angeben; optional einen vorhandenen privaten
   Schlüssel über die native Dateiauswahl wählen. Alternativ wird der lokale
   SSH-Agent verwendet.
2. Den angezeigten Host-Fingerabdruck mit Serverkonsole/Administration vergleichen
   und bestätigen. Änderungen werden vor der Installation erneut erkannt.
3. Verbindungsdatei und bei Bedarf ein Linux-Laufzeitarchiv auswählen.
   Die konkrete Installation bestätigen.

Der Manager erkennt amd64/arm64, wählt seinen passenden mitgelieferten Agenten,
überträgt Programm, einmalige Verbindungsdatei und optional Laufzeitarchiv und
meldet den Rechner an. Die Installation liegt in
`$HOME/.local/share/KI-Basis`. Vorhandene Agent-Identitäten werden nicht
überschrieben. Zugangsdaten des Managers werden nicht auf den Zielserver kopiert.

Verschlüsselte Schlüssel können unter macOS/Linux mit **Schlüssel entsperren**
über einen nativen Passwortdialog in den SSH-Agenten geladen werden. Die
Passphrase läuft direkt zu `ssh-add`, nicht durch Browser, API oder Log.
Unter Windows wird ein bereits entsperrter OpenSSH-Agent vorausgesetzt.
OpenSSH-Client und `ssh-keyscan` müssen auf dem Manager installiert sein;
Linux-Ziele benötigen eine Shell, `tar` und `mktemp`. Individuelle ProxyCommand- und
SSH-Konfigurationen werden nicht übernommen. Für Sprungserver ist zunächst ein
passender direkter/VPN-Zugang erforderlich.

Ein Systemdienst wird nur auf ausdrücklichen Wunsch eingerichtet und benötigt
Linux mit laufendem systemd und Root-Zugang. Fehlen diese Voraussetzungen, wird
vor der Anmeldung ein Fehler angezeigt; ein angeforderter Dienst wird nie still
zu einem Hintergrundprozess. Der Dienst wird auf aktiv und aktiviert geprüft;
die authentifizierte lokale Agent-Oberfläche muss ebenfalls antworten.
`Restart=on-failure` und Start beim Systemboot werden durch systemd umgesetzt.
Ein echter Serverneustart bleibt Teil der Betriebsabnahme.

Ohne Dienstwahl läuft der Agent im Hintergrund. Das überlebt keinen Serverneustart.
In Containern ist ein expliziter Start-/Neustartmechanismus der Infrastruktur
nötig. Die lokale Oberfläche zeigt den gespeicherten Neustartmodus; ein
unbekannter Modus wird nicht als eingerichtet ausgegeben. Windows-Autostart bei
Benutzeranmeldung und Linux-Benutzerdienste bleiben separat verfügbar.
Ein Dienstversuch wird gespeichert: Fortsetzen wechselt dann nicht unbemerkt auf
Hintergrundbetrieb. Dienst- und Hintergrundlogs enthalten keinen Setup-Sitzungslink.

### Fehler beheben und sicher fortsetzen

Die SSH-Oberfläche zeigt fehlgeschlagene Prüfungen getrennt für Laufzeit,
Speicher, Modellport und Verbindung zur Zentrale. Rohe SSH-Ausgaben und fremde
Fehlermeldungen werden nicht im Browser angezeigt.

- **Vor der Anmeldung fehlgeschlagen:** Ursache beheben und mit einer noch
  gültigen Verbindungsdatei erneut installieren. Vorabprüfungen verbrauchen
  keinen Anmeldecode. Temporäre Laufzeiten werden bei Abbruch bereinigt.
- **Bereits angemeldet oder Start fehlgeschlagen:** **Vorhandene Einrichtung
  fortsetzen** wählen und dieselbe Verbindungsdatei behalten. Zum Abgleich einer
  bestehenden Identität darf sie abgelaufen sein. Server-ID, Gateway und CA müssen
  übereinstimmen; es wird kein neuer Code verbraucht und keine Identität ersetzt.
- Eine laufende passende Instanz wird wiederverwendet. Ein beendeter Agent wird
  mit seinen bisherigen Einstellungen gestartet. Gleichzeitige Bootstrapversuche
  werden durch eine Betriebssystemsperre verhindert; nach Prozessabbruch bleibt
  keine verwaiste Sperre zurück.
- Fortsetzen ist kein Programmupdate und importiert keine neue Laufzeit. Für eine
  andere Identität oder ein beschädigtes Identitätsdokument ist eine geprüfte
  Sicherung bzw. eine bewusst neue Anmeldung erforderlich.
- Bei einer unklar bestätigten Anmeldung zuerst den Status in der Zentrale
  prüfen. Ein bereits verbrauchter Token wird nicht automatisch erneut gesendet.

## SSH-Tunnel ohne offene Terminalfenster

Für eine vorhandene Zentrale im SSH-Assistenten **Vorhandene Zentrale über SSH
öffnen** wählen. Der Manager öffnet lokale Weiterleitungen für Dashboard und
Gateway, zeigt **Zentrale öffnen**, merkt sich das Profil und baut unterbrochene
SSH-Verbindungen erneut auf. Schlüssel müssen im lokalen SSH-Agenten entsperrt
sein. Der Manager muss laufen; **Programm beenden** beendet seine Verbindungen.
Cloud-Server werden dadurch nicht ausgeschaltet und Kosten dort nicht beendet.

Wichtig: Dieser Tunnel verbindet den Manager mit der Zentrale. Er stellt nicht
automatisch eine Verbindung zwischen zwei entfernten Servern her. Ein neuer
Agent benötigt selbst eine erreichbare, zum Zertifikat passende Gateway-Adresse
(z.B. öffentliches HTTPS, privates Netzwerk/VPN oder einen bereits eingerichteten
Agent-zu-Gateway-Tunnel). Automatisches VPN und das Verteilen eingeschränkter
Tunnel-Schlüssel auf weitere Server sind noch nicht enthalten.

## Sicherung und Wiederherstellung

- **Zentrale sichern:** privater ZIP-Download mit Konfiguration, PKI und
  konsistenter SQLite-Sicherung einschließlich WAL. Auch der Audit-Verlauf ist
  enthalten. Nur Administratoren können diese Sicherung erstellen.
- **Agent-Einstellungen sichern:** privater ZIP-Download mit eigener Identität
  und Einstellungen. In einer noch nicht angemeldeten Agent-Oberfläche lässt
  sich die Sicherung wieder einlesen. Die vorherige Instanz muss ausgeschaltet
  sein. Eine Identität darf nicht gleichzeitig auf zwei Rechnern laufen.
- Modelle, Laufzeitdateien, fremde SSH-Schlüssel und Host-Systemkonfiguration
  werden nicht automatisch mitgesichert. Ein Agent-Backup ersetzt keine Sicherung
  individuell außerhalb der Anwendung eingerichteter VPN-/SSH-Verbindungen.
- Archive enthalten Geheimnisse. Nicht auf GitHub oder in ungeschützte
  gemeinsame Ordner hochladen. Backup-Downloads sind nicht verschlüsselt.

Beide Sicherungstypen verwenden denselben versionierten ZIP-Vertrag mit einer
SHA-256-Prüfsumme je Nutzdatei (Version 2). Frühere KI-Basis-ZIPs der Version 1
bleiben lesbar. Prüfsummen erkennen Beschädigungen; sie ersetzen weder
Verschlüsselung noch einen vertrauenswürdigen Aufbewahrungsort.
Dateisätze unterschiedlicher Rollen, doppelte Pfade und beschädigte ZIP-Einträge
werden abgewiesen.

Im Manager **Zentralen-Sicherung auf diesem Rechner wiederherstellen** öffnen,
Datei und einen **leeren** absoluten Zielordner wählen und bestätigen, dass die
vorherige Instanz ausgeschaltet ist. Die Oberfläche importiert Daten auf dem
Manager-Rechner, nicht auf einem entfernten SSH-Ziel. Die Zentrale wird dabei
nicht automatisch gestartet und keine Dienstverwaltung geändert. Die Prüfung
umfasst Audit-Kette und zusammengehörige gültige PKI-Dateien. Für automatisierte
Verwaltung bleibt derselbe Vorgang als CLI verfügbar:

```
ki-basis restore --archive ki-basis-zentrale-backup.zip --dir /neuer/datenordner
ki-basis quickstart --dir /neuer/datenordner --open-browser
```

Die Restore-Funktion prüft die Audit-Kette und setzt PKI-/Datenbankpfade auf den
neuen Ordner um. Externe Node-CA-Dateien und KeyEnv-Umgebungsvariablen aus
individuellen statischen Konfigurationen müssen separat bereitgestellt werden.
Die früher manuell erstellten `server-01.tar.gz`/`server-02.tar.gz` können als
begrenzte Pilotmigration über denselben Import eingelesen werden. Erkannt werden
nur die bekannten Datenpfade `workspace/ki-daten/` und für den Agenten
`workspace/ki-daten/agent/`. Host-SSH-Schlüssel, Programme, Bibliotheken und Modelle
aus diesen Archiven werden bewusst nicht restauriert. Für andere TAR-Strukturen
existiert kein allgemeiner Import. Anschließend neue ZIP-Sicherungen erstellen.
Bestehende manuelle Tunnel müssen separat wiederhergestellt werden.

### Zwei überprüfbare Netzwerkwege

1. Erreichbares **HTTPS**, direkt oder über ein bereits eingerichtetes VPN.
2. Ein **bestehender Tunnel auf dem Agent-Rechner** mit passender Gateway-Adresse.

Beide Wege werden auf dem Zielrechner vor der Anmeldung mit Zertifikatsprüfung
getestet. Der Manager-Tunnel ersetzt den zweiten Weg nicht. Netzwerkzugang,
Zertifikatsadresse und gegebenenfalls Tunnel-Neustart müssen nach einem Umzug oder
Serverneustart erneut geprüft werden. Es gibt keine Anbieter-API, automatische
Firewalländerung, VPN-Verteilung oder Treiberinstallation.

## Grenzen dieses Piloten

Der Ablauf ist anbieterneutral, aber nicht jede Hardware ist getestet. Der
Downloadkatalog enthält derzeit nur Gemma. Der allgemeine Gateway-Konfigurationsweg
für bereits vorhandene Modell-APIs bleibt bestehen; ein grafischer Import solcher
APIs ist noch nicht enthalten. Automatische Auswahl/Downloads signierter
Laufzeitpakete, vollständige Windows-Systemdienste, native macOS-Intel-Pakete,
Cloud-Ressourcenbeschaffung und universelle Netzwerkbereitstellung sind weitere
Ausbauschritte. Die Oberfläche behauptet bei fehlender Unterstützung keinen Erfolg.
