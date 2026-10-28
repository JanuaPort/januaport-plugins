> Vertrag 1 und seine vorhandenen Goldens gelten unverändert für Windows.
> Linux-Geräte der Art `server` verwenden ausschließlich Vertrag 2; siehe
> [Server-Vertrag 2](#server-vertrag-2). Die folgenden Python-/DPAPI-Regeln
> beschreiben Vertrag 1, soweit dort keine gemeinsame Regel genannt wird.

## Server-Vertrag 2

K1-Quelle: `januaport`, `origin/feat/1195-server-container`, Abschnitt
„Server-Geräte und Container-Pins“, `internal/scriptpins/container.go` und
`internal/desktop/testdata/manifest_container_canonical.bin`.

Vertrag 2 gilt ausschließlich für `server`/`always`. Pfade bleiben
`/api/device/v1/…`, Weg B ohne Listener. Jede Anfrage trägt
`X-Jnpt-Desktop-Contract: 2`; Manifest, Config, Pair und Heartbeat verwenden
`contract: 2`. Pair und Heartbeat ergänzen optional `container_runtime` aus
`rootless`/`rootful`; null und unbekannte Werte sind ungültig. Der Läufer sendet
den beim Start erkannten Wert. Desktop-Vertrag 1 sendet das Feld weiterhin nicht.

`pins[].container` enthält genau `image`, `command`, `network`, `cpu_millis`,
`memory_bytes`, `wall_s`, `secret_names`. Commit ist der reine Image-SHA256-Digest,
Algo `sha256`, Path leer, `declared_tools: []`. Kein Gitpaketabruf. Grenzen wie K1:
Image nur repo@sha256 ohne Tag; Befehl 1–32 Argumente mit höchstens 1024 Bytes,
kein NUL/ungültiges UTF-8; CPU 1–16000 mCPU, RAM 1–34359738368 Bytes, Wandzeit
1–86400 s. Netz `[a-z0-9][a-z0-9_-]{0,62}`, ohne host/bridge/default/private/pasta/
slirp4netns/container:/ns:. Max.16 eindeutige Geheimnisse nach
`[a-z0-9][a-z0-9_.-]{0,62}`, ohne `..`.

Das Container-Objekt gehört zu den Ed25519-signierten kanonischen Bytes.
`golden-v2/manifest_container_canonical.bin` ist bytegleich aus K1 gespiegelt.
Alle bisherigen Goldens einschließlich SHA256SUMS bleiben unverändert.
`TestVertrag2ManifestGolden` prüft das neue Fixture am echten Manifestadapter;
das D0-Testmodul bleibt der eingefrorene Vertrag-1-Wächter.

Claim ergänzt optional `allowed_secret_names`: fehlend/leer bindet nichts,
sonst nur Schnittmenge mit signiertem Pin. Ergebnis ergänzt verpflichtend
`mounted_secret_names`, eindeutige Teilmenge, leere Liste ausdrücklich `[]`.
Zeitpläne verwenden die signierte Deklaration. K1 verweigert bei leeren
declared_tools `runs/start`; wie unter Vertrag 1 startet deshalb ein werkzeugloser
Zeitplan ohne Lauf-Token und meldet anhand der historischen Manifestfreigabe nach.
Ad-hoc bleibt Claim → Start. Die bisherigen Gnadenfristen gelten auch für Server.

Zusätzliche Container-Fehler: `refused/hash`, `refused/runtime`, `refused/secret`,
`limit/wall`, `interrupted`. Normales Ende bleibt `ok` oder `error`.
**K1-Abweichung im Kerncode:** `cmd/jnpt/desktop_routes.go:desktopResult.valid`
akzeptiert noch weder Detail `secret` unter refused noch `wall` unter limit.
Diese beiden beauftragten Resultate werden nicht umbenannt; der Kern muss
nachgezogen werden. Bis dahin hält der Läufer HTTP-400-Ergebnisse im Journal.

Lokale Container-Konvention: Das Image stellt /bin/sh und tar bereit. Der Läufer
setzt --entrypoint /bin/sh ausdrücklich und überschreibt Image-ENTRYPOINTs.
--memory-swap entspricht --memory und erlaubt keinen zusätzlichen Swap.
Der feste Startcode entpackt privaten stdin-Tar auf tmpfs
(`/run/jnpt/input.json`, `token`, `ready`, UID/GID 65532, 0400) und startet dann
die signierte Befehlsliste. Token-Werte stehen nie in CLI-Args/Env/Labels.
Werkzeuglose Zeitpläne haben eine leere Token-Datei. Diese Konvention erweitert
keine Drahtfelder; Details und Installationsvoraussetzungen stehen im
[Server-Handbuch](../README.md#linux-server-modus).

# Draht-Vertrag v1 — Desktop-Läufer

Verbindlicher Vertrag für JanuaPort #1163 D0 einschließlich des vom PO
erweiterten und von SEC freigegebenen Deltas E1–E4; Vertragskennung **1**.
Pins haben einen Ausführungsort **`lokal | geraet:<id>`**. Nur Pins mit
`geraet` als Ausführungsort erscheinen in diesem Vertrag, jeweils für das
zugeordnete Gerät; lokale Pins gehören nicht in sein Manifest oder seine Aufträge.
D0 liefert diesen Vertrag, Goldens und einen Go-Artefakttest mit Generator.
D4 (#1167) ergänzt den [Windows-Client](../README.md) mit eigenen Adaptertests;
Kernimplementierung und gemeinsame Live-Abnahme liegen außerhalb dieses Moduls.

## Richtung und Erreichbarkeit

**Weg B:** Das Gerät holt alles selbst ab. Die Anlage (Box) meldet sich nie
beim Gerät. Auch Kopplung, Manifest-Sync und Ergebnisnachlieferung sind vom
Gerät eröffnete Anfragen. Abbrüche kommen mit der Heartbeat-Antwort.

`/api/device/v1/` ist standardmäßig nur im LAN oder Tunnel erreichbar.
Öffentlich ausschließlich als ausdrückliches Opt-in mit Allowlist und
Bremse (SEC C2). Das ersetzt weder Kopplung noch Geräte-/Laufberechtigung.

## Rahmen aller Anfragen und Antworten

Alle Endpunkte unten sind relativ zu **`/api/device/v1/`**.
Jede Anfrage trägt genau einmal **`X-Jnpt-Desktop-Contract: 1`**.
Außer **`POST pair`** trägt jede Anfrage außerdem genau einmal:

```text
Authorization: Bearer jnpt_<id>_<secret>
```

`pair` trägt **keinen Authorization-Header**, auch keinen leeren. Die
Kopplung ersetzt die manuelle Tokenübergabe. Der Windows-Befehl `pair --code`
nimmt den Kopplungscode per argv an; nur der Linux-Server liest ihn per stdin.
Nur MCP verlangt zusätzlich
`X-Jnpt-Run-Token`; das Lauf-Token ersetzt das Läufer-Token nicht.
`runs/start` bindet den synchronisierten Stand zusätzlich mit
`X-Jnpt-Manifest-Seq: <seq>` (positive uint64-Dezimalzahl ohne führende Nullen).
Token und Kopplungscode bleiben außerhalb von URLs und Dienstlogs.

**Jede Antwort** trägt **`Cache-Control: no-store`**, auch 204, Fehler und
Paketströme. JSON ist UTF-8 mit `Content-Type: application/json`, wird mit
**`DisallowUnknownFields`** gelesen und enthält genau einen JSON-Wert.
Pflichtfelder fehlen nicht und sind nicht `null`. Unbekannte geschlossene
Felder ergeben `400 invalid`; `input`, JSON-Schema und Werkzeugargumente
bleiben fachlich offene JSON-Objekte. Unbekannter `contract` im Header oder
Körper ergibt `400 contract`, auch bei widersprüchlichen Kennungen.
Fehlende/falsch geformte Pflichtfelder ergeben `400 invalid`.

KiB = 1024 Byte, MiB = 1024² Byte. Alle Größen sind inklusiv und werden an
tatsächlich gelesenen Körperbytes gemessen, unabhängig von `Content-Length`.
Bearer-Verkehr erfolgt über HTTPS oder einen geschützten Tunnel; für
`pair` gilt zusätzlich und vorrangig SEC E3.

## Kopplung, Vertrauen und Widerruf (E2/E3)

`POST pair {contract:1, code, runner_version}` liefert
`200 {device_id, token, expires_at, anlage_pubkey}`. `token` ist das
Läufer-Token; `expires_at` bezeichnet dessen UTC-Ablaufzeit, nicht die
Gültigkeitsdauer des Kopplungscodes. `anlage_pubkey` sind die 32 rohen
Ed25519-Public-Key-Bytes als Standard-Base64 mit Padding, kein PEM.

Der Code gilt **15 min ab Erzeugung**, **genau einmal**. Format (gleich dem Kern, #1164):
**`<device_id>.XXXX-XXXX-XXXX`**, also die öffentliche Geräte-ID (16 kleine
Hex-Zeichen), ein Punkt und drei Gruppen zu vier Zeichen aus Crockford-Base32
(`0123456789ABCDEFGHJKMNPQRSTVWXYZ`, ohne I/L/O/U oder Alias-Schreibweisen).
Die Geräte-ID ordnet die fünf Fehlversuche dem Gerät zu; die zwölf Zeichen
dahinter sind gleichverteilt kryptografisch zufällig: **60 Bit Geheimnis**. Die
Geräte-ID ist öffentlich und eindeutig. Nach **5 Fehlversuchen** zu diesem Gerät ist der Code verbrannt;
Erfolg und Verbrennen bleiben bis zum ursprünglichen Ablauf vermerkt. Unbekannte,
verbrannte und verbrauchte Codes ergeben denselben 401-Fehler. Eine
**globale Bremse** gilt auch für unbekannte Kennungen: `429` plus `Retry-After`.

**SEC E3:** `pair` ist ausschließlich über **HTTPS mit gültigem Zertifikat
oder Loopback** zulässig. Loopback meint den tatsächlich verbundenen Peer
`127.0.0.0/8` oder `::1`; ein beliebiger Hostname genügt nicht. Ein
selbstsigniertes Zertifikat ist ausschließlich mit **`--ca <datei>`** oder
**`--fingerprint <sha256>`** zulässig. Der Fingerprint ist SHA-256 über das
DER-kodierte Serverzertifikat, 64 Hex-Zeichen; er muss exakt passen.
`--ca` verwendet die ausdrücklich vertraute CA-Datei. Die gewählte
Vertrauensprüfung muss vor Übertragung des Codes erfolgreich sein.
**Einen Schalter, der die Prüfung abschaltet, gibt es nie.** Ein entfernter
HTTP-Endpunkt wird auch dann abgewiesen, wenn er hinter einem Tunnel liegt.

Der Client speichert `anlage_pubkey` aus der erfolgreichen Kopplung dauerhaft
(**Trust on First Use**, abgesichert durch E3). Ein späteres Manifest oder
eine gewöhnliche Antwort ersetzt diesen Schlüssel nicht. **In v1 erfolgt
Schlüsselrotation ausschließlich durch neue Kopplung (E2).**

Ein Widerruf ergibt bei **jeder späteren authentifizierten Anfrage** mit
diesem Credential **`401 {error:{class:"revoked"}}`**, ohne `message` oder
weitere Details. Dann löscht das Gerät **Credential, Pakete, Manifest und
Journal**, einschließlich vorbereiteter Umgebungen und Kopplungszustand.
Diese Löschung hat Vorrang vor der sonstigen Aufbewahrung unbestätigter
Ergebnisse. Neue Kopplung benötigt einen neuen gültigen Code.

## Endpunkte

| Endpunkt | Anfrage | Antwort und Grenzen |
|---|---|---|
| `POST pair` | `{contract, code, runner_version}`, kein Bearer; E3 | `200 {device_id, token, expires_at, anlage_pubkey}` |
| `GET config` | kein Körper | `200 {contract:1, device:{id,name,kind:"dedicated"\|"workstation"}, expected_account, start_policy:{mode:"always"\|"idle", idle_minutes}, heartbeat_s:60, poll_hold_s:25}` |
| `GET manifest` | kein Körper | `200 {manifest,sig}`, signiertes Geräte-Manifest gemäß E1 |
| `POST heartbeat` | `{contract:1, ready:bool, runner_version, manifest_seq}`, höchstens **1 KiB**, alle **60 s** | `200 {cancel:[job_id…], manifest_seq_current}`; leere Abbruchliste `[]` |
| `POST jobs/claim` | kein Körper, Long-Poll | `200 {job}` oder körperloses `204` nach höchstens **25 s**; zweiter Claim bei belegtem Platz sofort `204` |
| `GET packages/{commit}?path=<URL-kodierter Pin-Pfad>` | kein Körper | Paketstrom nur für `(commit,path)` eines **offenen eigenen Auftrags oder Pins im aktuellen signierten Manifest dieses Geräts**, sonst `404`; höchstens **128 MiB** samt Kopf/Rahmen |
| `POST jobs/{id}/start` | kein Körper | `200 {run_token, expires_at, mcp_path}` für Ad-hoc-Auftrag; zweiter Start `409 already_started` ohne Token |
| `POST runs/start` | `{run_key}`, zusätzlich `X-Jnpt-Manifest-Seq` | `200 {run_token, expires_at, mcp_path}` für geplanten Lauf mit Werkzeugen; Box prüft aktuellen Stand und Slot; veraltete seq `409 manifest` |
| `POST mcp` | zustandsloses MCP; Läufer-Token und `X-Jnpt-Run-Token`; höchstens **8 MiB** | MCP-JSON-Antwort; `200` im Golden |
| `POST runs/result` | `{run_key \| job_id, manifest_seq, commit, end, detail?, error_type?, error_at?, log, ran_offline:bool, finished_at}` | körperloses `204`; identische Wiederholung `204`, abweichend `409 ended`; `log` höchstens **64 KiB UTF-8**, Gesamtkörper **80 KiB** |

`runs/result` ist der gemeinsame Ergebnisweg für geplante und Ad-hoc-Läufe;
der bisherige Pfad `jobs/{id}/result` wird durch ihn ersetzt, kein Alias.
Genau eines von `run_key` und `job_id` ist gesetzt.

Datenschutz-Leitplanke 1 bleibt: Heartbeat enthält **keinen Sitzungsstatus,
keine Leerlaufzeit, kein Konto**. Er enthält ausschließlich `contract`,
`ready`, `runner_version`, `manifest_seq`; vor dem ersten Manifest ist die
Sequenz **0**. Ist `manifest_seq_current` höher, holt das Gerät das Manifest.
Konto/Leerlauf prüft es lokal; `expected_account` ist eine Vorgabe der Box.
`idle_minutes` ist eine nichtnegative ganze Vorgabe, bei `always` ohne Wirkung.

**Ein Ausgabefeld gibt es nicht.** `log` ist ein Pflicht-String, leer als `""`.
`error_type` und `error_at` sind optionale strukturierte Hinweise, keine
freien Fehlersätze; `error_at` bezeichnet eine Datei im Pin mit Zeilennummer.
Freier Lauftext steht ausschließlich im begrenzten `log`.

`mcp_path` ist `/api/device/v1/mcp` an derselben Anlage. MCP hat keinen
Sitzungszustand und gibt keine `Mcp-Session-Id` aus. Die Box prüft pro Aufruf
Gerät, Lauf-Token, Ablaufzeit und freigegebene Werkzeuge. Ein Lauf-Token
entsteht erst bei `jobs/{id}/start` oder `runs/start`.

## Signiertes Geräte-Manifest (E1)

Der signierte JSON-Wert `manifest` enthält genau:

```text
{contract:1, device_id, seq, issued_at, grace_hours,
 pins:[{pin, commit, path, algo, declared_tools:[…],
        schedules:[{name, cron, tz, input}]}]}
```

`seq` ist eine **streng steigende uint64**, ab 1, dauerhaft je Gerät
gezählt; kein Überlauf oder Zurücksetzen nach Box-Neustart. `issued_at`
ist UTC als RFC 3339 mit `Z`, `grace_hours` eine ganze Zahl **1–72**.
Listen sind vorhanden, ggf. `[]`. Jeder Pin gehört zu `geraet:<device_id>`.
Pin- und Zeitplannamen sind jeweils eindeutig und enthalten keinen `/`.
`commit`, `path` und `algo` folgen denselben Git-Regeln wie beim Auftrag.
`declared_tools` ist die vollständige Liste deklarierter Werkzeuge.
`cron` hat **fünf Felder** (Minute, Stunde, Tag, Monat, Wochentag), `tz` ist
eine IANA-Zeitzone, kein geräteabhängiges `Local`; `input` ein JSON-Objekt
mit höchstens **256 KiB**.

`sig` sind die **64 Ed25519-Signaturbytes** über die folgenden kanonischen
Bytes **allein des Wertes `manifest`**, als Standard-Base64 mit Padding.
Weder HTTP-Umschlag noch `sig` werden mitsigniert. Verifiziert wird mit dem
bei der Kopplung gespeicherten Schlüssel, nicht einem Schlüssel aus dem Manifest.

### Kanonisierung v1

1. Genau einen gültigen UTF-8-JSON-Wert lesen; doppelte Objektschlüssel
   (auch nach Auflösung von Escapes), ungültiges UTF-8, ungepaarte
   UTF-16-Surrogat-Escapes und weitere Werte danach abweisen.
2. **Alle Objektschlüssel rekursiv nach ihren dekodierten UTF-8-Bytes
   aufsteigend sortieren**, Arrays in übertragener Reihenfolge belassen.
3. Ohne Leerraum außerhalb von Strings, ohne BOM und **ohne abschließendes
   LF** als UTF-8 ausgeben. Keine Unicode-Normalisierung. In Strings werden
   nur `"`, `\`, Steuerzeichen sowie U+2028/U+2029 escaped: `\"`, `\\`,
   `\b`, `\f`, `\n`, `\r`, `\t`, sonst `\u00xx` mit kleinen Hex-Ziffern;
   U+2028/U+2029 als `\u2028`/`\u2029`. `/`, `<`, `>`, `&` bleiben unverändert.
   Andere Unicode-Zeichen bleiben UTF-8, gültige Surrogatpaare werden dekodiert.
4. `true`, `false`, `null` klein schreiben. **Zahlentoken bleiben exakt
   erhalten**, ohne Float-Konvertierung: `1`, `1.0`, `1e0` sind verschiedene
   signierte Bytes. Das gilt auch in `input`; `seq` bleibt ohne Rundung bis
   **18446744073709551615** erhalten und wird als uint64-Dezimalzahl ohne
   Vorzeichen, Exponent oder führende Nullen übertragen. Dies ist die hier
   definierte v1-Kanonisierung, kein Verweis auf eine IEEE-754-Normalisierung.

`manifest_canonical.bin` zeigt genau diese Bytes, `manifest_response.json`
die zugehörige Signatur. `manifest_test_key.json` ist ausdrücklich **NUR TEST**
markiert und enthält den öffentlichen RFC-8032-Testvektor 1 (Seed/Public Key).
Er ist kein Anlagenschlüssel und dient ausschließlich reproduzierbaren Goldens.

Das Gerät **lehnt eine fremde `device_id`, `seq` ≤ letzter angenommener seq
und eine falsche Signatur ab**. Ergebnis der Sync-Prüfung: Klasse **`manifest`**;
das **alte Manifest bleibt gültig**, ebenso sein letzter erfolgreicher
Sync-Zeitpunkt. Erst nach vollständig erfolgreicher Prüfung werden Manifest,
seq und Sync-Zeitpunkt atomar/dauerhaft übernommen. Ein fehlerhafter Sync
verlängert die Gnadenfrist nicht. Bei erstmals fehlendem Manifest kein Lauf.
Jeder erfolgreiche Sync liefert eine neue seq, **auch bei unveränderten Pins**;
eine wiederholte alte Antwort ist keine erfolgreiche Synchronisierung.

## Takt auf dem Gerät und geplante Läufe ohne Claim

**Das Gerät führt die Zeitpläne selbst aus. Genau 1 min vor jedem geplanten
Lauf** synchronisiert es das Manifest und bei einem neuen `commit` das Paket,
mit vollständiger Git-/SHA-1DC-, Wheel-, Python-Allowlist- und Umgebungsprüfung.
Die Paketberechtigung für Manifest-Pins gilt ausdrücklich **ohne offenen Claim**.
Ein geänderter Pfad/Algorithmus wird ebenso neu geprüft. C1/C3 bleiben erhalten.

Der deterministische **`run_key = <pin>/<schedule>/<UTC-Slot>`** verwendet
Pin- und Zeitplannamen des Manifests, `/` als Trenner und den geplanten
UTC-Termin als `YYYY-MM-DDTHH:MM:00Z`, z.B. `demo/daily/2026-10-23T10:00:00Z`.
Es ist der geplante Termin, nicht der tatsächliche Startzeitpunkt.
Sommerzeit-Wiederholungen haben verschiedene UTC-Slots; eine lokale Zeitlücke
hat keinen Slot. Derselbe Slot wird nach einem Neustart nicht erneut ausgeführt.

Ist der Lauf online **und hat er `declared_tools`**, ruft das Gerät
`POST runs/start {run_key}` mit der angenommenen `X-Jnpt-Manifest-Seq` auf.
Die Box bindet Pin, Commit und Slot an ihr **aktuelles Manifest**, prüft die
zugehörige seq und den Cron-/Zeitzonentermin. Der Slot muss in der aktuellen
UTC-Minute fällig sein. Ein zwischen Sync und Start geänderter Stand ergibt
`409 manifest`, ohne Token; erst neu synchronisieren. Ein zweiter Start
desselben Laufs ergibt `409 already_started`, ohne Token. Ein von einem
anderen Lauf belegter Platz ergibt `409 busy`. Ad-hoc- und geplante Läufe
teilen den einen Platz; auch werkzeuglose/offline Läufe belegen ihn lokal.

**Offline-Regel:** Ein geplanter Lauf startet ohne Box ausschließlich, wenn
(a) der Pin **keine `declared_tools`** hat und (b)
**`jetzt − letzter erfolgreicher Sync < grace_hours`** ist. Die Gleichheit
an der Grenze reicht nicht. Ohne erfolgreichen Sync, bei Uhr-Rücksprung
hinter diesen Zeitpunkt, mit Werkzeugen oder abgelaufener Gnadenfrist:
**`refused/offline`**, kein Prozess. Eine vorliegende ungültige Signatur wird
nicht als Netz-Ausfall umgedeutet. Bei einem fehlgeschlagenen Sync bleibt das
alte Manifest die einzige Grundlage; dessen Tools und Grace-Grenze gelten.
Ein zulässiger werkzeugloser Lauf benötigt auch online keinen Lauf-Token.
**Läufe mit deklarierten Werkzeugen brauchen den Lauf-Token und damit die Box.**

Ad-hoc-Läufe über `start_desktop_run` gehen weiter ausschließlich über
**`jobs/claim` → `jobs/{id}/start`**, ohne autonomen Offline-Start.

## Auftrag, Platz und Fristen

```text
{job_id, pin, commit, path, algo, manifest_seq, input,
 limits:{wall_s, tool_calls}, start_by}
```

`job_id` ist UUIDv4. `pin` bezeichnet den Pin, `path` den relativen Pin-Ordner
mit `/`. `algo` ist `sha1` oder `sha256`, `commit` eine kleingeschriebene
Hex-ID mit 40 bzw. 64 Zeichen. **`manifest_seq` bezeichnet den bei der
Freigabe gespeicherten Manifeststand** und wird für spätes Melden mitgeliefert;
er wird nicht aus einem späteren Heartbeat abgeleitet. Die Box hält die
zugehörige Freigabehistorie. `input` ist ein JSON-Objekt ≤ **256 KiB**,
`wall_s` positiv/ganzzahlig, `tool_calls` nichtnegative Ganzzahl (0 erlaubt
keinen Werkzeugaufruf). Zeitpunkte sind UTC/RFC 3339 mit `Z`.

- **Ein Platz je Gerät**, auch für geplante Läufe. Zweiter Claim bei offenem
  abgeholtem oder laufendem Auftrag sofort 204.
- **Lease 10 min** ab Claim; `start_by` ist die Startfrist. Nicht gestartet:
  `expired`. Box-Neustart verlängert keine Lease.
- `start_desktop_run`: Wandzeitvorgabe **900 s**, höchstens **86400 s**;
  wirksam `min(limits.wall_s, timeout_s)` aus dem Paket-Manifest.
- Lauf-Token ab erfolgreichem Start: Ablauf nach wirksamer Wandzeit **+ 5 s**.
  Für geplante Läufe gilt `timeout_s` des freigegebenen Paket-Manifests
  als Wandzeit, ebenfalls mit 5 s Tokenzugabe. Der nächste Werkzeugversuch
  oberhalb der Aufrufgrenze endet `limit/tool_calls`.
- Die frühere ein-stündige Box-Schlange für Zeitpläne entfällt mit dem
  Takt auf dem Gerät. Ad-hoc-Aufträge bleiben in der Box persistent.
- **`runs/result` erlaubt späte Meldungen auch nach Tokenablauf + 5 min**;
  die frühere Annahmefrist wird durch E4 ersetzt. Der Lauf-Token begrenzt
  MCP, nicht den Journalversand. Unbestätigte Ergebnisdaten halten keinen
  lokalen Laufplatz belegt.

## Journal, spätes Melden und Neustart (E4)

Ergebnisse geplanter **und** Ad-hoc-Läufe gehen **zuerst** in ein dauerhaftes
lokales, **DPAPI-CurrentUser-verschlüsseltes Journal**, auch wenn die Box online
ist. Höchstens **200 Ergebniseinträge bzw. 16 MiB gespeicherter Journalbestand**,
einschließlich DPAPI-/Rahmen-Overhead, Laufmarker und Verlustzähler (zusätzlich
zu den Einzelgrenzen 64/80 KiB); kein stilles Kürzen eines Datensatzes.
Vor Aufnahme über einer Grenze fallen die ältesten
Ergebniseinträge, bis beide Grenzen eingehalten sind; ein dauerhafter
**Verlustzähler** steigt je verworfenem Eintrag. Die Bytegrenze wird an der
tatsächlich zu speichernden verschlüsselten Darstellung geprüft.

Vor Prozessstart liegt zusätzlich ein dauerhaft verschlüsselter Laufmarker
mit `{job_id | run_key, manifest_seq, commit, phase}` vor (`claimed`/`started`).
Nach Geräte-Neustart wird ein offener Lauf als **`interrupted`** journalisiert;
er wird **nie erneut gestartet**. Aktive Laufmarker werden nicht als alte
Ergebniseinträge verdrängt. Der bisherige Klartext-Ansatz `state.json` entfällt.

`POST runs/result` verwendet ausschließlich das Läufer-Token. `ran_offline`
ist ein Pflicht-Bool, `finished_at` ein Pflicht-Zeitpunkt in UTC; beide sind
Meldedaten und erweitern keine Berechtigung. Die Box nimmt die Meldung nur
an, wenn **`commit` zum damals freigegebenen Pinstand der `manifest_seq`
passt**, zusätzlich an dieses Gerät und die angegebene Job-/Slot-Identität
gebunden. Für Ad-hoc nutzt sie die persistierte Jobbindung, für Zeitpläne
die zu dieser seq archivierte Pin-/Zeitplanfreigabe. Ein späterer Pin-Wechsel
macht eine passende alte Meldung nicht ungültig. Nicht zuordenbare oder
unpassende Freigaben werden mit `400 invalid` abgewiesen.
Das späte Ergebnis läuft in der Box durch **vetLog**.

**Erstes gültiges Ergebnis gewinnt atomar und dauerhaft.** Gleiches noch
mal ergibt 204, abweichendes **409 `ended`**. Die Identität ist je Gerät
`job_id` beziehungsweise `run_key`; `manifest_seq` und `commit` gehören zum
verglichenen Ergebnis, nicht zu einem zweiten Idempotenzschlüssel. Gleiche
Werte aller Felder und gleiche An-/Abwesenheit optionaler Felder sind
identisch; Schlüsselreihenfolge/äußere Leerzeichen sind unerheblich, `log`
bleibt byte-genau. Erst nach **204-Bestätigung** löscht das Gerät den Eintrag;
Transportfehler oder 409 löschen ihn nicht. Ausnahmen sind das ausdrücklich
gezählte Verdrängen an der Kapazitätsgrenze und Widerruf samt Gesamtlöschung.

**Die Box hält Ad-hoc-Aufträge persistent und löscht deren Eingabe bei Ende;
ein Claim funktioniert auch nach Box-Neustart.** Auch Manifest-seq,
Freigabehistorie, Startmarken und Ergebnis-Idempotenz überleben den Neustart.
Es gibt kein pauschales `start`/`result`-404 und kein Verwerfen wegen eines
Box-Neustarts; unbekannte/fremde IDs bleiben die gewöhnlichen 404-Fälle.

## Ende-Klassen und Detail-Allowlist

Geschlossene Menge; `detail` fehlt, außer wo ausdrücklich verlangt.

| `end` | Zulässiges `detail` | Herkunft |
|---|---|---|
| `ok` | keines | Gerät |
| `error` | keines | Gerät |
| `timeout` | keines | Gerät |
| `limit` | `tool_calls` (Pflicht) | Gerät |
| `internal` | keines | Gerät |
| `cancelled` | keines | Gerät |
| `refused` | `hash`, `runtime`, `account`, `manifest`, **`offline`** (genau eines, Pflicht) | Gerät, vor Prozessstart |
| `interrupted` | keines | Gerät, Neustart mit offenem Laufmarker |
| `expired` | keines | nur Box, ohne Start |
| `lost` | keines | nur Box, ohne verwertbares Ergebnis |
| `denied` | keines | nur Box, Berechtigung verweigert |

Das Gerät kann `expired`/`lost`/`denied` nicht per `runs/result` setzen
(`400 invalid`). Ein Heartbeat-Abbruch führt zu Prozessabbruch und `cancelled`.
Die Sync-Ablehnung **Klasse `manifest`** ist keine zusätzliche Ende-Klasse.

## Fehler: feste Sätze

Regelform **`{error:{class,message}}`**, Ausnahme Widerruf **`{error:{class:"revoked"}}`**.
Keine Headerwerte, Token, fremden IDs, Kontodaten oder internen Fehlertexte.

| HTTP | `class` | `message` |
|---|---|---|
| 401 | `unauthorized` | Die Anmeldung ist fehlgeschlagen. |
| 401 | `revoked` | Feld fehlt vollständig |
| 404 | `not_found` | Der Auftrag oder das Paket wurde nicht gefunden. |
| 409 | `already_started` | Der Auftrag wurde bereits gestartet. |
| 409 | `not_claimed` | Der Auftrag wurde nicht abgeholt. |
| 409 | `ended` | Der Auftrag ist bereits beendet. |
| 409 | `manifest` | Das Manifest ist nicht aktuell. |
| 409 | `busy` | Das Gerät ist belegt. |
| 413 | `too_large` | Die Anfrage überschreitet die zulässige Größe. |
| 400 | `contract` | Die Vertragsfassung wird nicht unterstützt. |
| 400 | `invalid` | Die Anfrage ist ungültig. |
| 503 | `unavailable` | Der Dienst ist vorübergehend nicht verfügbar. |
| 429 | `rate_limited` | Es sind zu viele Anfragen eingegangen. |

401 nennt kein Detail. 404 ist für unbekannte und fremde Aufträge/Pakete
**byte-gleich**, samt Status, Körper und vertraglichen Headern. Fremde IDs
werden vor Zustandsprüfung verdeckt. `429` und `503` tragen zusätzlich
**`Retry-After`** als positive ganze Anzahl Sekunden (Golden `60`).

## Benannte Grenzen

- Ein widerrufenes Gerät kann bei fehlender Box-Verbindung offline bis zur
  Gnadenfrist weiterlaufen; erst Kontakt macht den Widerruf sichtbar.
- Der Ed25519-Schlüssel rotiert in v1 **nur über neue Kopplung (E2)**.
- Bei **`workstation`** handelt das Skript mit den **Rechten der Person**.
- **DPAPI CurrentUser schützt nicht gegen jemanden, der die Sitzung hat (C4).**

## Paketformat `application/vnd.jnpt.gitobjects.v1`

Der Körper ist ein binärer Strom ohne zusätzliche Kompression:

1. **4 Byte Länge, unsigned Big Endian**, danach genau so viele UTF-8-Bytes
   JSON-Kopf: `{format:"gitobjects", version:1, algo, commit, path, objects:N}`.
2. Danach genau **N Rahmen**: jeweils 4 Byte Länge (unsigned Big Endian),
   dann **1 Byte Typ** (`c`, `t`, `b`) und der unveränderte Git-Rohinhalt.
   **Die Rahmenlänge schließt das Typbyte ein**, nicht die vier Längenbytes.
   Der Kopf zählt nicht zu N. Hinter dem letzten Rahmen folgen keine Bytes.

Bei Pinentzug beendet der Kern alle offenen Aufträge dieses Pins mit `denied`
und bricht einen laufenden Lauf ab; der script-runner liefert ausschließlich
Pakete für aktuelle Pins (siehe [Weg A](../../script-runner/contract/README.md#desktop-pins-und-paketabruf-auf-weg-a-1165-d2b)).

`algo`, `commit` und `path` müssen dem offenen eigenen Auftrag oder dem
freigegebenen Pin im aktuellen signierten Geräte-Manifest entsprechen. Enthalten sind
genau das angeforderte Commit-Objekt, die Bäume von der Wurzel bis `path`
und alle Bäume/Blobs unterhalb dieses Pin-Ordners. Eltern-Commits und
Objekte von Geschwisterordnern gehören nicht dazu. Ein gemeinsam
referenziertes Objekt wird genau einmal übertragen. Der Typ entspricht
`c` = `commit`, `t` = `tree`, `b` = `blob`; Tree-Referenzen enthalten je nach
Algorithmus 20 oder 32 rohe ID-Bytes, keine Hex-Zeichen.

**Prüfung auf dem Gerät vor jeder Ablage**, auch bei wiederholtem Abruf:

1. Jeden Objekthash selbst nachrechnen:
   **`id = H("<typ> <len>\0" + inhalt)`**, mit vollem Git-Typnamen und
   dezimaler Bytezahl des Rohinhalts ohne das Transport-Typbyte.
2. Die nachgerechnete Commit-ID muss gleich **`job.commit`** beziehungsweise
   **`pins[].commit`** des freigegebenen Gerätestands sein. Aus dem Commit
   die Wurzel lesen, den Baum bis zum freigegebenen `path` und darunter ablaufen;
   Referenzen müssen zum richtigen Objekttyp passen.
3. **Abweisen:** fehlende und **überzählige** Objekte, doppelte Objektrahmen,
   zusätzliche Bytes, kaputte Längen, unbekannte Typen/Versionen/Algorithmen,
   Modi **120000** (Symlink) und **160000** (Gitlink), andere Modi als
   `40000`, `100644`, `100755`, unzulässige Namen oder überschrittene Grenzen.
   Höchstens 128 MiB einschließlich Kopf und Rahmen; höchstens 256
   Baumkanten von der Wurzel zu einem Ziel. Längen vor Allokation prüfen.
4. Unzulässige Namen: nicht UTF-8, leer, `.`/`..`, Steuerzeichen/NUL,
   `/`, `\`, `<`, `>`, `:`, `"`, `|`, `?`, `*`, abschließender Punkt oder
   Leerzeichen sowie Windows-Gerätenamen `CON`, `PRN`, `AUX`, `NUL`,
   `COM1`–`COM9`, `LPT1`–`LPT9` (auch vor einer Dateiendung, ohne Beachtung
   der Großschreibung). Absolute Pfade und Laufwerks-/UNC-Pfade sind damit
   ausgeschlossen. Namen eines Baums müssen nach Unicode-Faltung
   `ToLower(ToUpper(r))` eindeutig sein. `path` besteht aus zulässigen,
   nichtleeren Segmenten; kein führender/abschließender `/`.
5. Folge jeder Paket-/Git-Prüfabweichung: **`refused/hash`, kein Prozess**.
   Erst nach erfolgreicher Gesamtprüfung wird Inhalt zur Ausführung abgelegt.

**SEC C3:** Bei `sha1` nutzt der Client **SHA-1DC mit Kollisionserkennung
statt `crypto/sha1`**. Auch ein Kollisionsbefund ist `refused/hash`.
Der Artefakttest nutzt dafür `github.com/pjbgf/sha1cd` in gepinnter Fassung.
Für `sha256` gilt SHA-256. Ein passend geformter Kopf ersetzt keine Hashprüfung.

## Paket-Manifest `jnpt-desktop.yaml` im Pin-Ordner

| Feld | Regel |
|---|---|
| `name` | Name des Pins |
| `description` | Beschreibung des Skripts |
| `entry` | relativer Pfad zur Python-Einstiegsdatei im Pin-Ordner |
| `input_schema` | JSON-Schema für das Eingabeobjekt |
| `tools` | Liste der freigegebenen Werkzeugnamen, darf leer sein |
| `timeout_s` | ganze Sekunden, **1–86400** |
| `requirements` | falls vorhanden ausschließlich **`requirements.lock`**, pip-Format mit `--hash=sha256:` je gesperrtem Paket |
| `python` | **Pflicht**: relativer Pfad zur embeddable-ZIP im Pin-Ordner |

Die sechs Basisfelder und `python` sind Pflicht. Lock und Wheels werden
mitgeliefert: `wheels/*.whl`, Plattform-Tags **`win_amd64` / `any`**, zur
ausgewählten Python-Laufzeit passende Python-/ABI-Tags aus **`cp312`, `cp313`,
`abi3`, `none`**.
`cp312`/`cp313` bezeichnen Interpreter beziehungsweise versionsgebundene ABI,
`abi3`/`none` die ABI, keine frei austauschbaren Teile. Ein cp313-Wheel ist
unter Python 3.12 nicht kompatibel. `any` verlangt ABI `none`.
`tools` muss als Menge mit `declared_tools` des freigegebenen Pins
übereinstimmen; ein fehlender oder abweichender Stand ergibt `refused/manifest`.
Pfadangaben bleiben im Pin-Ordner. Manifestfehler ergeben `refused/manifest`,
eine unpassende lokale Kontovorgabe `refused/account`, jeweils vor Prozessstart.

## Drei Hash-Stufen und Python-Herkunft

1. **Git-IDs** binden Commit, Bäume, Skript, Lock, Wheels und Python-ZIP an
   den Auftrag; Prüfung vor jeder Ablage wie oben.
2. **SHA-256 der Wheels gegen das Lock** vor dem Entpacken. Ein Git-Hash
   ersetzt diese Prüfung nicht. Eine Abweichung ergibt `refused/hash`.
3. **Umgebungs-Manifest** nach dem Entpacken: je Datei Pfad, Größe, SHA-256.
   **Vor jedem Start** gegen den tatsächlichen Inhalt nachrechnen,
   einschließlich fehlender/zusätzlicher Dateien. Abweichung:
   `refused/hash`, kein Prozess. Das gilt auch für eine wiederverwendete Umgebung.

**SEC C1:** Die durch `python` benannte ZIP wird **zusätzlich** vor dem
Entpacken gegen [python-allowlist.json](python-allowlist.json) geprüft.
Format: `{version:1, entries:[{version, filename, sha256}]}`. Die äußere
`version` bezeichnet das Listenformat, die Eintragsversion die Python-Version.
Version, Dateiname und SHA-256 müssen zu **demselben** Eintrag passen.
Die Liste wird mit dem Läufer ausgeliefert, nicht aus dem Pin übernommen.
Keine Übereinstimmung, fehlender Eintrag oder ungültige Liste:
**`refused/runtime`, kein Prozess**. Der Betreiber kann nicht durch einen
anderen Git-Pin die python.org-Herkunftsprüfung ersetzen.

Die Liste ist für die zwei ausdrücklich zugelassenen Dateien vollständig,
ohne TODO oder Beispielhash; sie lässt **nicht pauschal alle Patchversionen**
von 3.12/3.13 zu. Weitere Versionen benötigen verifizierte Einträge.
Herkunft der SHA-256: offizielles Sigstore-Bundle (`messageSignature` →
`messageDigest`, Algorithmus `SHA2_256`), zusätzlich am vollständig von
python.org heruntergeladenen ZIP nachgerechnet:

| Datei | ZIP / offizielle Digest-Quelle |
|---|---|
| `python-3.12.10-embed-amd64.zip` | [ZIP](https://www.python.org/ftp/python/3.12.10/python-3.12.10-embed-amd64.zip), [Sigstore-Bundle](https://www.python.org/ftp/python/3.12.10/python-3.12.10-embed-amd64.zip.sigstore) |
| `python-3.13.9-embed-amd64.zip` | [ZIP](https://www.python.org/ftp/python/3.13.9/python-3.13.9-embed-amd64.zip), [Sigstore-Bundle](https://www.python.org/ftp/python/3.13.9/python-3.13.9-embed-amd64.zip.sigstore) |

Die D0-Recherche vergleicht HTTPS-Quelldigest und ZIP-Hash; sie ist keine
zusätzliche kryptografische Sigstore-Signaturabnahme. Die ZIPs werden nicht
in dieses Repository aufgenommen und beim Start nicht aus dem Netz geladen.

## Versionierung, Referenzdateien und Prüfung

Die Vertragskennung bleibt **1**, ebenso Paketversion 1; Updates **zuerst
Kern/Box, dann Läufer**. Ein unbekannter `contract` ergibt `400 contract`.
Die Python-Allowlist bleibt für 3.12.10/3.13.9 vollständig, ohne TODO.

`golden/*.json` sind HTTP-Umschläge: Anfragen `{method,path,headers,body?}`,
Antworten `{status,headers,body?}`. `body` bezeichnet den tatsächlichen
JSON-Körper; körperlose Nachrichten lassen es weg. `body_file` verweist
auf ein Binärpaket. Ausnahme `manifest_test_key.json`: klar markierte
Testschlüssel-Metadaten, kein Drahtkörper. `manifest_canonical.bin` sind
die signierten UTF-8-Bytes ohne LF. Ein Geräte-Manifest und das
Paket-Manifest `jnpt-desktop.yaml` erfüllen verschiedene Aufgaben.

Die zwei kleinen `package_sha1.bin`/`package_sha256.bin` bleiben unveränderte
**Formatbeispiele** mit Commit, Wurzelbaum, Pin-Baum und Blob; keine
startbaren Pins. `SHA256SUMS` enthält sortierte LF-Zeilen
`<sha256> *<dateiname>` für **alle** Golden-Dateien außer sich selbst;
sein Hash ist im Test gepinnt. Die Python-Allowlist wird separat gepinnt.

Im eigenen Go-Modul `desktop-runner/contract/`:

```sh
go test -count=1 ./...
go vet ./...
go mod verify
# Bewusste Vertragsänderung: Pakete, Manifest-Signatur/-Bytes und Summen.
go test -run '^TestGenerateGolden$' -update -v
```

Der Generator signiert ausschließlich mit dem klar markierten Testschlüssel.
Anschließend den ausgegebenen SHA256SUMS-Pin bewusst im Test nachziehen.
Normale Tests schreiben keine Referenzdateien. Der Test prüft alle JSONs,
Rahmen/Tokenrollen, Paket-Hashes, kanonische Bytes, Ed25519, uint64-Sequenzen,
Ablehnung alter/gleicher seq und fremder Geräte bei unverändertem altem
Manifest sowie Offline-Grenze, Ergebnisbindungen und Prüfsummen.

**Implementierungsgrenze von D0:** Die Artefaktprüfungen ersetzen keine
Laufzeitadapter-Abnahme. E3/CA/Fingerprint, Code-Verbrauch/Fehlversuche/Bremse,
Widerrufslöschung, echte Box-Neustarts, Slot-/Zeitplanprüfung, historischer
Freigabevergleich, vetLog und DPAPI-/Kapazitätsverhalten brauchen
Verbotsproben und Mutationen an echten Kern-/Geräteadaptern. Die Geräteadapter
und ihre Prozess-, Konto-, Wheel-/ZIP-/Umgebungsprüfungen liegen seit D4 in
../internal/; tragende Tests nennt ../CLAUDE.md. Die Kernabnahme bleibt separat.
Es wird kein Ersatzadapter in D0 eingeführt.
