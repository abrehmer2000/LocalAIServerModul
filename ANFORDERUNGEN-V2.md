# Verbindlicher Abnahmevertrag – Fassung 2

Hier ist die vollständige Liste (Fassung 2, nach der Prüfung durch den Gutachter).

Legende: MUSS = ohne das ist der Betrieb nicht zuverlässig. SOLL = deutlich besser, Ersatz möglich. KANN = nützlich, nicht nötig.

Fehlercodes (gelten für F-30, F-31, N-32)
error.code	Status	Prozesslog wiederholt?	Bedeutung
model_busy	429/503	ja	alle Slots belegt und Schlange voll oder Wartezeit abgelaufen
model_loading	503	ja	Modell wird geladen
model_unavailable	503	ja	Engine vorübergehend weg, auch beim Beenden
upstream_failure	502	ja	Engine gestört (nie für Eingabefehler, Bilder, Modellausgabe)
timeout	504	ja	Zeit abgelaufen vor Beginn der Erzeugung oder in der Engine
context_length_exceeded	400/413	nein	Eingabe zu lang
vision_not_supported	400/415/422	nein	Modell kann keine Bilder
invalid_request	400/422	nein	Format falsch
unauthorized / forbidden	401 / 403	nein	Zugang
model_not_found	404	nein	unbekannter Alias
client_disconnect	nur Audit	–	Client hat getrennt
Funktionale Anforderungen
Protokoll und Anmeldung

ID	Prio	Anforderung	Prüfung (bestanden, wenn …)
F-01	MUSS	POST /v1/chat/completions, OpenAI-Format, nicht streamend. content ist ein UTF-8-String. Für die Abnahme zusätzlich: finish_reason, usage.prompt_tokens, usage.completion_tokens, model, id.	alle Felder vorhanden
F-02	MUSS	Bearer-Anmeldung. Ohne oder mit falschem Schlüssel 401, ohne Modellrecht 403. Der Schlüssel steht nie in Antworten, Fehlern oder Logs.	401/403 stimmen, Schlüssel in Log und Audit nicht auffindbar
F-03	MUSS	X-User-ID ([A-Za-z0-9][A-Za-z0-9._@-]{0,63}) wird angenommen und im Audit benutzt. Fehlt er, geht die Anfrage durch. Unzulässiger Wert: 400, nie 5xx.	mit, ohne, mit führendem - senden
F-04	MUSS	Nie eine Weiterleitung (3xx).	…/chat/completions/ und …/../chat/completions: kein 3xx
F-05	MUSS	Stabiler Modell-Alias. Unbekannter Alias: 404/400, nie 5xx.	unbekanntes Modell senden
F-06	MUSS	chat_template_kwargs wird angenommen. Andere unbekannte Felder: ignorieren oder 400, nie 5xx.	mit erfundenem Feld senden
F-07	SOLL	GET /v1/models listet die Aliase.	Aufruf mit Schlüssel
Inhalt der Antworten

ID	Prio	Anforderung	Prüfung
F-08	MUSS	Ohne max_tokens begrenzt nur der freie Kontext die Ausgabe, mindestens 8.192 Token. Ein kleinerer Vorgabewert ist verboten oder dokumentiert und ergibt finish_reason: length.	Anfrage ohne max_tokens mit etwa 3.000 Token Ausgabe: vollständig, stop
F-10	MUSS	temperature und max_tokens werden eingehalten, bei Grenze finish_reason: length.	max_tokens: 5: kurz, length
F-11	MUSS	Mit json_object erzwingt das Gateway gültiges JSON (z. B. Grammatik), ohne Zaun und ohne Text davor oder danach. Bei Kürzung 200, length, Ausgabe unverändert. Für Modellausgabe nie ein 5xx.	20 Anfragen: mindestens 19 parsebar, kein 5xx
F-12	SOLL	json_schema wird unterstützt. Ungültiges Schema: 400. Wie F-11 kein 5xx für Ausgabe.	Anfrage mit Schema
F-13	MUSS	enable_thinking wirkt je Aufruf. Denktext nie in content (auch nicht als <think>), sondern in getrenntem Feld oder gar nicht.	false: completion_tokens unter 50 % des Werts bei true. Bei beiden beginnt content nicht mit <think>.
F-14	SOLL	Leere content nur, wenn das Modell nichts ausgibt. Verbraucht das Denken das Budget: length.	kleines max_tokens mit Denken: length, kein stilles leeres stop
F-15	MUSS	Nie stilles Abschneiden. Zu lange Eingabe: 400/413 context_length_exceeded.	Prompt über dem Fenster: 4xx mit Code, kein verkürztes 200
F-16	MUSS	Kontext je Anfrage mindestens 32.768 Token, dokumentiert.	Prompt mit 20.000 Token wird beantwortet
Bilder

