# LinkedIn-Seite 0.3.0 einrichten — Stufe A und Stufe B1

Diese Anleitung führt den Admin durch LinkedIn-Zustimmung und Tresoreintrag. Der Betreiber
installiert vorher den Beisteller, legt die beiden Tresor-Stellen an und setzt den Bridge-Key.
Die API-Formen und der Live-Zugriff müssen vor produktiver Freigabe geprüft werden.

## LinkedIn-App und Rolle

1. Eine zweckgebundene App unter https://www.linkedin.com/developers/apps anlegen und mit
   der eigenen Unternehmensseite verknüpfen. Den für die Seitenstatistik benötigten
   Community-Management-Zugang einschließlich Development Tier bei LinkedIn beantragen.
   Die zustimmende Person braucht die passende **Seiten-Admin-Rolle** dieser Organisation.
2. In der App exakt die Redirect-URI `http://localhost:8765/linkedin/callback` eintragen.
3. Genau die Scopes `rw_organization_admin r_organization_social` freigeben lassen.
   Der benötigte Admin-Scope hat bei LinkedIn keine Read-only-Alternative. Der Beisteller
   begrenzt den Zugriff deshalb auf feste GET-Routen; Posten gehört nicht zu Stufe A.
4. Client-ID und Client-Secret bereithalten. Organisations-ID/URN und eine unterstützte
   LinkedIn-Version (YYYYMM) an den Betreiber geben; diese Angaben kommen in dessen
   Konfiguration und sind keine Werkzeugparameter.

## Zustimmung am PO-Rechner und Tresor

Python 3.12 oder neuer am PO-Rechner verwenden. Das Skript aus dem geprüften Plugin-Paket
`linkedin-seite/tools/zustimmung.py` läuft **am Rechner des PO, nicht im Container**.
Es benötigt nur die Python-Standardbibliothek. Der Browser und Port 8765 müssen lokal
verfügbar sein. Die Betreiber-CLI muss für die vorgesehene Anlage eingerichtet sein.

```bash
python linkedin-seite/tools/zustimmung.py --client-id APP_CLIENT_ID | jnpt credential set --name plugin-linkedin-seite-oauth
```

Der Client-Secret-Prompt blendet Eingaben aus; das Secret niemals als Argument übergeben.
Alternativ `--secret-stdin` verwenden und den Wert aus dem Passwortmanager in stdin pipen.
Kein Klartext unter `/etc` oder `/opt`, keine Zwischenablage in Log, Chat oder Datei.
Das JSON geht ausschließlich auf stdout in die Pipe. Statusmeldungen gehen auf stderr.
Die Ausgabe auf stdout nie in eine Datei umleiten, nur direkt per Pipe an `jnpt credential set`.
Das Skript schreibt keine Dateien. Ein Terminal ohne sichere verdeckte Eingabe bricht ab;
dann stdin verwenden. Ausgabe nicht mit `tee` speichern.

**Windows:** In `cmd` fügt das Einfügen per Strg+V in den verdeckten Prompt ein Steuerzeichen
in das Secret ein. Das Skript erkennt das, meldet „Steuerzeichen“ und bricht ab, bevor etwas
an LinkedIn gesendet wird. Unter Windows deshalb **PowerShell 7** (`pwsh`) verwenden, das
Secret dort maskiert mit `Read-Host -MaskInput` einlesen (Windows PowerShell 5.1 kennt die
Option nicht) und per `--secret-stdin` übergeben. Eine Zeile, `APP_CLIENT_ID` ist die
Client-ID der App:

```powershell
Read-Host -MaskInput 'Client-Secret' | python linkedin-seite/tools/zustimmung.py --client-id APP_CLIENT_ID --secret-stdin | jnpt credential set --name plugin-linkedin-seite-oauth
```

Das Secret wird nur von `Read-Host` gelesen und direkt durchgereicht: nicht als Argument, nicht
in einer Variablen, nicht in einer Datei. Die Ausgabe geht auch hier ohne Umweg in `jnpt`.

