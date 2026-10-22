# Begleiteter Test nach der Stabilisierung

## Vorbereiten

1. Das neue vollständige Manager-Paket aus dem aktuellen GitHub-Stand verwenden.
   Gestoppte Cloudserver erhalten durch einen GitHub-Commit kein Update.
2. Falls alte Daten verloren sind: `server-01.tar.gz` über den Zentralenimport
   in einen leeren Ordner auf dem Rechner der Zentrale einlesen. `server-02.tar.gz`
   über den Agent-Import auf dem Rechner des Agenten einlesen. Der Import auf dem
   Mac stellt keine entfernte Cloudinstallation wieder her. Bei der ersten
   Wiederaufnahme deshalb den bisherigen administrativen Zugriff bereithalten.
3. Passende Laufzeit bereitstellen. Die alten Archive importieren keine Programme,
   Modelle oder Host-SSH-Schlüssel. Bestehende Tunnel und gegebenenfalls neue
   Cloud-IP-Adressen separat prüfen. Beide bisherigen Server bleiben bis zum Test
   ausgeschaltet; der Manager schaltet keine kostenpflichtigen Ressourcen ein.

## Hauptablauf

- Frischen Linux-Agenten über die drei SSH-Schritte installieren. Eine fehlende
  Laufzeit oder eine vom Zielserver unerreichbare Zentrale muss eine konkrete
  Diagnose liefern, bevor eine Identität angelegt wird.
- Für jeden Agenten eine sichtbare vollständige Antwort im abschließenden Test
  verlangen. Kurzen und längeren Stream sowie parallele Anfragen prüfen.
- Denselben SSH-Ablauf mit **Vorhandene Einrichtung fortsetzen** wiederholen:
  keine neue Identität, kein zweiter Prozess. Dann Agent beenden und so erneut
  starten. Abgelaufene Datei darf nur zur passenden bestehenden Identität dienen.
- Eine Datei für einen anderen Server muss beim Fortsetzen abgewiesen werden.
- Neue Zentralen-/Agent-ZIPs herunterladen. In leere Ordner restaurieren;
  vorherige Instanzen ausgeschaltet lassen. Audit-Kette und echte Antwort prüfen.

## Betriebsabnahme gesondert

- Bei systemd den Dienst ausdrücklich wählen. Ein nicht verfügbarer oder
  fehlgeschlagener Dienst muss als Fehler erscheinen, ohne stillen Hintergrundstart.
- Auf einem Testserver echten Maschinenneustart durchführen. Agent, Modell und
  Netzwerkzugang müssen ohne Manager-Sitzung wiederhergestellt werden. Der
  automatisierte Prozessneustarttest ersetzt diesen Nachweis nicht.
- Bei Containern Startbefehl, Neustartrichtlinie und dauerhaften Speicher in der
  Infrastruktur festlegen. Hintergrundbetrieb wird als solcher angezeigt und
  überlebt allein keinen Maschinenneustart.
- Agent-zu-Zentrale-Tunnel müssen ebenfalls eigenständig wieder starten. Der
  Manager-Tunnel verbindet ausschließlich den Manager mit der Zentrale.

Danach erst Plattformkombinationen freigeben. Native Mac-/Windows-Bedienung,
reale GPU-Beschleunigung, Langzeittest und Update/Rückweg bleiben eigene Nachweise.