ID	Prio	Anforderung	Prüfung
F-20	SOLL	Ein Bild je Anfrage als Data-URL (PNG/JPEG/WebP/GIF, roh bis 8 MiB, Anfrage bis 16 MiB) wird ausgewertet, sofern das Modell Bilder kann.	Probebild
F-21	MUSS	Kann das Modell keine Bilder: sofort 400/415/422 vision_not_supported, nie 5xx (Prozesslog würde 3-mal wiederholen und dann „ausgelastet“ melden).	Bild an Modell ohne Bildfähigkeit: 4xx unter 1 s
F-22	SOLL	Bildfähigkeit je Modell abfragbar (siehe F-41).	Status zeigt vision
F-23	MUSS	Bilder werden nie gespeichert oder aus Fremdadressen geladen.	https://-Bild-URL: 400
Fehler, Überlast, Warteschlange

ID	Prio	Anforderung	Prüfung
F-30	MUSS	Dauerhafter Fehler nie als 5xx, Zuordnung nach Codetabelle. 504 nur vor Beginn der Erzeugung oder in der Engine. Die Erzeugungsgrenze liegt über der längsten Clientgrenze (N-13).	Fehlerarten gezielt auslösen, Status und Code mit der Tabelle vergleichen
F-31	MUSS	Fehlerform {"error":{"code","message"}}. Die Meldung enthält nie Inhalt der Anfrage oder Antwort.	Fehler auslösen, Text auf Inhalt prüfen
F-31a	SOLL	Die Codes entsprechen der Codetabelle.	Codes vergleichen
F-32	MUSS	Genaue Belegung: Abweisung nur, wenn alle Slots wirklich belegt sind. Nach einer ausgelieferten Antwort ist der Slot sofort frei (unter 250 ms), nicht erst nach einem verspäteten Meldezustand.	50 Anfragen nacheinander ohne Pause: 0 Abweisungen
F-33	MUSS	Retry-After bei model_busy, model_loading und model_unavailable immer gesetzt: Ganzzahl 1 bis 30, aus dem aktuellen Zustand (höchstens 1 s alt).	bei belegtem Slot: Kopf vorhanden, 1 bis 30
F-34	MUSS	Warteschlange: Bei belegtem Slot wartet die Anfrage der Reihe nach (Wartezeit einstellbar, Vorgabe 120 s, mindestens 8 Plätze). Danach oder bei voller Schlange 503 model_busy mit Retry-After. Eine wartende Anfrage ohne Client wird entfernt.	2 gleichzeitige: beide beantwortet, kein 503. 10 gleichzeitige: erste bedient, überzählige model_busy. Wartende trennen: wird nie ausgeführt.
F-35	MUSS	Abbruch: Bei Trennung oder Zeitablauf des Clients bricht das Gateway die Erzeugung ab und gibt den Slot nach höchstens 5 s frei.	lange Anfrage, nach 3 s trennen, dann max_tokens:1: 200 binnen 5 s plus Rechenzeit
F-36	SOLL	Optionaler Kopf X-Priority (z. B. interactive) ordnet die Schlange. Fehlt er: mittlere Stufe. Wirkt erst mit P-3.	lange Anfrage laufen lassen, eine ohne Kopf und eine interactive anstellen: die interaktive zuerst
F-37	MUSS	Beim Laden des Modells 503 model_loading mit Retry-After, nicht 502.	Engine neu starten, währenddessen anfragen
Zustand und Betrieb

