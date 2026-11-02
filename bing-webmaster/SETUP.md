# Bing Webmaster und IndexNow einrichten

Für den Admin oder eine KI, die ihn führt. Die Anleitung deckt die Bing-Seite, den Eintrag des
Schlüssels, den optionalen IndexNow-Schlüssel und die Rechte des Nutzers ab. Der Dienst fragt
Bing Webmaster nach dem Indexstand einzelner Seiten und der Sitemap-Liste und meldet neue oder
geänderte Seiten und Sitemaps an. Er crawlt nichts, sucht keine Konten oder Seiten und entfernt
oder blockiert keine Adresse.

**Vorher erledigt der Betreiber auf der Anlage:** den Beisteller installieren, `config.json`
ablegen (Seite, erlaubte Hosts, optional der IndexNow-Schlüssel) und den Bridge-Key setzen. Der
Bridge-Key ist ein Zufallswert, den kein Mensch kennen muss. Ihn trägt der Admin nicht ein.

## 1. Bing-Seite

1. In Bing Webmaster Tools anmelden und die Website hinzufügen und verifizieren (alternativ aus
   der Google Search Console importieren). Die Adresse muss genau so eingetragen sein, wie sie
   der Betreiber als `site_url` in `config.json` angibt, zum Beispiel `https://www.example.test/`.
2. Oben rechts unter **Einstellungen** den Bereich **API-Zugriff** öffnen und **API-Schlüssel
   erzeugen**. Der Schlüssel erscheint einmal im Klartext.
3. Der Schlüssel gehört zum Bing-Konto, das ihn erzeugt hat. Was er erreicht, bestimmt dieses
   Konto: am besten ein Konto, das nur diese Website verwaltet. Der Beisteller selbst spricht
   ausschließlich die Seite aus `config.json` an.

## 2. Schlüssel eintragen

1. Den Schlüssel in der Admin-GUI unter **Basis-Einrichtung** bei der Stelle
   `plugin-bing-webmaster-bing` einfügen.
2. Den Schlüssel danach aus Zwischenablage und Notizen entfernen. Eine Kopie gehört höchstens in
   einen Passwortmanager, nie in eine Datei auf der Anlage.

Den Schlüssel trägt immer ein Mensch in der GUI ein. Über den Admin-MCP werden keine
Zugangsdaten eingegeben. Der Schlüssel steht nur in der ausgehenden Verbindung des Beistellers zu
`ssl.bing.com`, nie im Gateway, im Audit oder im Protokoll.

## 3. IndexNow (optional)

IndexNow meldet Seiten an `api.indexnow.org` und damit an die beteiligten Suchmaschinen. Der
IndexNow-Schlüssel ist per Definition öffentlich: Er liegt als Datei `<Schlüssel>.txt` im Root
der Website und steht deshalb in der `config.json` des Betreibers, nicht im Tresor. Die Datei
legt die Website-Betreuung an; ihr Inhalt ist genau der Schlüssel. Ist in `config.json` kein
IndexNow-Schlüssel eingetragen, antwortet die IndexNow-Route mit `not_configured`, und es
geschieht kein Aufruf. Ein Schlüsselwechsel gilt für alle Stellen, die mit diesem Schlüssel
melden, und ist dort gleichzeitig zu tauschen.

## 4. Rechte des Nutzers

Wer die Lesewerkzeuge nutzen soll, bekommt in der Admin-GUI unter **Users** beim eigenen Nutzer
den Scope `bing-webmaster`. Ein Team oder ein eigener Token ist dafür nicht nötig. Über den
Admin-MCP geht dasselbe mit `admin_set_user_scopes`. Das Werkzeug ersetzt die ganze Menge:
vorher mit `admin_list_users` die bestehenden Scopes des Nutzers lesen und mitgeben, sonst gehen
sie verloren.

Die Schreibwerkzeuge stehen in einer eigenen Vorlage, `bing-webmaster-manage`. Ein Scope auf die
ganze Integration gibt dort nichts Schreibendes. Jedes Schreibwerkzeug braucht sein eigenes
Recht, einzeln vergeben:

- `bing-webmaster-manage:submit_urls`
- `bing-webmaster-manage:submit_sitemap`
- `bing-webmaster-manage:indexnow_submit_urls`

Ein Schreibwerkzeug bleibt in Grenzen: höchstens 20 Aufrufe je Route und UTC-Tag (ein Neustart
des Beistellers setzt den Zähler zurück), höchstens 100 Adressen je Aufruf, nur https-Adressen
der erlaubten Hosts aus `config.json`, jedes mit Vorschau über `dry_run`. Die Vorschau prüft die
Eingabe und ruft Bing nicht auf. Vor einer Einreichung bei Bing liest der Dienst die gemeldete
Tagesquote und reicht nie mehr Adressen ein, als sie erlaubt.

## 5. Prüfen

Danach im KI-Client den Katalog neu einlesen und `submission_quota` aufrufen. Erwartet sind die
Zahlen für Tag und Monat. `bing_api_key_invalid` heißt, dass der Schlüssel in Schritt 2 fehlt, nicht
zur Website passt oder am Bing-Konto widerrufen wurde. `url_info` mit einer Adresse der Website
zeigt Entdeckung, letzten Crawl und HTTP-Status. Ein Schreibwerkzeug lässt sich gefahrlos mit
`dry_run` prüfen.

## Was die Werkzeuge nicht können

- Eine Einreichung meldet Adressen an. Sie garantiert keine Indexierung und keinen Zeitpunkt.
- **AI Performance** in Bing Webmaster Tools hat keine API. Diese Zahlen gibt es nur in der
  Oberfläche von Bing.
- Seitenstatistik und Suchbegriffe liefert dieser Dienst nicht; dafür steht `google-reporting`
  für Google-Zahlen.

## Schlüssel wechseln und entfernen

Wechsel jährlich und bei Verdacht sofort: in Bing Webmaster Tools unter **API-Zugriff** einen neuen
Schlüssel erzeugen, wie in Abschnitt 2 eintragen, `submission_quota` prüfen. Der Beisteller
startet nach dem Eintrag selbst neu.

**Entfernen ist kein Widerruf.** Wird der Wert in der GUI entfernt oder ersetzt, bleibt der
Schlüssel bei Bing gültig, bis er dort widerrufen ist.
