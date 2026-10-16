# Agenten-Läufer 0.1.0

**Gebaut, Live-Durchstich ausstehend.** Der Beisteller führt ein vom Kern
freigegebenes Agenten-Profil aus: Modell aufrufen, Werkzeuge ausführen,
Ergebnisse anhängen. Modell und Werkzeuge laufen über JanuaPort. Der Läufer
kennt den Modell-Schlüssel nicht und hat im ausgelieferten Netz keinen
Internet-Ausgang. Apache 2.0, siehe [LICENSE](../LICENSE).

**Grenze in 0.1.0:** Der Läufer baut nur Anfragen im Format der Anthropic Messages API. Ein Agenten-Profil, das auf eine openai-kompatible Modell-Anbindung zeigt (OpenAI, Azure, Mistral, Ollama), scheitert in 0.1.0, weil der Kern die Anfrage abweist. Der OpenAI-Adapter folgt in einer späteren Version.

## Start

Voraussetzung: ein Kern mit Agenten-Relay und Modell-Durchgang aus
JanuaPort/januaport#1099. Er verwaltet das Docker-Netz `jnpt_plugins` mit
`internal: true` und stellt den runtime-key in
`/run/jnpt/plugins/agent-runner/runtime-key` bereit. Ordner und Datei müssen
für UID/GID 65532 lesbar sein; der Schlüsselordner wird nur lesend gemountet.
Der Ordner-Mount unterstützt atomare Schlüsselrotation.

```sh
docker build -t agent-runner:0.1.0 .
export JNPT_AGENT_RUNNER_IMAGE=agent-runner:0.1.0
docker compose -f docker-compose.yml up -d
docker compose -f docker-compose.yml ps
```

Für eine Anlage ein geprüftes Image per Digest in `.env` hinterlegen. Die
Release-Strecke `.github/workflows/agent-runner-release.yml` startet nur mit
`agent-runner-vX.Y.Z`: Tests → ein Build für `linux/amd64` mit SBOM und
Provenance → Sonden gegen den gepushten Digest samt Config-Digest-Vergleich
(SEC C1) → Image-Tag `X.Y.Z` auf denselben Digest. Trivy liefert einen Bericht
als Artefakt, kein Gate. Die Kuratierung unter 0.1.0 in `CHANGELOG.md` füllt
das Gate nach dem Release aus, einschließlich Prüfung mit JanuaPort v0.80.0.
Ein veröffentlichtes Image und ein Katalogeintrag stehen noch aus.

| Variable | Standard | Bedeutung |
|---|---|---|
| `JNPT_AGENT_RUNNER_IMAGE` | Pflicht in Compose | Gebautes Image oder kuratierter Digest |
| `JNPT_GATEWAY_URL` | `http://jnpt:8484` | Kern-Adresse ohne Pfad, Query oder Zugangsdaten |
| `JNPT_AGENT_MAX_WALL_S` | `300` | Prozessoption 1–300 Sekunden (0 = Standard); im ausgelieferten Compose bleibt der Standard |

## Internes Netz und Gesundheit

Der Dienst hängt ausschließlich an `jnpt_plugins`. Compose verwendet
`external: true`, weil das Netz dem Kern gehört; es muss dort **internal**
sein. Keine Port-Veröffentlichung. Der MCP-Endpunkt liegt intern unter
`http://agent-runner:8090/mcp`, `/healthz` auf demselben Port. Der
Docker-Healthcheck nutzt `/agent-runner healthcheck`: lesbarer, nichtleerer
runtime-key → `ok`/200, sonst `not ready`/503. Das bestätigt die lokale
Bereitschaft, nicht die Verfügbarkeit des Kerns oder Modellanbieters.

Das Image läuft statisch in distroless/static als 65532:65532, mit read-only
Root-FS, ohne Linux-Capabilities und mit no-new-privileges. Laufzeitgrenzen
der Compose: 256 MiB, 64 PIDs. Der HTTP-Client folgt keinen Weiterleitungen
und verwendet keinen Proxy aus der Umgebung. Eine Netzfreigabe ist in B1
nicht vorgesehen.

## Grenzen und Protokoll

Schritte und Werkzeugaufrufe werden nach den Anfragegrenzen im Runner
gezählt. Die Wandzeit umfasst Werkzeugsuche, Modell- und Werkzeugaufrufe;
sie ist das Minimum aus Anfrage und eigener Meldung (Standard 300 s).
Die Token-Budgets verwaltet der Kern. Beendet der Kern den Relay-Kontext,
endet der laufende HTTP-Aufruf ebenfalls.
Vor dem Schließen der MCP-Sitzung wird der Lauf-Kontext abgebrochen; das
SDK-Cleanup kann den Lauf dadurch nicht verlängern. Der HTTP-Transport
begrenzt Antworten auf Modell- und MCP-Pfad auf 4 MiB, unabhängig von der
Wandzeit.

Bilder werden nur als base64 mit `image/png`, `image/jpeg`, `image/gif`
oder `image/webp` weitergegeben. Andere Bild-Medientypen und URL-Bilder aus
Werkzeugergebnissen werden zum festen Platzhalter aus dem Draht-Vertrag.

Eine JSON-Zeile je Lauf auf stdout: `steps`, `tool_calls`, `tools`, `end`,
`duration_ms`. Darin stehen **keine Inhalte**: keine Eingabe, Anweisungen,
Modellantworten, Werkzeugargumente/-ergebnisse oder Token. Werkzeugnamen
sind Teil dieses Betriebsprotokolls. Ende-Klassen: `ok`, `limit`, `cancelled`,
`revoked`, `error`. Fehler liefern feste Sätze; Text des Durchgangs wird
verworfen. Bei abgebrochener Verbindung bleibt das Protokoll der Nachweis
des Endes, eine HTTP-Antwort kann dann ausbleiben.
Für `error/rejected` lautet der feste Satz: „Der Modell-Durchgang hat die
Anfrage des Agenten-Läufers abgewiesen.“

Vertrag und neue Ende-Beispiele: [contract/README.md](contract/README.md).
Die Tests benutzen ausschließlich einen lokalen Fake-Kern, keine Live-Modellaufrufe.
Mutationsproben: `probe/mutations.py` (Bericht nach `<tmp>/agent-runner-mutations/`).
Die Release-Strecke verwendet die vorhandene `probe/smoke.ps1` mit `pwsh`
auf Ubuntu: UID 65532, read-only Root-FS, `/healthz`, genau ein internes Netz,
kein Host-Port und ein Testserver in einem fremden Netz (mit erreichbarer
Positivkontrolle). Lokal: `pwsh -File probe/smoke.ps1 -Image agent-runner:test`.
Der temporäre Zustand unter `probe/.smoke/` ist ignoriert und wird aufgeräumt.