ID	Prio	Anforderung	Prüfung
F-40	SOLL	Health-Aufruf ohne Modellaufruf in unter 1 s: bereit, Modell geladen, Slots gesamt/frei, Schlangenlänge. Wirkt erst mit P-4.	im Leerlauf und unter Last unter 1 s
F-41	SOLL	Abfragbar: Kontext je Slot, Slotzahl, Bildfähigkeit, Modellrevision, höchste Ausgabelänge.	Werte mit Engine-Konfiguration vergleichen
F-42	SOLL	Antwortkopf X-Request-ID = Audit-Kennung. Ein Client-Wert (bis 32 Zeichen [A-Za-z0-9-]) wird übernommen.	im Audit wiederfinden
F-43	KANN	POST /v1/embeddings	–
F-44	KANN	Streaming	–
Nichtfunktionale Anforderungen
ID	Prio	Anforderung	Prüfung
N-01	MUSS	Dauerbetrieb ohne Anmeldung: Gateway, Agent und Engine starten beim Hochfahren als Dienst oder Aufgabe und starten nach Absturz selbst neu. Nichts hängt an einer interaktiven Sitzung.	Server neu starten, nicht anmelden: nach höchstens 10 min antwortet F-01. Engine beenden: nach höchstens 2 min wieder bereit.
N-02	MUSS	Im Nachtfenster 20:00 bis 06:00 keine Ausfälle außer angekündigter Wartung; keine Wartung während eines Imports.	24-h-Probe (N-12)
N-03	MUSS	Beim Beenden: keine neuen Anfragen (503 model_unavailable + Retry-After), laufende fertig oder 503. Nie ein Abbruch ohne Status.	Neustart während einer 60-s-Anfrage: 200 oder 503, kein Abbruch
N-04	SOLL	Schlüsselwechsel ohne Ausfall, mehrere Schlüssel je Programm.	zweiten Schlüssel anlegen, alten sperren
N-10	SOLL	Eigener Aufwand des Gateways höchstens 100 ms im Median, 500 ms (p99) gegenüber direktem Engine-Aufruf.	100 Anfragen max_tokens:1 über Gateway und direkt: Differenz der Mediane ≤ 100 ms, p99 ≤ 500 ms
N-12	MUSS	24-h-Dauertest mit festem Profil (nacheinander, 10 % zusätzlich gleichzeitig, 2 % abgebrochen): keine wachsende Schlange, keine übrig bleibenden Kindprozesse, Speicher (RSS) nach 1 h Aufwärmen höchstens +10 %, Fehlerrate durch das Gateway unter 0,5 %.	Lastskript 24 h
N-13	MUSS	Erzeugungsgrenze einstellbar, mindestens 600 s und mindestens längste Clientgrenze (420 s) plus Schlangenwartezeit. Bei Ablauf 504 timeout, Slot frei.	Wert ablesen. Anfrage mit etwa 450 s Erzeugung (Client-Grenze 700 s): 200
N-20	MUSS	Keine Verfälschung von Ein- oder Ausgabe (Kürzung, Zeichensatz, Umordnung). JSON mit \uXXXX wird korrekt dekodiert, Antwort ist UTF-8.	Umlaute und „“ – zitieren lassen: unverändert
N-24	SOLL	Bei temperature: 0 wiederholbar.	5 gleiche Anfragen: mindestens 4 gleiche Antworten
N-21	MUSS	Kein Antwort-Cache. Ein Präfix-/KV-Cache im Arbeitsspeicher ist erlaubt, aber nie auf Platte (--slot-save-path aus) und nie zwischen Nutzern geteilt.	Engine-Konfiguration lesen. Zwei Anfragen mit gleichem Anfang: keine Vermischung
N-23	MUSS	Modellrevision festgeschrieben. Änderungen sind bewusst und stehen im Audit je Start.	Revision im Audit vor/nach Neustart
N-30	MUSS	Nur TLS ≥ 1.2, Zertifikat passt zum Hostnamen, eigene CA möglich. HTTP höchstens auf Loopback.	openssl s_client
N-31	MUSS	Keine Inhalte (Prompt, Antwort, Bild, Denktext) in Audit, Gateway- und Engine-Logs (auch Debug), auf Platte, in Absturzabbildern. Auslagerung verschlüsselt oder aus. Im Audit nur Metadaten.	Testsatz senden, dann Audit, Logs, Datenordner durchsuchen: kein Treffer. Konfiguration von Abbildern und Auslagerung vorzeigen.
N-32	MUSS	Audit ergänzungsgeschützt (verkettet/signiert), Fehler nach error.code ausgewiesen.	Audit nach Codes auswerten
N-33	MUSS	Begrenzungen: Anfrage ≥ 16 MiB (einstellbar), Schema 64 KiB, Zahl gleichzeitiger Verbindungen. Überschreitung 413/429, kein Absturz.	zu große Anfrage, 200 gleichzeitige Verbindungen
N-34	MUSS	Lauscht nur auf festgelegten Adressen. Verwaltung mit dem Modellschlüssel nicht erreichbar.	Verwaltung mit Prozesslog-Schlüssel: 401/403
N-40	SOLL	Kennzahlen abrufbar: Anfragen nach Status/Code, Dauerverteilung, Schlangenlänge, belegte Slots, Tokens, Engine-Neustarts.	Zähler vor/nach Testreihe
N-41	SOLL	Gateway-Log mit Zeit, Kennung, Code, ohne Inhalt. Rotation ≤ 100 MB × 7.	Log ansehen
N-42	SOLL	Abnahmeskript oder Anleitung für die MUSS-Punkte.	Skript laufen lassen
Entfallen (Nummern bleiben frei): N-11 (in F-32 enthalten), N-14 (in N-10), N-22 (ohne Kriterium).

