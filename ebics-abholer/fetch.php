<?php
// Der taegliche camt.053-Abruf (#549) — das Herzstueck des Beistellers.
//
// Aufruf (so ruft ihn der systemd-Timer):
//     php /app/fetch.php
//
// Nachzug von Hand fuer einen bestimmten Zeitraum:
//     php /app/fetch.php --von=2026-08-01 --bis=2026-08-05
//
// Exit-Codes (fuer den Timer, siehe ebicsExitCode() in logik.php):
//     0  geholt — oder die Bank hatte nichts Neues
//     1  Konfiguration/Umgebung unbrauchbar, es wurde nichts abgerufen
//     2  echter Fehler beim Abruf (Bank, Netz, Protokoll)
//     3  abgerufen, aber nicht abgelegt — negativ quittiert, die Bank behaelt
//        die Daten, der naechste Lauf holt sie erneut
//     4  die Bankschluessel passen nicht mehr (EBICS 091008) — nichts
//        abgerufen, nichts quittiert; naechster Handgriff: hpb.php --pruefen
//
// ⚠️ PROTOKOLL: Hier wird NIE ein Kontoumsatz ausgegeben. Der Lauf sagt, wie
// viele Dateien er abgelegt hat und wie sie heissen — mehr nicht. Auch der
// Default-Logger der Bibliothek (ArrayLogger) schreibt nirgendwohin; er wird
// bewusst nicht ersetzt.

declare(strict_types=1);

require_once __DIR__ . '/vendor/autoload.php';
require_once __DIR__ . '/logik.php';
require_once __DIR__ . '/version.php';
require_once __DIR__ . '/start.php';

use EbicsApi\Ebics\Contexts\BTDContext;
use EbicsApi\Ebics\Contexts\FDLContext;
use EbicsApi\Ebics\Contexts\RequestContext;
use EbicsApi\Ebics\Contracts\EbicsClientInterface;
use EbicsApi\Ebics\Contracts\Order\DownloadOrderInterface;
use EbicsApi\Ebics\EbicsClient;
use EbicsApi\Ebics\Exceptions\EbicsResponseException;
use EbicsApi\Ebics\Models\DownloadTransaction;
use EbicsApi\Ebics\Orders\BTD;
use EbicsApi\Ebics\Orders\FDL;
use EbicsApi\Ebics\Services\FileKeyringManager;
use EbicsApi\Ebics\Services\ZipArchiveExtractor;

/**
 * Der Abruf-Auftrag zur konfigurierten Version.
 *
 * H005 (EBICS 3.0) kennt keine Auftragsarten mehr: Derselbe Abruf laeuft als
 * BTD mit der BTF-Kennung Service „EOP" / Geltungsbereich „DE" / Nachricht
 * „camt.053", gebuendelt in einem ZIP-Container.
 *
 * H004 (EBICS 2.5) ist der Rueckfall — dort ist es der klassische Datei-Abruf
 * (FDL) mit Dateiformat und Laenderkennung.
 *
 * ⚠️ Beide Kennungen sind konfigurierbar. Verbindlich beantwortet die Frage
 * nicht die Hotline, sondern die Bank selbst: HAA und HTD nach der
 * Freischaltung (Runbook).
 */
function ebicsAbrufAuftrag(array $cfg, ?array $fenster, RequestContext $kontext): DownloadOrderInterface
{
    [$von, $bis] = $fenster ?? [null, null];

    if (ebicsVersionKennung($cfg) === 'H005') {
        $btf = new BTDContext();
        $btf->setServiceName(ebicsWert($cfg, 'btf_service', 'EOP'));
        $btf->setScope(ebicsWert($cfg, 'btf_scope', 'DE'));
        $btf->setMsgName(ebicsWert($cfg, 'btf_msg_name', 'camt.053'));
        $btf->setContainerType(ebicsWert($cfg, 'btf_container', 'ZIP'));

        // Fassung und Option nur setzen, wenn die Bank sie verlangt — ein
        // leeres Element im Auftrag ist etwas anderes als ein fehlendes.
        if (ebicsWert($cfg, 'btf_msg_name_version', '') !== '') {
            $btf->setMsgNameVersion(ebicsWert($cfg, 'btf_msg_name_version', ''));
        }
        if (ebicsWert($cfg, 'btf_service_option', '') !== '') {
            $btf->setServiceOption(ebicsWert($cfg, 'btf_service_option', ''));
        }

        // TEXT: Die Bibliothek soll die Antwort NICHT nachbearbeiten. Das
        // Zerlegen passiert in der Quittungs-Schleife unten — also VOR der
        // Quittung an die Bank (siehe Kommentar dort).
        $btf->setParserFormat(EbicsClientInterface::FILE_PARSER_FORMAT_TEXT);

        return new BTD($btf, $von, $bis, $kontext);
    }

    $fdl = new FDLContext();
    $fdl->setFileFormat(ebicsWert($cfg, 'h004_file_format', 'camt.053'));
    $fdl->setCountryCode(strtoupper(ebicsWert($cfg, 'h004_land', 'DE')));
    $fdl->setParserFormat(EbicsClientInterface::FILE_PARSER_FORMAT_TEXT);

    return new FDL($fdl, $von, $bis, $kontext);
}

