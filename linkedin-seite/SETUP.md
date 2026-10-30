# LinkedIn-Seite einrichten — Stufe A

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
