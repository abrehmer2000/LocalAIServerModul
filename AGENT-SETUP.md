# Weitere Modellserver per Agent hinzufügen

Die Registrierung funktioniert im laufenden Gateway. Jeder Knoten hat eine eigene Geräteidentität.
Ein Join-Token ist zehn Minuten gültig und einmal verwendbar. Die Registrierung ist an Adresse,
Modell, Revision und Kapazität gebunden; der Agent kann diese Angaben nicht selbst erweitern.

## 1. Gateway einmalig vorbereiten

Im Gateway-Arbeitsverzeichnis:

```sh
./bin/ki-basis init-tls --dir pki --host 127.0.0.1
./bin/ki-basis keygen
```

Bei getrennten Rechnern `--host` auf den tatsächlichen DNS-Namen oder die IP des Gateways setzen.
`init-tls` legt eine eigene Installations-CA an und überschreibt keine vorhandenen Dateien.
`pki/ca.key` und `pki/gateway.key` bleiben ausschließlich auf dem Gateway.

`examples/config-agents.json` nach `config.local.json` kopieren und den Key-Hash einsetzen.
Für Netzwerkzugriff den Listener beispielsweise auf `0.0.0.0:8443` setzen;
das Gateway-Zertifikat muss den tatsächlich verwendeten Hostnamen abdecken.

```sh
./bin/ki-basis serve --config config.local.json
```

Die öffentliche Datei `pki/ca.crt` über einen vertrauenswürdigen Weg auf den Modellserver kopieren.
Sie ist kein Geheimnis, ihre Echtheit muss aber gesichert sein. Niemals die CA-Privatschlüssel kopieren.
API-Clients müssen dieser CA ebenfalls vertrauen.

## 2. Join-Token am Gateway erstellen

Das Gateway darf währenddessen weiterlaufen. Dieses Kommando ist eine lokale Verwaltungsaktion;
der Betriebssystemzugang und die Dateirechte schützen die Berechtigung.

```sh
./bin/ki-basis join-token --config config.local.json \
  --node-id gpu-02 \
  --url https://192.168.10.22:8091 \
  --model gemma-4-26b-a4b \
  --upstream-model gemma-4-26b-a4b \
  --revision MODELLHASH-QUANTISIERUNG-ENGINEVERSION \
  --model-sha256 SHA256_DER_GGUF_DATEI \
  --capacity 1
```

Modell-Prüfsumme unter Linux: `sha256sum modell.gguf`.
Unter PowerShell: `(Get-FileHash .\modell.gguf -Algorithm SHA256).Hash.ToLower()`.

Die JSON-Ausgabe enthält den einmaligen Token. Nicht in Git, gemeinsam genutzte Logs oder Tickets übernehmen.
Jeder Knoten bekommt eine eindeutige ID; eine widerrufene ID wird nicht wiederverwendet.
Replikate desselben öffentlichen Modells müssen dieselbe deklarierte Revision verwenden.
`--model-sha256` ist für eine vom Agent gestartete Engine verpflichtend.

## 3. Modellserver registrieren

Auf dem Modellserver sind die ausführbare KI-Basis-Datei, `llama-server`, das gewünschte GGUF-Modell
und die öffentliche CA-Datei erforderlich. Installer und automatischer Modelldownload folgen separat.

Bash, Token interaktiv ohne Echo oder Aufnahme des Wertes in die Befehlshistorie einlesen:

```sh
read -r -s -p 'Join token: ' taskJoinToken
printf '%s' "$taskJoinToken" | ./ki-basis agent join \
  --gateway https://192.168.10.10:8443 \
  --ca ca.crt --state data/agent.json --token-stdin
unset taskJoinToken
```

PowerShell:

```powershell
$taskJoinSecret = Read-Host 'Join token' -AsSecureString
[System.Net.NetworkCredential]::new('', $taskJoinSecret).Password | .\ki-basis.exe agent join `
  --gateway https://192.168.10.10:8443 `
  --ca ca.crt --state data/agent.json --token-stdin
Remove-Variable taskJoinSecret
```

`data/agent.json` enthält den privaten Geräteschlüssel. Unter Windows die ACL des Verzeichnisses
auf das Dienstkonto und berechtigte Administratoren begrenzen; Unix-Dateien werden mit Modus 0600 angelegt.
Eine bestehende Identität wird beim Join nicht überschrieben.
Wenn die Registrierung serverseitig erfolgreich war, aber die Antwort oder die lokale Datei verloren ging,
den Knoten widerrufen und mit neuer ID sowie neuem Token registrieren.

## 4. Agent und Modell starten

```sh
./ki-basis agent run --state data/agent.json \
  --listen 0.0.0.0:8091 \
  --engine http://127.0.0.1:8081 \
  --engine-binary /pfad/llama-server \
  --model-file /pfad/modell.gguf \
  --context-size 4096
