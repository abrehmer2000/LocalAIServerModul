# KI-Basis installieren und erstmals verwenden

Der neue Einrichtungsweg verbindet Zentrale und Modellserver auf **einem
Rechner**. Im normalen Ablauf sind weder Terminalbefehle noch das Bearbeiten
von JSON-Dateien, Kopieren von Schlüsseln oder Übertragen einer Verbindungsdatei
nötig. Prozesslog wird separat installiert.

Dieser Stand ist ein **Testkandidat**. Das Windows-Setup ist ohne hinterlegte
Codesignierung ein **unsignierter Testbuild**, keine veröffentlichte signierte
Produktversion. Der native Windows-Nachweis muss im Workflow und anschließend
auf dem Zielrechner erfolgen; ein erfolgreicher Linux-Test oder Windows-Build
ersetzt ihn nicht. Prüfstand und offene Nachweise stehen in
[Abnahme](docs/installation/ACCEPTANCE.md).

## 1. Das passende Paket laden

Für Windows mit Intel-/AMD-Prozessor verwende das einzelne Setup-EXE
`KI-Basis-<Version>-windows-x64-setup.exe`. Der Workflow **Windows Installer**
stellt es nach erfolgreichem Lauf im Artefakt **KI-Basis-Windows-Unsigned**
zusammen mit `SHA256SUMS.txt` bereit. Der Download des GitHub-Artefakts kann
ein einmaliges Entpacken erfordern; das Setup selbst benötigt keine weiteren
Programmarchive. Ein öffentlicher direkter Release-Download ist damit noch
nicht eingerichtet.

Die Installer-Untergrenze ist Windows 10 22H2; Windows 11 und Windows Server
2025 sind vorgesehene Testziele. Eine Freigabe gilt erst für tatsächlich
geprüfte Systeme. Der automatische CPU-Laufzeitdownload unterstützt Windows
x64 und Linux x64. Andere Plattformen und GPU-Laufzeiten verwenden die
[erweiterte Einrichtung](SETUP-ASSISTENT.md).

Go, Python und Docker sind auf dem Zielrechner nicht erforderlich. Downloads
benötigen Internetzugang zu GitHub und zur Modellquelle. Die Laufzeit benötigt
etwa 24 MB Download; das Modell kann viele GB beanspruchen und zusätzlich viel
Arbeitsspeicher benötigen. Freien Speicher und Modelllizenz vorab prüfen.

## 2. Desktop- oder Serverbetrieb wählen

Starte das Setup und wähle den passenden Betriebsmodus:

| Modus | Verwendung | Verhalten |
|---|---|---|
| **Desktopbetrieb nur für mich** | Test auf deinem eigenen Rechner | Installation unter deinem Benutzerkonto. Optionaler Start bei deiner Windows-Anmeldung. Nach Abmeldung läuft diese Instanz nicht weiter. |
| **Serverbetrieb für alle Benutzer** | Dauerbetrieb, beispielsweise auf einem Lightsail-Windows-Server | Benötigt Administratorrechte und richtet einen Windows-Dienst ein. Er soll auch ohne Benutzeranmeldung laufen. Abmeldung und Neustart müssen vor unbeaufsichtigter Nutzung auf dem Zielserver geprüft werden. |

Für den Servermodus meldest du dich zur ersten Einrichtung beispielsweise per
RDP auf dem Server an. Über das Startmenü öffnest du anschließend
**KI-Basis Server verwalten**; die Verwaltung verlangt Administratorrechte.
Im Desktopmodus öffnest du **KI-Basis**. Beide Einstiege öffnen die lokale
Browseroberfläche selbst. Es müssen keine PowerShell-Fenster offen bleiben.

Die Standardinstallation öffnet keine Firewallports. Ihre Verwaltung und ihr
Gateway sind nur auf diesem Rechner erreichbar. Ein anderer Rechner benötigt
eine bewusst eingerichtete Netzwerkverbindung; siehe
[erweiterte Einrichtung](SETUP-ASSISTENT.md).

## 3. Vorbereiten

In der Browseroberfläche zeigt **Download und Speicherort** die ausgewählte
CPU-Laufzeit, Quelle, Größe, Prüfsumme und den Datenordner an.

Mit **Vorbereiten** bestätigst du den Laufzeitdownload. KI-Basis prüft die
Datei und startet eine kurze Kompatibilitätsprobe. Danach verbindet es den
lokalen Modellserver intern mit der Zentrale. Zertifikate und Zugänge werden
automatisch verwaltet.

Der Status **Verbunden** bedeutet, dass die lokale Verbindung vorbereitet ist.
Das Modell ist zu diesem Zeitpunkt noch nicht heruntergeladen oder bereit.
Du kannst den laufenden Vorgang mit **Abbrechen** stoppen und später erneut
starten. Auf einer nicht unterstützten Plattform steht der manuelle
Laufzeitimport im bisherigen Manager zur Verfügung.

## 4. Modell herunterladen und starten