Das Skript öffnet LinkedIn mit zufälligem `state` und lauscht auf beiden Loopback-Adressen
`127.0.0.1:8765` und `[::1]:8765`, niemals auf Wildcard-Adressen. Die eingetragene Redirect-URI
bleibt `http://localhost:8765/linkedin/callback`. Gemeinsam wird genau eine Anfrage behandelt;
die erste Anfrage schließt danach beide Listener, auch bei falschem State oder Pfad.
Ungültige Anfragen werden abgewiesen, danach erneut starten. Warte-/Gesamtfrist fünf Minuten.
Fehlt IPv6 (EAFNOSUPPORT/EADDRNOTAVAIL/EPROTONOSUPPORT), bleibt IPv4 aktiv und stderr zeigt
einen Hinweis. Ist `[::1]:8765` belegt (EADDRINUSE), bricht das Skript vor Browserstart mit
„IPv6-Port [::1]:8765 ist belegt; Zustimmung abgebrochen.“ ab. Auch andere Bindefehler
brechen ab. Unter Windows gilt exklusive Socket-Bindung. Nach Timeout erneut starten.
PKCE nur nach belegter S256-Unterstützung dieser LinkedIn-App mit `--pkce`
einschalten; ohne diesen Beleg gilt der vertrauliche Client-Code-Tausch ohne PKCE.
Anschließend meldet stderr **„Refresh-Token: ja“** oder **„Refresh-Token: nein“**.
LinkedIn liefert die gewährten Scopes kommagetrennt und die Laufzeiten gelegentlich knapp
über dem Höchstwert (beobachtet: Refresh-Token 59 Sekunden über 365 Tagen). Das Skript und
der Dienst akzeptieren Komma oder Leerraum als Trenner, verlangen aber genau die zwei
gepinnten Scopes, und deckeln eine Überschreitung bis zu einem Tag auf 60 bzw. 365 Tage.
Alles darüber hinaus wird als unerwartete Antwort abgewiesen; die Frist wird nie verlängert.

Die Tresor-Stelle `plugin-linkedin-seite-oauth` enthält JSON mit `client_id`, `client_secret`,
`access_token`, `access_expires_at` und, falls LinkedIn sie liefert, gemeinsam
`refresh_token`, `refresh_expires_at`. Ablaufwerte sind ISO-8601-Zeitstempel mit Zeitzone.
Der Dienst bekommt ausschließlich eine RAM-Datei im read-only Mount; der Betreiber muss
die Path-Unit für den automatischen Neustart nach Schlüsselwechsel eingerichtet haben.
Der zweite Tresorplatz `plugin-linkedin-seite` enthält den vom Betreiber erzeugten Bridge-Key.

## Prüfen und Rechte vergeben

Der Betreiber prüft lokale Gesundheit und einen kontrollierten echten Bericht. Die
synthetischen Tests beweisen keinen LinkedIn-Grant. Erst nach Live-Abnahme stellt das
Produkt die Connector-Vorlage und Werkzeugrechte bereit. Dem Nutzer nur die freigegebenen
Lesewerkzeuge geben; bestehende Nutzerrechte beim Ändern erhalten.
`unexpected_source_shape` bedeutet einen unbelegten/geänderten Herstellervertrag, nicht null.
Der Follower-Gesamtwert kommt aus dem fünften festen GET-Endpunkt `networkSizes`
(`firstDegreeSize`, `edgeType=COMPANY_FOLLOWED_BY_MEMBER`), nur für die konfigurierte Organisation.
Follower-Zuwachs bezeichnet Zugänge; der Zeitraum endet spätestens heute minus zwei UTC-Tage.
Die Siebentage-Vorgabe berücksichtigt diese Verzögerung, ein später verlangtes Ende ergibt
`end_date_not_available`. Seitenstatistiken enden spätestens gestern.
Nur gültige leere Share-Antworten auf genau eine angefragte Beitrags-ID erhalten fünf
Nullzähler und `ohne_interaktion: true`. `paging` mit fehlenden oder leeren `links` ist gültig;
ein `rel=next`-Link, unbekannte Paging-Formen, fremde Organisation/ID oder eine
zweite Zeile ergeben `unexpected_source_shape` (502). Die leere Liste ist allein durch
den festen Einzel-GET der konfigurierten Organisation zugeordnet; Metadaten dürfen nicht widersprechen.
Nur negative `likeCount`-Summen werden auf 0 korrigiert und mit `korrigiert: true` am
Beitrag gekennzeichnet: erst Tageswerte summieren, dann kappen. Alle anderen negativen
Zähler einschließlich Follower/Seitenaufrufe bleiben Formfehler. Bei Personenbezug irgendwo
im gesamten Beitragsobjekt entfällt der Textanfang (`text: null`,
`text_ausgelassen: "erwaehnung"`); der übrige Beitrag bleibt erhalten. Diese Herstellerbefunde
2026-10 sind synthetisch geprüft und weiterhin live zu belegen.
Der Tagesdeckel beträgt 50 einzelne Aufrufe einschließlich Fehlschlägen und Refresh pro
UTC-Tag im Serverprozess; ein Neustart setzt ihn zurück. Ein Bericht kann mehrere verbrauchen.

## Erneuern und widerrufen