```

Unter Windows entsprechend `ki-basis.exe` und einen Pfad zu `llama-server.exe` verwenden.
Der Agent prüft die Modell-Prüfsumme und startet die Engine mit festen Argumenten ohne Shell.
Der Engine-Port muss frei sein. Hardwareoptionen wie GPU-Offload sind in diesem Stand noch nicht über den Agent konfigurierbar.
Engine-Standardausgabe wird aus Datenschutzgründen nicht in Agent-Logs übernommen.
Nach fünf erfolglosen Starts endet der Agent mit Fehler. Bei regulärem Beenden wird der eigene Prozessbaum gestoppt.

Alternativ eine vorhandene lokale Engine verwenden: `--engine-binary` und `--model-file` weglassen.
Dann muss der Betreiber die korrekte Modellversion sicherstellen; eine Prüfung der Gewichtsdatei ist dem Agent nicht möglich.
Die vorhandene Engine wird beim Agent-Stopp nicht beendet.

## Netz und Bereitschaft

- Gateway und Agent benötigen direkten Netzwerkzugriff aufeinander. Noch kein ausgehender Tunnel/NAT-Durchstich.
- Agent-Port nur für das Gateway freigeben. Die Engine selbst bleibt auf einer Loopback-IP.
- Gegenseitige TLS-Authentifizierung ist verpflichtend, auch beim lokalen Agent-Test.
- Nur die Gateway-Geräteidentität darf die Agent-API ansprechen; andere gültige Gerätezertifikate reichen nicht aus.
- Alle fünf Sekunden prüft der Agent `/health` und den erwarteten Modellnamen aus `/v1/models` der Engine.
- Ohne positive Bereitschaftsmeldung wird der Knoten nicht genutzt.
- Nach zwanzig Sekunden ohne Heartbeat läuft seine Routing-Freigabe ab.
- Bei Gateway-Neustart bleiben die Geräte registriert; sie müssen erneut einen Heartbeat senden.
- Bei Agent-Neustart wird die vorhandene Identität wiederverwendet; kein neuer Join erforderlich.

Der erste Stand unterstützt die durch `init-tls` erzeugte Gateway-Identität.
Die Trennung von kundeneigenem öffentlichem API-Zertifikat und internem Geräte-Zertifikat folgt später.

## Widerruf und Audit

```sh
./bin/ki-basis node-list --config config.local.json
./bin/ki-basis node-revoke --config config.local.json --node-id gpu-02
./bin/ki-basis audit-verify --config config.local.json
```

Widerruf wirkt ohne Neustart auf neue Zuweisungen; bereits laufende Antworten dürfen zu Ende laufen.
Join-Token-Ausgabe, Anmeldung und Widerruf werden gemeinsam mit der jeweiligen Datenänderung atomar gespeichert.
Der Audit enthält zusätzlich Bereitschaftswechsel, erkannte Heartbeat-Ausfälle und Zertifikatserneuerungen.
CLI-Verwaltungsaktionen enthalten den vom lokalen Betriebssystem gemeldeten Benutzernamen;
Geräteaktionen enthalten die überprüfte Geräteidentität. Das ersetzt noch keine zentrale Admin-Benutzerverwaltung.

Gerätezertifikate sind 90 Tage gültig und werden bei laufendem Agent ab 30 Tagen Restlaufzeit automatisch erneuert.
Alte Zugangsdaten bleiben nach erfolgreicher Erneuerung zehn Minuten übergangsweise gültig.
Abgelaufene Identitäten nach langer Offline-Zeit müssen neu registriert werden.
Das Gateway-Zertifikat gilt ein Jahr und die CA zehn Jahre; deren Erneuerungswerkzeuge sind noch offen.
Dies sind Sicherheitszertifikate, keine Softwarelizenz oder Ablaufprüfung der gekauften Anwendung.

## Wiederholbarer Inferenztest

```sh
python scripts/agent_smoke.py \
  --gateway bin/ki-basis \
  --engine /pfad/llama-server \
  --model /pfad/tinystories15M.gguf
```

Der Test registriert zwei Agenten nacheinander im laufenden Gateway. Beide starten echte Modellprozesse.
Er prüft Inferenz, mTLS, Verteilung, Wiederanlauf nach Gateway-Neustart, Widerruf, Abmeldung, Prozessbeendigung und die Audit-Kette.
Alle Prozesse laufen dabei auf einem Rechner; getrennte Rechner und Windows-Laufzeit bleiben Pilotprüfungen.