/** Ein optionaler Konfigurationswert mit Vorgabe. */
function ebicsWert(array $cfg, string $schluessel, string $default): string
{
    $wert = trim((string)($cfg[$schluessel] ?? ''));

    return $wert === '' ? $default : $wert;
}

/**
 * Die Dokumente ablegen — atomar und ohne je etwas zu ueberschreiben.
 *
 * Geschrieben wird zuerst unter einem versteckten Zwischennamen und dann
 * umbenannt: Das Verzeichnis wird von JanuaPort gelesen, und eine halb
 * geschriebene XML-Datei unter ihrem Endnamen waere ein kaputter Auszug im
 * Posteingang. `rename` innerhalb desselben Verzeichnisses ist atomar.
 *
 * Scheitert eine Datei, werden die bereits geschriebenen dieses Laufs wieder
 * entfernt: entweder alle oder keine — kein Halbzustand.
 *
 * @param list<string> $inhalte
 * @return list<string> die geschriebenen Pfade
 */
function ebicsAblegen(string $verzeichnis, array $inhalte, string $datum): array
{
    $geschrieben = [];

    try {
        foreach ($inhalte as $index => $inhalt) {
            $pfad = ebicsFreierPfad($verzeichnis, ebicsDateiname($datum, $index + 1), 'file_exists');
            $zwischen = $verzeichnis . '/.' . basename($pfad) . '.teil';

            // Unterdrueckt, damit die MELDUNG unsere ist und nicht eine
            // PHP-Warnung im journald — der Grund reist als Klartext mit.
            if (@file_put_contents($zwischen, $inhalt) === false) {
                throw new EbicsAblageFehler("Konnte nicht schreiben: $zwischen (" . ebicsLetzterFehler() . ')');
            }
            @chmod($zwischen, 0644);

            if (!@rename($zwischen, $pfad)) {
                @unlink($zwischen);
                throw new EbicsAblageFehler("Konnte nicht ablegen als $pfad (" . ebicsLetzterFehler() . ')');
            }

            $geschrieben[] = $pfad;
        }
    } catch (Throwable $e) {
        foreach ($geschrieben as $pfad) {
            @unlink($pfad);
        }
        throw $e;
    }

    return $geschrieben;
}

/** Das Ablage-Verzeichnis muss benutzbar sein, BEVOR die Bank etwas liefert. */
function ebicsAblageVerzeichnisPruefen(string $verzeichnis): void
{
    if (!is_dir($verzeichnis)) {
        throw new EbicsKonfigFehler("Ablage-Verzeichnis fehlt: $verzeichnis (Mount pruefen).");
    }

    if (!is_writable($verzeichnis)) {
        throw new EbicsKonfigFehler(
            "Ablage-Verzeichnis ist nicht beschreibbar: $verzeichnis — es muss UID 65532 gehoeren."
        );
    }
}