Prüfe den Link **Modellquelle und Lizenz ansehen**. Wähle dann
**Modell herunterladen und starten**. Größe und Fortschritt erscheinen beim
Download. Anschließend wird das Modell geprüft und in den Arbeitsspeicher
geladen. Erst **Modell bereit** bestätigt die Bereitschaft.

Der vorhandene Katalog verwendet Gemma; das ist ein großes Modell. Der
Standardweg arbeitet auf der CPU mit **einem Anfrageplatz**. Die verfügbare
RAM-Menge, Kontextlänge und weitere Programme beeinflussen den Bedarf.
Die Anwendung garantiert keine ausreichende Leistung für eine bestimmte
Instanzgröße. Auf CPU-Servern können Laden und Antworten mehrere Minuten
dauern. Eine gestartete Anwendung und ein heruntergeladenes Modell allein
sind noch kein bestandener Funktionstest.

## 5. Eine echte Antwort prüfen

Sobald das Modell bereit ist, wähle **Testantwort erzeugen**. Die Oberfläche
zeigt den tatsächlich erzeugten Begrüßungssatz an. **Test bestanden** bestätigt
den vollständigen Weg durch Gateway und Modell zum Zeitpunkt des Tests.

Die Browserseite darfst du danach schließen. Im Desktopmodus läuft KI-Basis
bis zum bewussten **KI-Basis beenden** oder zur Abmeldung weiter. Über das
Startmenü öffnest du dieselbe Einrichtung erneut. Im Servermodus wird der
Prozess durch die Windows-Diensteverwaltung gesteuert; das Schließen der Seite
beendet den Dienst nicht.

Vor unbeaufsichtigtem Serverbetrieb: Melde dich ab oder starte den Server
neu, ohne dich danach anzumelden. Prüfe anschließend über den vorgesehenen
Verwaltungsweg Dienststatus und Erreichbarkeit und erzeuge erneut eine
Testantwort. Die bloße Anzeige „Servermodus“ ersetzt diese Betriebsprüfung
nicht.

## Wenn etwas nicht funktioniert

- **Download fehlgeschlagen:** Netzwerk und freien Speicher prüfen und den
  Vorgang erneut starten. Bei falscher Prüfsumme wird die Laufzeit nicht
  verwendet.
- **Laufzeit inkompatibel:** Systemanforderungen prüfen; eine passende eigene
  Laufzeit kann über den bisherigen Manager importiert werden.
- **Modell noch nicht bereit:** Den angezeigten Download-/Ladezustand abwarten.
  Bei Fehlern Speicher, Modellquelle und Laufzeit prüfen.
- **Verbindung zur Oberfläche unterbrochen:** KI-Basis über das Startmenü erneut
  öffnen. Eine bestehende Einrichtung wird wiederverwendet.
- **Test ohne vollständige Antwort:** Modellstatus und Auslastung prüfen und
  den Test wiederholen. Ein Modellprozess allein beweist keine erfolgreiche
  Antwort.

Weitere Diagnose- und Mehrrechnerwege stehen in
[SETUP-ASSISTENT.md](SETUP-ASSISTENT.md). Der
[Laufzeitkatalog](docs/installation/RUNTIME-CATALOG.md) dokumentiert Quellen
und Prüfsummen. Private Datenordner enthalten Zugänge und Zertifikate;
sie gehören nicht in GitHub, Screenshots oder öffentliche Supportanhänge.

## Updates, Daten und Deinstallation

Installiere ein Update im **gleichen Modus** und im **gleichen Programmordner**.
Das Setup versucht zuerst, seine eigene laufende Instanz sauber zu beenden.
Falls das nicht gelingt, bricht es ab, statt fremde Prozesse zu beenden.
Ein Downgrade auf eine ältere Programmversion wird nicht unterstützt.

Die Datenordner bleiben bei Updates und normaler Deinstallation erhalten:

| Modus | Datenordner |
|---|---|
| Desktop | `%APPDATA%\KI-Basis\desktop` |
| Server | `%ProgramData%\KI-Basis\desktop` |

Darin liegen unter anderem Modellgewichte, Konfiguration, Zertifikate und
Auditdaten. Eine Sicherung auf einem getrennten Datenträger bleibt notwendig.
Ein Wechsel des Installationsmodus übernimmt die Daten nicht automatisch.

Die Deinstallation erfolgt über **Installierte Apps / Programme entfernen**
oder den Startmenüeintrag **KI-Basis deinstallieren**. Sie entfernt die
installierten Programmdateien und die zugehörige Startregistrierung bzw. den
Dienst. Eine gewünschte vollständige Datenlöschung ist ein gesonderter
administrativer Schritt nach Sicherung und Prüfung des richtigen Ordners.

Eine Deinstallation oder ein gestoppter Dienst löscht keine AWS-Lightsail-
Instanz und beendet keine Cloud-Kosten. Cloud-Ressourcen werden ausschließlich
über die jeweilige Infrastrukturverwaltung verwaltet.