Anteil von Prozesslog selbst (nicht Gateway)
ID	Maßnahme
P-1	Gateway-Aufrufe durch die Modellzulassung führen (ein Platz, Vorrang Chat), wenn das Gateway nur einen Slot hat.
P-2	Bei model_busy bis etwa 45 s warten statt nach 3 Versuchen aufzugeben.
P-3	X-Priority senden (Chat: interactive).
P-4	Vor Bildaufträgen die Bildfähigkeit abfragen.
P-5	finish_reason auswerten, damit gekappte Antworten nicht als vollständig gelten.
Abnahme und Stand von KI-Basis
Bereit für den Kundentest ist das Gateway, wenn alle MUSS-Punkte bestehen. Reihenfolge bei knapper Zeit: F-32/F-34, dann F-30/F-21/F-15/F-11, dann F-35, dann N-01, dann N-31.

Gemessener Stand von KI-Basis (Audit 5. bis 9.11., 9.804 Ereignisse):

Anforderung	Stand
F-32 nacheinander ohne Abweisung	nicht erfüllt. 3.357 von 3.440 Abweisungen (97,6 %) kamen bei freier Engine, im Median 2,1 s und höchstens 5,5 s nach der vorigen Antwort.
F-33 Retry-After	nicht erfüllt. 1 s genannt, Belegung dauert bis 5,5 s.
F-34 Warteschlange	nicht vorhanden.
F-20 Bilder	nicht anwendbar: Engine meldet vision: false, 1 Slot.
F-21/F-30 Statussemantik	nicht erfüllt. Bildanfrage endet sofort in 502.
F-16 Kontext	erfüllt (32.768).
F-01/F-11 Text und JSON	im Betrieb benutzbar (1.364 Erfolge), Felder und JSON-Reinheit nicht geprüft.
N-01 Dienst ohne Anmeldung	teilweise. Der Agent läuft in einer Administrator-Sitzung.
N-13 Erzeugungsgrenze	zu klären. 900 s eingestellt, Dokumentation nennt 300 s als Vorgabe.
F-35 Abbruch	nicht geprüft. 163 Abbrüche nach 3 bis 6 s kommen wahrscheinlich vom Navigator (Restbudget ab 1 s), nicht vom Gateway.
N-31/N-32	teilweise. Audit ohne Inhalt und mit Hash-Kette, Ursachen nur als model_busy.
Zwei Hinweise zum Testen: tools/gateway_pruefen.py liest nur die .env, ein 401 dort kann ein Messfehler sein. Die Aussagen zu llama.cpp und Ollama stammen aus allgemeinem Wissen und nicht aus einem Test auf dem Server.