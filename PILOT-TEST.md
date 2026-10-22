# Funktionstest auf Linux und Windows, CPU und GPU

Stand: Entwicklungsbuild für einen technischen Pilot. Linux/CPU ist mit echten Modellen getestet;
Windows ist bisher cross-kompiliert. GPU-Treiber, GPU-Ausführung und Windows-Laufzeit werden mit diesem Pilot erstmals geprüft.

## Dateien

- Linux amd64: `bin/ki-basis`
- Windows amd64: `bin/ki-basis.exe`
- Gateway-Konfiguration: `examples/config-agents.json`
- Einrichtung und Join: `AGENT-SETUP.md`

Die KI-Basis-Binaries enthalten Gateway, Agent und Statusoberfläche.
llama-server und Modellgewichte werden separat bereitgestellt. Es gibt noch keinen Dienstinstaller.

## Standardmodell

Gemma 4 26B A4B ist die gewünschte MoE-Variante. Standardprofil für llama.cpp:

- Repository: https://huggingface.co/google/gemma-4-26B-A4B-it-qat-q4_0-gguf
- Format: GGUF, offizielle QAT-Q4_0-Variante.
- API- und Engine-Alias: `gemma-4-26b-a4b`
- Modellkarte: https://ai.google.dev/gemma/docs/core/model_card_4

Das Profil ist hinterlegt; Gemma wurde in dieser Entwicklungsumgebung noch nicht heruntergeladen oder ausgeführt.
Die bisherigen Inferenznachweise verwenden ein kleines TinyStories-Modell.
MoE reduziert die pro Token aktiven Parameter, nicht den Speicherbedarf auf die Größe der aktiven Teilmenge.
Gewichte, KV-Cache und Engine benötigen gemeinsam ausreichend RAM beziehungsweise VRAM.
Beginne mit einer parallelen Anfrage und 2048 Kontexttokens; genaue Speicher- und GPU-Offload-Werte richten sich nach der Testhardware.
Die Anwendung unterstützt zunächst Textanfragen, auch wenn das Basismodell weitere Fähigkeiten bietet.

## Engine vor dem Agent prüfen

Passende offizielle llama.cpp-Version je Testrechner installieren:
https://github.com/ggml-org/llama.cpp/releases

Für GPU-Ausführung muss der Build zum Backend und Treiber passen, beispielsweise CUDA für NVIDIA oder ein passender Vulkan-Build.
Ein CPU-Build kann die GPU nicht durch eine Startoption aktivieren.

Linux, CPU:

```sh
./llama-server --model /pfad/gemma4.gguf \
  --alias gemma-4-26b-a4b --host 127.0.0.1 --port 8081 \
  --ctx-size 2048 --parallel 1 --n-gpu-layers 0
```

Linux, GPU mit passendem Build und ausreichend Speicher:

```sh
./llama-server --model /pfad/gemma4.gguf \
  --alias gemma-4-26b-a4b --host 127.0.0.1 --port 8081 \
  --ctx-size 2048 --parallel 1 --n-gpu-layers 99
```

Windows PowerShell, CPU:

```powershell
.\llama-server.exe --model C:\Modelle\gemma4.gguf `
  --alias gemma-4-26b-a4b --host 127.0.0.1 --port 8081 `
  --ctx-size 2048 --parallel 1 --n-gpu-layers 0
```

Windows PowerShell, GPU:

```powershell
.\llama-server.exe --model C:\Modelle\gemma4.gguf `
  --alias gemma-4-26b-a4b --host 127.0.0.1 --port 8081 `
  --ctx-size 2048 --parallel 1 --n-gpu-layers 99
```

99 fordert Offload für alle vorhandenen Modellschichten an. Passt das Modell nicht vollständig in den Grafikspeicher,
muss ein niedrigerer Wert nach Hardwaretest gewählt werden. Im Engine-Startlog kontrollieren, welche GPU erkannt wurde
und wie viele Schichten tatsächlich ausgelagert wurden. Eine erfolgreiche Antwort allein beweist noch keine GPU-Nutzung.

