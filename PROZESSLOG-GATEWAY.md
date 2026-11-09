# Prozesslog-Gateway: Fassung 2

Maßgeblich sind [ANFORDERUNGEN-V2.md](ANFORDERUNGEN-V2.md) und [V2-BETRIEB.md](V2-BETRIEB.md).

- Ein Slot ist unterstützt: acht FIFO-Warteplätze, 120 s Standardwartezeit. Keine vier Mindestslots.
- Erzeugungsbudget standardmäßig 900 s, Proxybudget Queue + Erzeugung + 60 s.
- X-User-ID optional, 1–64 Zeichen; `@` erlaubt, `:` verboten. Bearer und Modellrechte erforderlich.
- JSON-/Schema-Decoding wird der gepinnten llama.cpp-Engine übergeben. `length` und ungültige generierte JSON-Texte ergeben keine Ausgabe-5xx. Nicht konforme vollständige Ausgabe bleibt eine sichtbare Qualitätsabweichung.
- Denktext gehört in reasoning_content; explizite Legacywrapper werden getrennt, auch über SSE-Chunks hinweg.
- Bilder nur als Data-URL, höchstens ein Bild mit 8 MiB Rohdaten; Textmodell lokal 422 vision_not_supported.
- Capabilities: GET /v1/models/ALIAS/capabilities. Prozesslog sendet X-Priority:interactive im Chat, hält eine prozessübergreifende Zulassung und wertet finish_reason aus.
- Embeddings sind optional unter einem expliziten kind:embedding-Alias, keine Umwidmung des Chatmodells. Prozesslogs lokale Embeddings bleiben kompatibel.
- Modell-/Engine-/Projektor-/Templatehashes festhalten. Der verwaltete Start verlangt den Binaryhash; Änderungen über bewusste neue Einrichtung/Einstellungen.
- Modellwechsel/Stop benötigen angekündigtes Wartungsfenster und keine offene Importlease. Abgelaufene Importlease bleibt gesperrt bis explizite Freigabe.

Der isolierte Prozesslog-Änderungsbranch lautet `feat/gateway-contract-v2`, aufbauend auf `qualitaet/gateway-navigator-moe`. Es wurden keine Datenbanken oder laufenden Installationen umgestellt.
