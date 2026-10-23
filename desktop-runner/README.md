# Desktop-Läufer

Status: **im Bau**. JanuaPort #1163 D0 legt den erweiterten Draht-Vertrag v1
und byte-genaue Referenzdaten fest: Kopplung, signiertes Geräte-Manifest,
lokaler Zeitplantakt und spätes Melden. Dieser Ordner enthält noch keinen Läufer,
HTTP-Adapter, Installer oder Dienst.

Verbindlich ist [der Vertrag](contract/README.md), ausschließlich für Pins
am Ausführungsort `geraet:<id>`; `lokal` gehört nicht zu diesem Geräteweg.
Das Gerät holt alles bei der Anlage ab (Weg B), koppelt sich ohne Bearer
über E3-gesichertes `pair` und prüft Manifest-Signatur, Gerät und steigende seq.
Werkzeuglose Zeitpläne dürfen innerhalb der Gnadenfrist offline starten;
Werkzeuge brauchen die Box und ein Lauf-Token. Ergebnisse gehen zuerst in
das DPAPI-Journal und werden mit Manifeststand/Commit idempotent nachgeliefert.

Benannte Grenzen: Widerruf wird offline erst bei Kontakt sichtbar;
Ed25519-Rotation in v1 nur durch neue Kopplung. Bei `workstation` handelt das
Skript mit den Rechten der Person; DPAPI CurrentUser schützt nicht gegen
Zugriff auf deren Sitzung.

Der einzige Go-Code ist der Artefakttest samt Paket-/Signaturgenerator in einem eigenen
Modul unter `contract/`: dort `go test ./...` und `go vet ./...` ausführen.
Prüfumfang und noch ausstehende Adapterprüfungen stehen im Vertrag.
