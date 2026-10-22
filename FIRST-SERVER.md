# Den ersten Server in Betrieb nehmen

Diese Anleitung beschreibt den manuellen Pilotaufbau mit Gateway, Agent und einer separaten llama.cpp-Engine auf einem Server.
Die Anwendung wird noch nicht als Systemdienst installiert. Die drei Prozesse muessen fuer den Test weiterlaufen.

## 1. Passendes Testpaket verwenden

GitHub > Actions > "Linux and Windows Pilot Packages" > erfolgreicher Lauf > Artifacts > Linux-Windows-Pilot.
Download entpacken und darin das zum Server passende ZIP entpacken:

- Linux mit Intel/AMD: KI-Basis-linux-amd64.zip
- Linux mit ARM64: KI-Basis-linux-arm64.zip
- Windows mit Intel/AMD: KI-Basis-windows-amd64.zip

Die Pakete enthalten fertige Programmdateien. Go, Python und Docker sind auf dem Testserver nicht erforderlich.
Es sind Entwicklungsbuilds, keine signierten Installationspakete.

## 2. Gateway starten

Linux, Terminal 1 im entpackten Ordner:

```sh
chmod +x ki-basis
./ki-basis quickstart --dir ./Daten
```

Windows, PowerShell 1:

```powershell
.\ki-basis.exe quickstart --dir .\Daten
```

Der erste Start erstellt Daten/config.json, die lokale CA, einen API-Key und die Audit-Datenbank.
Die Anwendung zeigt den API-Key und die Benutzer-ID local-preview im Terminal.
Der Browserzugang liegt unter http://127.0.0.1:3000, die eigentliche API unter https://127.0.0.1:8443.
Beide sind bewusst nur auf dem Server selbst erreichbar. Nach erfolgreichem Pilot kann der regulaere Netzwerkbetrieb
mit eigener Serveradresse nach AGENT-SETUP.md eingerichtet werden.

## 3. Engine und Modell starten

Die passende llama.cpp-Ausgabe separat von https://github.com/ggml-org/llama.cpp/releases herunterladen.
CPU-Version oder zum GPU-Treiber passender Build; Windows benoetigt llama-server.exe und die mitgelieferten Bibliotheken.

Linux, Terminal 2, vorhandene GGUF-Datei, CPU:

```sh
./llama-server --model /pfad/gemma4.gguf --alias gemma-4-26b-a4b \
  --host 127.0.0.1 --port 8081 --ctx-size 2048 --parallel 1 --n-gpu-layers 0
```

Windows, PowerShell 2:

```powershell
.\llama-server.exe --model C:\Modelle\gemma4.gguf --alias gemma-4-26b-a4b `
  --host 127.0.0.1 --port 8081 --ctx-size 2048 --parallel 1 --n-gpu-layers 0
```

Die Modelldateien stammen aus https://huggingface.co/google/gemma-4-26B-A4B-it-qat-q4_0-gguf.
Vor dem Download Speicherplatz und benoetigten RAM/VRAM anhand der gewaehlten Dateien pruefen.
Fuer automatische Downloads direkt durch llama.cpp kann statt --model auch
`-hf google/gemma-4-26B-A4B-it-qat-q4_0-gguf:Q4_0` verwendet werden, wenn diese Engine-Version das Repository unterstuetzt.
Der Modelldownload ist noch keine Funktion unserer App.

Fuer GPU-Ausfuehrung den passenden Build verwenden und --n-gpu-layers entsprechend anpassen.
Startparameter und Hardwarepruefung stehen in PILOT-TEST.md. Zunaechst nur eine gleichzeitige Anfrage testen.

## 4. Ersten Agent registrieren

Linux, Terminal 3 im KI-Basis-Ordner:

```sh
./ki-basis join-token --config ./Daten/config.json \
  --node-id server-01 --url https://127.0.0.1:8091 \
  --revision gemma4-q4_0-pilot --capacity 1
```

Den Wert aus dem JSON-Feld token kopieren. Danach interaktiv einlesen:

```sh
read -r -s -p 'Join token: ' taskJoinToken
printf '%s' "$taskJoinToken" | ./ki-basis agent join \
  --gateway https://127.0.0.1:8443 --ca ./Daten/pki/ca.crt \
  --state ./Daten/agent.json --token-stdin
unset taskJoinToken
./ki-basis agent run --state ./Daten/agent.json \
  --listen 127.0.0.1:8091 --engine http://127.0.0.1:8081
```

Windows, PowerShell 3:

```powershell
.\ki-basis.exe join-token --config .\Daten\config.json `
  --node-id server-01 --url https://127.0.0.1:8091 `
  --revision gemma4-q4_0-pilot --capacity 1
$taskJoinSecret = Read-Host 'Join token' -AsSecureString
[System.Net.NetworkCredential]::new('', $taskJoinSecret).Password | .\ki-basis.exe agent join `
  --gateway https://127.0.0.1:8443 --ca .\Daten\pki\ca.crt `
  --state .\Daten\agent.json --token-stdin
Remove-Variable taskJoinSecret
.\ki-basis.exe agent run --state .\Daten\agent.json `
  --listen 127.0.0.1:8091 --engine http://127.0.0.1:8081
```

Modell- und Engine-Alias sind durch den Standard Gemma 4 bereits vorbelegt.
Nach einem regulaeren Neustart nur die vorhandene Engine und agent run starten; keinen neuen Join ausfuehren.
Die einfache Revisionsangabe dient nur dem ersten Pilot. Fuer dauerhafte Replikate Modellhash und Engine-Version festhalten.

## 5. Im Browser ansehen

Mit Browser direkt auf dem Server: http://127.0.0.1:3000.
API-Key aus Terminal 1 und Benutzer-ID local-preview eintragen, Status laden.
Nach positiver Bereitschaftsmeldung muss der Agent als bereit erscheinen.

Vom Mac auf einen per SSH erreichbaren Linux-Server:

```sh
ssh -L 3000:127.0.0.1:3000 BENUTZER@SERVER
```

Dieses SSH-Fenster offen lassen und auf dem Mac http://127.0.0.1:3000 oeffnen.
Hier stellt SSH die Weiterleitung her; ohne diese Weiterleitung waere localhost nur der Mac selbst.
Fuer einen Windows-Server ist zunaechst der Browser in der Remotedesktop-Sitzung die einfache Variante.

## 6. Echte Antwort und Audit pruefen

Auf dem Server eine kurze Streaming-Anfrage an https://127.0.0.1:8443/v1/chat/completions senden.
Die lokale CA aus Daten/pki/ca.crt als vertrauenswuerdig angeben. Kein Abschalten der Zertifikatspruefung erforderlich.
API-Key und X-User-ID sind Pflicht. Beispiel-Nutzlast:

```json
{"model":"gemma-4-26b-a4b","messages":[{"role":"user","content":"Antworte kurz: Was ist lokale KI?"}],"stream":true,"max_tokens":64}
```

Anschliessend im KI-Basis-Verzeichnis:

```sh
./ki-basis audit-verify --config ./Daten/config.json
./ki-basis audit-export --config ./Daten/config.json
```

Unter Windows jeweils .\ki-basis.exe verwenden. Erwartet werden Annahme und Abschluss mit Benutzer-ID, Modell und Knoten;
Prompt- und Antwortinhalte werden nicht gespeichert.

Erst wenn dieser erste Server funktioniert, einen zweiten Rechner nach AGENT-SETUP.md ergaenzen.
