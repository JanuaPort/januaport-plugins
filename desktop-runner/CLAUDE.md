# CLAUDE.md — desktop-runner

## Verantwortung

Desktop-Läufer für JanuaPort #1163. Ein Gerät holt Aufträge selbst ab und
führt später gepinnte Python-Skripte lokal aus. D0 enthält ausschließlich
Vertrag, Referenzdaten und einen Go-Artefakttest mit Generator. Der PO-Schnitt
umfasst Kopplung, signiertes Geräte-Manifest, lokalen Zeitplantakt und
DPAPI-Journal (SEC E1–E4, C4). Status: im Bau.
Maßgeblich: [Draht-Vertrag v1](contract/README.md).

## Aufbau

| Pfad | Inhalt |
|---|---|
| `contract/README.md` | Verbindlicher deutscher Draht-Vertrag |
| `contract/golden/` | HTTP-Beispiele, Gitobjekt-Pakete, kanonische Manifestbytes, klar markierter Testschlüssel, Signatur, SHA256SUMS |
| `contract/python-allowlist.json` | Kuratierte python.org-SHA-256, SEC C1 |
| `contract/contract_test.go` | Ausschließlich Artefaktprüfung und deterministischer Generator |

## Invarianten

1. Nur Pins am Ausführungsort `geraet:<id>`, keine `lokal`-Pins. Weg B:
   Gerät holt alles ab, keine Verbindung von der Anlage zum Gerät.
   `/api/device/v1/`, Contract-Header je Anfrage, no-store je Antwort.
   Läufer-Token überall **außer `pair`**, dort kein Authorization-Header.
   Standard LAN/Tunnel, öffentlich nur Opt-in mit Allowlist und Bremse (C2).
2. Kopplung ersetzt Token per stdin: Code 15 min/einmalig/≥50 Bit,
   Crockford-Gruppen, nach 5 Fehlversuchen verbrannt, globale 429-Bremse.
   E3: nur gültiges HTTPS oder echter Loopback; selbstsigniert nur mit
   `--ca`/`--fingerprint`, kein Abschalten der Prüfung. Ed25519-Key per TOFU
   speichern, Rotation v1 nur neue Kopplung (E2). Widerruf: 401 `revoked`
   ohne message; Credential, Pakete/Umgebungen, Manifest und Journal löschen.
3. E1: Geräte-Manifest mit Ed25519 über exakt kanonische Bytes prüfen:
   rekursiv sortierte UTF-8-Schlüssel, kein äußerer Leerraum/BOM/LF,
   Zahlentoken verlustfrei; genaue Escape-Regeln im Vertrag. Fremde Geräte-ID,
   alte/gleiche seq oder falsche Signatur → Klasse `manifest`; alter Stand
   und letzter erfolgreicher Sync bleiben erhalten. seq uint64, dauerhaft,
   ab 1, je erfolgreichem Sync erhöht, auch bei gleichen Pins.
4. JSON streng mit `DisallowUnknownFields`. Heartbeat ausschließlich
   `contract`, `ready`, `runner_version`, `manifest_seq`; kein Sitzungsstatus,
   keine Leerlaufzeit, kein Konto. Antwort ergänzt `manifest_seq_current`;
   bei höherem Wert Manifest holen. Ergebnis hat kein Ausgabefeld.
5. Takt auf dem Gerät: 1 min vor jedem geplanten Lauf Manifest und bei
   neuem Commit Paket voll prüfen. Offline nur ohne declared_tools und
   `jetzt − letzter erfolgreicher Sync < grace_hours` (1–72 h), sonst
   `refused/offline`. Werkzeuge brauchen Box/Lauf-Token. Deterministischer
   `run_key = pin/schedule/UTC-Slot`; Online-Start mit Werkzeugen über
   `runs/start`, Standbindung per `X-Jnpt-Manifest-Seq`, veraltet 409 manifest.
   Ad-hoc bleibt claim → jobs/start; Job liefert seine Freigabe-manifest_seq.
