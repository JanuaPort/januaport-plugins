# Google Reporting einrichten

Für den Admin oder eine KI, die ihn führt. Die Anleitung deckt die Google-Seite, den Eintrag
des Schlüssels und die Rechte des Nutzers ab. Die Rechte bei Google sind die Obergrenze, die die
Sicherheitsprüfung verlangt; großzügiger einrichten ist nicht vorgesehen. Sie bestimmen auch, was
ein abgeflossener Schlüssel kann: Die Freigaben im Gateway gelten nur für den Weg über das Gateway.

**Vorher erledigt der Betreiber auf der Anlage:** den Beisteller installieren, `config.json`
ablegen und den Bridge-Key setzen. Der Bridge-Key ist ein Zufallswert, den kein Mensch kennen
muss. Ihn trägt der Admin nicht ein.

## 1. Google-Seite

1. In der Google Cloud Console ein **neues** Projekt nur für diesen Zweck anlegen, kein
   vorhandenes wiederverwenden.
2. Darin genau drei APIs aktivieren: **Google Search Console API**,
   **Google Analytics Data API** und **Google Analytics Admin API**.
3. Ein Dienstkonto anlegen und ihm **keine Rolle** im Projekt geben. Unter „Schlüssel“ genau
   einen JSON-Schlüssel erzeugen und herunterladen.
4. In der Search Console die E-Mail-Adresse des Dienstkontos als Nutzer der Property hinzufügen,
   mit Berechtigung **„Uneingeschränkt“**, nie „Inhaber“. „Uneingeschränkt“ braucht das
   Einreichen einer Sitemap; die Berichte lesen auch damit nur.
5. In Google Analytics unter Verwaltung → Property-Zugriff dieselbe Adresse als **„Bearbeiter“**
   auf genau dieser einen Property hinzufügen, nie „Administrator“. „Bearbeiter“ braucht das
   Anlegen und Ändern von Annotationen und der Kanalgruppe. Bei den Datenbeschränkungen
   **„Keine Kosten- und Umsatzmesswerte“** anhaken.
6. Vier Angaben an den Betreiber geben, der sie in `config.json` einträgt:
   - die **GA4-Property-ID**, nur die Ziffern ohne `properties/`,
   - den **GSC-Property-Typ** und die Adresse, also `sc-domain:<domain>` für eine
     Domain-Property oder die URL-Präfix-Adresse,
   - die **Zeitzone** der GA4-Property (IANA, z. B. `Europe/Berlin`),
   - optional den **Namen der Kanalgruppe** für KI-Assistenten (Vorgabe „KI-Assistenten“).

## 2. Schlüssel eintragen

1. Den Inhalt der JSON-Datei in der Admin-GUI unter **Basis-Einrichtung** bei der Stelle
   `plugin-google-reporting-google` einfügen. Dass die Zeilenumbrüche beim Einfügen verloren
   gehen, ist unschädlich.
2. Die heruntergeladene Datei danach lokal löschen. Eine Kopie gehört höchstens in einen
   Passwortmanager, nie auf die Anlage.

Den Schlüssel trägt immer ein Mensch in der GUI ein. Über den Admin-MCP werden keine
Zugangsdaten eingegeben.

## 3. Rechte des Nutzers

Wer die Berichte abrufen soll, bekommt in der Admin-GUI unter **Users** beim eigenen Nutzer
die Scopes `google-search-console` und `google-analytics`. Ein Team oder ein eigener Token ist
dafür nicht nötig. Über den Admin-MCP geht dasselbe mit `admin_set_user_scopes`. Das Werkzeug
ersetzt die ganze Menge: vorher mit `admin_list_users` die bestehenden Scopes des Nutzers lesen
und mitgeben, sonst gehen sie verloren.

Die Verwaltungswerkzeuge stehen in zwei eigenen Vorlagen, `google-search-console-manage` und
`google-analytics-manage`. Ein Scope auf die ganze Integration gibt dort nur die Lesewerkzeuge
(Sitemaps und Annotationen lesen, URL prüfen, Kanalgruppen lesen). Jedes Schreibwerkzeug braucht
sein eigenes Recht, einzeln vergeben:

- `google-search-console-manage:submit_sitemap`
- `google-analytics-manage:create_annotation`
- `google-analytics-manage:update_annotation`
- `google-analytics-manage:create_channel_group`
- `google-analytics-manage:update_channel_group`