function ebicsAbholen(array $argumente): void
{
    // Zuerst alles pruefen, was ohne die Bank pruefbar ist. Ein Lauf, der
    // Daten holt, quittiert — und dann merkt, dass sie nirgends hinkoennen,
    // waere der teuerste aller Fehler.
    $fenster = ebicsZeitfensterAusArgumenten($argumente);
    $cfg = ebicsKonfigLaden();
    $ablage = rtrim((string)$cfg['ablage_verzeichnis'], '/');
    ebicsAblageVerzeichnisPruefen($ablage);

    $manager = new FileKeyringManager();
    $keyring = ebicsKeyringLaden($cfg, $manager);
    $bank = ebicsBank($cfg);
    $teilnehmer = ebicsTeilnehmer($cfg);
    ebicsApplyVersionSpecifics($cfg, $keyring, $bank);
    $client = new EbicsClient($bank, $teilnehmer, $keyring);

    $datum = (new DateTimeImmutable())->format('Y-m-d');
    $abgelegt = [];
    $ablageFehler = null;

    $kontext = new RequestContext();

    // ⚠️ DIE WICHTIGSTE STELLE DIESES SKRIPTS.
    //
    // EBICS ist ein quittiertes Verfahren: Erst die Quittung sagt der Bank
    // „angekommen, du kannst es aus dem Vorrat nehmen". Diese Schleife laeuft
    // VOR der Quittung — hier wird abgelegt, und erst wenn jede Datei liegt,
    // gibt es ein `true`.
    //
    // Ein `false` ist eine NEGATIVE Quittung: Die Bank fuehrt die Daten
    // weiter als neu, und der naechste Lauf bekommt sie erneut. Genau das
    // meint „nichts geht verloren". Wuerde erst nach `executeDownloadOrder`
    // geschrieben, waere der Auszug bei einer vollen Platte weg — quittiert,
    // aber nie abgelegt.
    $kontext->setAckClosure(
        function (DownloadTransaction $transaktion) use (&$abgelegt, &$ablageFehler, $ablage, $datum): bool {
            try {
                $inhalte = ebicsInhalte(
                    $transaktion->getOrderData(),
                    static fn(string $roh): array => (new ZipArchiveExtractor())->extractFilesFromString($roh)
                );
                $abgelegt = ebicsAblegen($ablage, $inhalte, $datum);

                return true;
            } catch (Throwable $e) {
                $ablageFehler = $e;

                return false;
            }
        }
    );

    $client->executeDownloadOrder(ebicsAbrufAuftrag($cfg, $fenster, $kontext));

    if ($ablageFehler !== null) {
        throw new EbicsAblageFehler(
            'Abgerufen, aber nicht abgelegt — Empfang NEGATIV quittiert, die Bank fuehrt die Daten weiter '
            . 'als neu. Ursache: ' . $ablageFehler->getMessage()
        );
    }

    echo ebicsLogSatz(array_map('basename', $abgelegt)), "\n";
}

// ── Ablauf ──────────────────────────────────────────────────────────────────
//
// ⚠️ Nur, wenn diese Datei DIREKT aufgerufen wurde. Ohne diese Schranke
// spraeche allein das Einbinden von fetch.php (die Teststrecke tut das, um die
// Ablage gegen ein echtes Dateisystem zu fahren) mit einer Bank.

if (PHP_SAPI !== 'cli' || !isset($argv[0]) || realpath($argv[0]) !== __FILE__) {
    return;
}

try {
    ebicsAbholen(array_slice($argv, 1));
    exit(0);
} catch (Throwable $e) {
    $ebicsCode = $e instanceof EbicsResponseException ? $e->getResponseCode() : null;
    $exitCode = ebicsExitCode(get_class($e), $ebicsCode);

    if ($exitCode === 0) {
        // Der haeufigste Ausgang eines taeglichen Laufs: Wochenende, Feiertag,
        // oder heute schon geholt. Kein Fehler — ein Dienst, der sonntags rot
        // wird, erzieht Betreiber dazu, rote Dienste zu ignorieren.
        echo ebicsLogSatz([]), "\n";
        exit(0);
    }

    if ($exitCode === 4) {
        // ⚠️ Bewusst NICHT $e->getMessage(): Der Fehlerpfad der Bibliothek
        // fuehrt den Report-Text der Bank mit, und der gehoert nicht ins
        // journald. Unsere Meldung sagt statt dessen, was zu tun ist.
        fwrite(STDERR, ebicsSchluesselwechselMeldung() . "\n");
        exit(4);
    }

    fwrite(STDERR, $e->getMessage() . "\n");
    exit($exitCode);
}