6. Ein Platz je Gerät für beide Laufarten. Zweiter Claim sofort 204,
   geplanter Start bei anderem Lauf 409 busy; zweiter Start desselben Laufs
   409 already_started ohne Token. MCP verlangt beide Token, zustandslos.
7. Pakete nur für `(commit,path)` eines offenen eigenen Auftrags **oder**
   eines aktuellen eigenen Manifest-Pins, damit Sync ohne Claim funktioniert.
   Unbekannt/fremd byte-gleiche 404. Git-IDs/exakte Objektmenge vor jeder
   Ablage prüfen; fehlend/überzählig, 120000/160000, unsichere Namen oder
   Grenzen → refused/hash, kein Prozess. SHA-1DC statt crypto/sha1 (C3).
8. Drei Hash-Stufen: Git, Wheel-SHA-256 gegen Lock, Umgebungs-Manifest
   (Pfad/Größe/SHA-256) nach Entpacken und vor jedem Start. Python-ZIP
   zusätzlich gegen ausgelieferte python.org-Allowlist (C1), nicht aus Pin;
   fehlend/falsch → refused/runtime. Paket-tools = signierte declared_tools.
9. E4: DPAPI-CurrentUser-Journal zuerst für alle Ergebnisse, höchstens
   200 Ergebniseinträge/16 MiB gespeicherter Bestand inkl. DPAPI-/Rahmen-Overhead,
   Laufmarker und Zähler; ältester Ergebniseintrag fällt mit dauerhaftem Zähler raus.
   Verschlüsselte Laufmarker überleben Neustart; offen → interrupted,
   nie erneut starten. Keine Klartext-state.json. runs/result trägt genau
   run_key oder job_id, Freigabe-manifest_seq, Commit, Offline-Flag und
   Abschlusszeit. Box prüft historische Freigabe und führt vetLog aus.
   Erstes gewinnt dauerhaft, gleich 204/abweichend 409; erst nach 204 löschen
   (Ausnahmen gezählte Verdrängung und Widerruf). Kein Tokenablauf-Cutoff.
10. Box hält Ad-hoc-Aufträge persistent, löscht Eingabe bei Ende; Claim nach
    Box-Neustart funktioniert. Auch Start-/Ergebnis-/Manifesthistorie bleibt;
    kein pauschales Neustart-404 mit Verwerfen. Contract 1 und Paketversion 1,
    Updates zuerst Kern, dann Läufer; geschlossene Klassen inkl. offline.

## Benannte Grenzen

Widerruf wird offline erst beim Kontakt sichtbar, bis zur Gnadenfrist kann
das Gerät weiterlaufen. Ed25519-Rotation v1 nur neue Kopplung. Workstation-
Skripte handeln mit den Rechten der Person. DPAPI CurrentUser schützt nicht
gegen jemanden mit Zugriff auf diese Sitzung (C4).

## Prüfen und Grenzen von D0

Im Ordner `contract/`: `go test -count=1 ./...`, `go vet ./...`, `go mod verify`.
Generator für Pakete und Testsignatur: `go test -run '^TestGenerateGolden$' -update -v`; anschließend
SHA256SUMS-Pin im Test bewusst prüfen und nachziehen. Normale Tests schreiben
keine Referenzdateien. `.gitattributes` erhält deren Bytes auch unter Windows.

Die Negativtests lösen verbotene Daten am echten Artefaktleser aus; das ist
kein Nachweis am Laufzeitadapter. HTTP-, Prozess-, Netzwerk-, Konto-,
Zeit-/Slot-/Neustart-, Wheel-/ZIP-/Umgebungs-, E3-/Widerrufs-/DPAPI-/Kapazitäts-
und Laufzeit-Token-Sperren
benötigen in den Implementierungspaketen echte Adaptertests mit Mutationsprobe.
Keine Ersatzimplementierung des Läufers oder Mock-Abnahme in D0 einführen.
Abschlussbericht ausschließlich im Chat, keine Berichtsdatei im Repository.