Gelöscht oder archiviert wird über diese Werkzeuge nichts. Ein Schreibwerkzeug bleibt in Grenzen:
höchstens 20 Schreibversuche je Quelle, Site und Tag (ein Neustart des Beistellers setzt den
Zähler zurück), jedes mit Vorschau über `dry_run`. Den Stand vor und nach jeder Änderung zeigt die
Antwort, die Änderungszeile steht im Protokoll des Beistellers.

## 4. Prüfen

Danach im KI-Client den Katalog neu einlesen und je Quelle `get_status` aufrufen. Erwartet ist
`ready`. `access_denied` heißt meist, dass Schritt 1.4 oder 1.5 fehlt oder auf der falschen
Property liegt. Für die Verwaltungsvorlagen genügt `list_sitemaps` bzw. `list_annotations`; ein
Schreibwerkzeug lässt sich gefahrlos mit `dry_run` prüfen.

## Mehrere Sites

Der Beisteller kann mehrere Websites bedienen, jede mit eigener Property. Der Betreiber führt
sie in `config.json` unter `sites` mit einem Kurznamen auf (Kleinbuchstaben, Ziffern und
Bindestrich, zum Beispiel `terminagent`). Für jede Site legt der Admin **eigene Instanzen** der
vier Vorlagen an (`google-search-console`, `google-analytics`, `google-search-console-manage`,
`google-analytics-manage`):

1. Beim Anlegen ist `site` ein Pflichtwert: genau der Kurzname aus der `config.json`, ohne
   Großbuchstaben und ohne Leerzeichen. Bei einer Bestandsinstanz, die auf die neue Vorlage
   angehoben wird, setzt `--wert site=<kurzname>` ihn mit. Die Site gibt es nie als Parameter
   eines Werkzeugs.
2. Die Instanznamen kurz halten, zum Beispiel `google-search-console-manage-terminagent`: Die
   Werkzeugnamen der KI-Clients sind auf 64 Zeichen begrenzt.
3. Die Scopes mit den **exakten Instanznamen** nur dem Team dieser Site geben. Ein Scope auf eine
   Instanz gilt nur für diese Instanz.
4. Das Dienstkonto bei Google für die Properties der neuen Site freigeben, mit denselben Rechten
   wie in Abschnitt 1.
5. **Nach jeder Anlage `get_status` aufrufen.** Die Antwort nennt `site` und `hosts` der
   aufgelösten Site, die Property-IDs bleiben verborgen. Stimmen Site und Hosts nicht, ist die
   Instanz kopiert oder vertippt: korrigieren, bevor jemand sie nutzt. `site_required` heißt,
   dass die Instanz keinen Wert `site` hat (noch auf Vorlage 1.0.0), `site_unknown`, dass der
   Name nicht in der `config.json` steht.

Die Mandantentrennung per Instanz setzt voraus, dass nur der Betreiber Admin-Zugang hat, also GUI, Admin-MCP und `target_address`.
Wer Instanzen anlegen oder ändern kann, könnte eine Instanz auf eine fremde Site umbiegen.

## Zeiträume, Seitenvergleich und Segmente

Die Berichte nehmen `period_days` von 1 bis 90 (Standard 28). Das letzte Datum liegt höchstens drei
Tage vor heute in der Zeitzone der Quelle. Das Werkzeug `page_compare` der Vorlage
`google-search-console` vergleicht je freigegebener Seite zwei gleich lange, nicht überlappende
Zeiträume; es gibt es nur für die Search Console. Ein Scope auf die ganze Integration gibt dieses
Werkzeug mit.

Der Betreiber benennt je Site 1 bis 8 Segmente in `config.json` (Kleinbuchstaben, Ziffern und
Bindestrich, zum Beispiel `preise`); `all_public` meint immer alle Seiten der Site. `get_status`
nennt die Segmentnamen der Site. Die Vorlagen stehen seit dieser Fassung auf 2.0.0. Eine
Bestandsinstanz wird nicht automatisch angehoben: Der Admin hebt sie mit
`jnpt connector upgrade --yes <instanz>` an, der Wert `site` bleibt dabei erhalten.

## Schlüssel wechseln und entfernen

Wechsel jährlich und bei Verdacht sofort: in der Cloud Console einen neuen Schlüssel erzeugen,
wie in Abschnitt 2 eintragen, `get_status` prüfen, dann den alten Schlüssel in der Cloud
Console löschen.

**Entfernen ist kein Widerruf.** Wird der Wert in der GUI entfernt oder ersetzt, bleibt der
Google-Schlüssel gültig, bis er in der Cloud Console gelöscht ist.