Access-Tokens gelten höchstens **60 Tage**. Ohne Refresh die Zustimmung spätestens alle
60 Tage erneut ausführen und JSON in derselben Tresor-Stelle ersetzen. Ab **14 Tagen**
vor Ablauf ergänzt der Dienst Antworten um `warnung: token_laeuft_ab` und das Ablaufdatum
(nur Datum), nach Ablauf kommt `token_abgelaufen`.

Ein optionales Refresh-Token gilt höchstens **365 Tage ab Erstzustimmung**, die Frist
verlängert sich nicht. Der Dienst erneuert Access vor Ablauf beim nächsten Abruf selbst,
das Ergebnis bleibt nur im RAM. Er kann nicht in den Tresor zurückschreiben. Deshalb
jährlich vor Ablauf erneut zustimmen; ab 14 Tagen vorher warnt jede Anwendungsantwort
mit dem Refresh-Ablaufdatum. Kill/Neustart verliert erneuerte RAM-Tokens; falls LinkedIn
das alte Refresh-Token dabei widerruft, erneut zustimmen. Ein Access-Token bleibt bis zu
seinem eigenen Ablauf nutzbar. Kürzere Herstellerfristen werden eingehalten.

Bei Verdacht sofort die Autorisierung bei LinkedIn widerrufen und neu zustimmen.
Entfernen aus dem Tresor ist kein Widerruf beim Hersteller. Eine etwaige Sicherung gehört
höchstens in den Passwortmanager. Niemals Tokens in ein Supportticket kopieren.

## Stufe B1: Posten nach Freigabe