Auf jedem Modellserver zuerst `http://127.0.0.1:8081/health` und `/v1/models` prüfen.
Der Alias muss exakt `gemma-4-26b-a4b` lauten.

## Gateway und Agent anbinden

Gateway und CA einmalig nach `AGENT-SETUP.md` einrichten. Für jeden Modellserver eine eigene Knoten-ID und HTTPS-Adresse vergeben.
`join-token` verwendet nun standardmäßig den Modellalias `gemma-4-26b-a4b`.
Bei Replikaten Gewichtsdatei, Quantisierung und Engine-Version in derselben Revision dokumentieren.

Nach dem Join den Agenten für die bereits gestartete Engine ausführen:

Linux:

```sh
./ki-basis agent run --state data/agent.json \
  --listen 0.0.0.0:8091 --engine http://127.0.0.1:8081
```

Windows:

```powershell
.\ki-basis.exe agent run --state data/agent.json `
  --listen 0.0.0.0:8091 --engine http://127.0.0.1:8081
```

Die Engine wird bei dieser Variante separat verwaltet. Sie muss vor dem Agentenstart laufen.
Gateway und Modellserver müssen einander im Netz erreichen; noch kein ausgehender Tunnel für NAT-Umgebungen.

## Testablauf

1. Jeden der vier Fälle einzeln prüfen: Linux/CPU, Linux/GPU, Windows/CPU, Windows/GPU.
2. Mit API-Key und `X-User-ID` eine kurze gestreamte Antwort über das Gateway abrufen.
3. Ohne Key eine 401-Antwort, ohne Benutzer-ID eine 400-Antwort erwarten.
4. Audit prüfen: Anfrage-ID, Benutzer-ID, Modellrevision und Knoten müssen passen; Prompt/Antwort dürfen nicht enthalten sein.
5. Zweiten passenden Knoten bei laufendem Gateway anmelden; beide müssen Anfragen erhalten.
6. Einen Agenten stoppen; beim regulären Stopp wird er abgemeldet, bei Verbindungsverlust endet die Freigabe spätestens nach 20 Sekunden.
7. Gateway neu starten; laufende Agenten müssen ohne neuen Join wieder bereit werden.
8. Einen Knoten widerrufen; neue Anfragen dürfen ihm nicht mehr zugewiesen werden.

Pro Test Rechner/OS, Engine-Version, Modellhash, Quantisierung, RAM/VRAM, tatsächlichen GPU-Offload,
Zeit bis zum ersten Token und Fehlermeldungen festhalten. Noch kein Leistungsversprechen für 40 gleichzeitige Anfragen.

## Statusoberfläche und Webpreview

Die echte Gateway-Oberfläche ist direkt am Gateway unter `/` erreichbar. Statusabfragen benötigen API-Key und Benutzer-ID.
Die Oberfläche dient dem Status; Downloads, Key-Verwaltung und Join-Erstellung erfolgen noch außerhalb der Oberfläche.

In der Codex-Entwicklungsumgebung startet ein eigener lesender Vorschauzugang:

```sh
python3 scripts/dev_preview.py
```

Webpreview-Port: 3000. Das Skript startet ein echtes lokales TLS-Gateway ohne künstliche Modellserver.
Der Zugangsschlüssel liegt ausschließlich in `work/preview/access.json`; eine beliebige zulässige pseudonyme Benutzer-ID kann verwendet werden.
Der Vorschauzugang stellt nur die Oberfläche sowie lesende Health-/Status-/Modell-Endpunkte bereit.
Er leitet keine Inferenz- oder Verwaltungsaufrufe weiter. Er ist für die Entwicklungsumgebung vorgesehen.
Auf Testrechnern direkt das reguläre HTTPS-Gateway verwenden.
