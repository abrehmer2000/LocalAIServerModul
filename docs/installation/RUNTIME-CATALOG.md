# Geprüfter CPU-Laufzeitkatalog

Stand: 14. November 2026. Anwendungseigener Katalog in
`cmd/ki-basis/runtime_download.go`, fest auf llama.cpp `b12128` gebunden.
Es wird zur Laufzeit keine `latest`-Version aufgelöst. Nicht aufgeführte
Plattformen verwenden weiterhin den manuellen Archivimport.

Quelle: [offizielle Veröffentlichung b12128](https://github.com/ggml-org/llama.cpp/releases/tag/b12128)
und [GitHub-Veröffentlichungsmetadaten](https://api.github.com/repos/ggml-org/llama.cpp/releases/tags/b12128).

| Plattform | Offizielles Archiv | Bytes | SHA-256 |
|---|---|---:|---|
| Windows amd64, CPU | `llama-b12128-bin-win-cpu-x64.zip` | 24730477 | `2864bc950fe1681f1867ebc1bf0d6a66d18cc5561b29c285b4504787145bd7b7` |
| Linux amd64, CPU | `llama-b12128-bin-ubuntu-x64.tar.gz` | 23080410 | `a03e1f92e039dc23b630748db27f57f6287aa09a9d015c4eec6fc037657edf1e` |

Die beiden SHA-256-Werte wurden aus den offiziellen Asset-Metadaten entnommen
und nach separatem Download beider Archive mit `sha256sum` verglichen.
Die Linux-Laufzeit wurde auf dem Cloud-Linux-Rechner mit ihrem Bibliothekspfad
gestartet. `llama-server --version` meldete:

```text
version: 0.9.0-dev (build 12128, commit a01dff908)
built with GNU 11.4.0 for Linux x86_64
```

Das ist ein Ausführbarkeitsnachweis dieser Linux-Laufzeit, kein Inferenztest,
keine Windows-Ausführung und keine Zusage für jede Linux-Distribution.
Windows-Verifikation erfordert weiterhin den echten Zielrechner. Die
Installationsfunktion prüft jede heruntergeladene Laufzeit dort mit `--version`
und bietet bei Inkompatibilität den manuellen Import an.

## Wiederholbare Verifikation

```sh
curl -fL --retry 2 --max-time 180 \
  https://github.com/ggml-org/llama.cpp/releases/download/b12128/llama-b12128-bin-ubuntu-x64.tar.gz \
  -o /tmp/llama-b12128-linux.tar.gz
curl -fL --retry 2 --max-time 180 \
  https://github.com/ggml-org/llama.cpp/releases/download/b12128/llama-b12128-bin-win-cpu-x64.zip \
  -o /tmp/llama-b12128-windows.zip
sha256sum /tmp/llama-b12128-linux.tar.gz /tmp/llama-b12128-windows.zip
```

Die Downloads dieser Arbeitssitzung liegen unter den obigen temporären Pfaden;
der entpackte Linux-Server liegt unter
`/tmp/llama-pinned-smoke/llama-b12128/llama-server`. Diese Dateien werden nicht
versioniert oder mit der Anwendung ausgeliefert. Sie können für einen weiteren
Integrationstest wiederverwendet werden, solange die Arbeitsumgebung besteht.

## Vertrauens- und Fehlerverhalten

Nur explizite Installationsaktionen laden ausführbaren Code. Die UI oder ein
Modellauftrag kann keine Download-URL vorgeben. HTTPS ist vorgeschrieben;
höchstens drei Weiterleitungen zwischen `github.com` und
`release-assets.githubusercontent.com` sind erlaubt. Andere Ziele,
HTTP-Downgrades, URL-Zugangsdaten und abweichende Ports werden abgewiesen.

Größe und SHA-256 sind fest gebunden; ein unbekannter HTTP-Längenwert hebt die
Bytegrenze nicht auf. Transportfehler werden einmal wiederholt. Prüfsummen- und
Archivfehler werden nicht wiederholt. Die Anfrage besitzt Zeitgrenzen,
Verbindungs-/Header-Zeitgrenzen und einen Abbruchkontext. Der bestehende
Archivprüfer begrenzt die Entpackgröße und lehnt unsichere Pfade ab.

Vor der Nutzung erfolgen Entpacken in ein neues privates Verzeichnis und ein
zeitlich begrenzter Prozessstart mit `--version`. Fehler und Abbruch entfernen
die angelegten temporären Dateien. Erst der Aufrufer wählt die zurückgegebene
Laufzeit in den Einstellungen; vorhandene laufende Modellprozesse und deren
Bibliotheken werden dadurch nicht überschrieben. Die Prüfsumme schützt gegen
abweichende Bytes; sie ersetzt keine unabhängige Signatur des Herausgebers.

Bei einer Katalogaktualisierung müssen Quelle, Bytezahl, Digest, Ausführbarkeit
und Inferenzkompatibilität mit den unterstützten Modellparametern erneut geprüft
werden. GPU-Pakete brauchen eine getrennte Hardware- und Treiberabnahme.

## Automatisierte Prüfungen

`runtime_download_test.go` prüft Plattformauswahl, Quellen-/Redirectregeln,
offiziellen Asset-Redirect, Prüfsummenfehler, übergroße und abgeschnittene
Antworten, Antworten ohne Längenangabe, Archivfehler, Pfadtraversal, Abbruch,
Laufzeitprobe, Erhalt einer vorhandenen Laufzeit und genau einen Transportretry.
Die Archive sind kleine synthetische Fixtures; sie ersetzen die obige Prüfung
echter Veröffentlichungsartefakte nicht.

```sh
go test -race ./cmd/ki-basis -run 'TestRuntime(Recommendation|Download|Install|Network)' -count=1
GOOS=windows GOARCH=amd64 go build ./cmd/ki-basis
```