Ab Version 0.3.0 (#1306 F4) kann der Beisteller auf der eigenen Unternehmensseite posten, bearbeiten, löschen
und eigene Kommentare setzen — nur für Einträge des Redaktionsplans, die ein Mensch freigegeben hat.
Dafür gibt es eine **zweite, eigene Zustimmung** mit genau den Scopes `w_organization_social`
(Posten, Bearbeiten, Löschen, Medien) und `w_organization_social_feed` (eigene Kommentare). Der
Lese-Zugang aus Stufe A bleibt unverändert; Lese- und Schreib-Token sind getrennte Tresor-Stellen
und werden nie getauscht. Ohne diese Zustimmung bleibt Posten aus.

### Schreib-Zustimmung

1. In der LinkedIn-App prüfen, dass das Produkt „Community Management API“ die Scopes
   `w_organization_social` und `w_organization_social_feed` freigibt. Die zustimmende Person braucht auf der Seite die Rolle
   Administrator (oder Content-Admin). Die Redirect-URI bleibt dieselbe.
2. Am PO-Rechner das Zustimmungsskript im Schreib-Modus starten. Es fordert **nur**
   `w_organization_social` und `w_organization_social_feed` an und schreibt das JSON in eine **andere**
   Stelle als Stufe A:

```bash
python linkedin-seite/tools/zustimmung.py --client-id APP_CLIENT_ID --schreiben | jnpt credential set --name plugin-linkedin-seite-post-oauth
```

Unter Windows wie oben mit PowerShell 7 und maskierter Eingabe:

```powershell
Read-Host -MaskInput 'Client-Secret' | python linkedin-seite/tools/zustimmung.py --client-id APP_CLIENT_ID --secret-stdin --schreiben | jnpt credential set --name plugin-linkedin-seite-post-oauth
```

stderr nennt die Ziel-Stelle `plugin-linkedin-seite-post-oauth`. Das JSON trägt zusätzlich das Feld
`scope`; der Dienst weist es in der Lese-Stelle ab und verlangt es in der Schreib-Stelle. Wer die
Pipe vertauscht, sieht das beim nächsten Start: Stufe A startet nicht (falsches JSON in der
Lese-Stelle) bzw. das Schreiben bleibt aus (`schreiben_nicht_eingerichtet`). Die Ausgabe nie in eine
Datei umleiten. Laufzeit und Erneuerung wie beim Lese-Token (60 Tage, Refresh höchstens 365 Tage).

### Gateway-Zugang des Beistellers

Der Beisteller liest Redaktionsplan und Medien selbst über das Gateway, im internen Netz
`jnpt_plugins` unter **`http://jnpt:8484/mcp`** (Eintrag `schreiben.gateway_url` in der
Betreiber-Konfiguration). Erlaubt ist nur `/mcp`, nie `/admin/mcp`; jede andere Adresse verhindert
den Start. Dafür bekommt er einen eigenen Team-Token in der Stelle `plugin-linkedin-seite-gateway`
mit **genau** diesen zwei Werkzeugen und sonst nichts: `redaktionsplan_get` und
`redaktion_medien_get_file`. Frühere Freigabe-Suchrechte entfernen; auch den alten
Konfigurationsschlüssel `schreiben.freigabe` entfernen (wird beim Start abgewiesen).

### Freigeben

Die KI legt Inhalt und Medien im Redaktionsplan bzw. in der Medien-Ablage ab.
Die unveränderlichen Freigabe-Aufträge und ihre menschliche Entscheidung verwaltet der Kern.

1. `linkedin_seite_vorschau` mit Kartei-ID und Aktion (`posten`, `bearbeiten`, `loeschen`,
   `kommentar`) aufrufen. Anzeige, direkte Wirkungsfelder `aktion`, `ziel_urn`, `text`, `medien` und `umfrage`
   sowie `hash` stammen aus demselben einmal gelesenen Eintrag und denselben geprüften Dateien.
   Medien enthalten `name`, `sha256`, `datei`, `art`, `bytes`; bei PDF ist name der Dokumenttitel.
   Weitere Antwortfelder: eintrag_id, format, post_text, post_urn, kommentar_text, dokument_titel.
   Es wird kein zusätzliches Wirkungsobjekt gesendet. Nur die vier von F2 erwarteten
   Ablaufhinweise warnung, ablaufdatum, warnung_schreiben, ablaufdatum_schreiben sind optional.
   Der Kern holt die Vorschau selbst und berechnet den Freigabehash daraus neu.
2. Dateien ansehen und im Kern den Auftrag prüfen und freigeben. Der Hash bindet Aktion,
   Ziel-URN, exakten Text, geordnete Mediennamen/Dokumenttitel mit Datei-SHA-256 sowie
   Umfragefrage, jede Antwort samt Reihenfolge und ganzzahlige Dauer.
3. Der Kern startet den freigegebenen Auftrag und sendet genau einen Header:
   `X-Jnpt-Freigabe: hash=<freigabe:v2:sha256:…>`, ohne Auftrags-ID. Der Beisteller berechnet den aktuellen
   Hash selbst. Ohne exakte Gleichheit, auch ohne Header, kommt 409 nicht_freigegeben
   vor jedem LinkedIn-Schreibaufruf. Auch Löschen braucht diesen Header.
4. Bestätigter Erfolg enthält `ausgefuehrt: true`; bewiesener Abbruch vor dem ersten
   LinkedIn-Schreibversuch `nicht_ausgefuehrt: true`. Fehler danach enthalten keines
   der Felder, bereits ab Upload-Start, auch bei Edit/Delete. Der Kern wertet dies als
   unklar und hält den Sperrvermerk. Der RAM-Ergebnis-Cache (`report_hash`) im Beisteller
   ist Komfort und kein Sperrvermerk; ein Neustart leert ihn. Wiederholung desselben Hashs
   liefert wiederholt: true zusammen mit ausgefuehrt: true und erzeugt keinen zweiten Post.

Hash v2: Präfix `freigabe:v2:sha256:` + SHA-256 über UTF-8 `freigabe:v2`, LF und kompaktes
JSON mit Feldfolge aktion, medien, text, umfrage, ziel_urn. Medien geordnet {name, sha256};
Umfrage null oder {antworten, dauer, frage}. Kein HTML-Escaping, keine Unicode-Normalisierung.
Beim Posten ist ziel_urn die konfigurierte Organisation, sonst post_urn; text ist der genaue
Post-/Kommentartext, bei Löschen leer. Medien/Umfrage nur beim Posten. Vorschau enthält keine
Freigabeentscheidung. HTTP wartet auf das Endergebnis, maximal 25 Minuten im Beisteller. F2 begrenzt den Call aktuell auf
25 Sekunden (Runtime-Decke 120 Sekunden). Lange Videojobs können deshalb im Kern schon vorher
unklar werden; eine bloße Spec-Anhebung auf 25 Minuten überwindet die Runtime-Decke nicht. Gemeinsame Live-Abnahme mit dem Kern bleibt Aufgabe von GATE.

Es gelten höchstens **5 schreibende Aktionen** je UTC-Tag, über Posten, Bearbeiten, Löschen und
Kommentieren zusammen; ein Video zählt ab dem Upload-Start als eine Aktion. Eine Umfrage lässt sich
nach dem Posten nicht mehr ändern; die Admins der Seite sehen bei LinkedIn, wer wie abgestimmt hat,
der Beisteller liest keine Stimmen. Ein Post-Entwurf (DRAFT) über die Schnittstelle ist bei LinkedIn
nicht möglich. Rückbau: die Schreib-Autorisierung bei LinkedIn widerrufen und die Stelle
`plugin-linkedin-seite-post-oauth` leeren; das Entfernen aus dem Tresor allein ist kein Widerruf.
