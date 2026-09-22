<?php
// Teststrecke des EBICS-Abholers (#549).
//
// Bewusst KEIN PHPUnit: Der Abholer ist ein Beisteller aus sechs kleinen
// Skripten; ein Testrahmen mit eigenem Autoloader und eigener Konfiguration
// waere hier schwerer als das Getestete. Dieser Runner braucht nur `php`.
//
// ⚠️ Er laeuft OHNE vendor/ — `logik.php` haengt bewusst an keiner Bibliothek.
// Damit sind die Regeln pruefbar, ohne dass irgendetwas installiert oder gar
// eine Bank angesprochen werden muesste. **Es gibt in dieser Strecke keinen
// einzigen Netzwerkaufruf**, und es darf nie einen geben.
//
// Aufruf:  php tests/run.php        (Exit 0 = alles gruen, 1 = mindestens ein Fall rot)

declare(strict_types=1);

require __DIR__ . '/../logik.php';

// ── Mini-Rahmen ─────────────────────────────────────────────────────────────

$GLOBALS['jnpt_gruen'] = 0;
$GLOBALS['jnpt_rot']   = [];

function pruefe(string $name, callable $fall): void
{
    try {
        $fall();
        $GLOBALS['jnpt_gruen']++;
        echo "  ok   $name\n";
    } catch (Throwable $e) {
        $GLOBALS['jnpt_rot'][] = $name . ': ' . $e->getMessage();
        echo "  ROT  $name\n       " . $e->getMessage() . "\n";
    }
}

function gleich(mixed $erwartet, mixed $ist, string $was = ''): void
{
    if ($erwartet !== $ist) {
        throw new RuntimeException(sprintf(
            '%serwartet %s, bekommen %s',
            $was !== '' ? $was . ': ' : '',
            var_export($erwartet, true),
            var_export($ist, true)
        ));
    }
}

function wahr(bool $bedingung, string $was): void
{
    if (!$bedingung) {
        throw new RuntimeException($was);
    }
}

function wirft(string $klasse, callable $fall, string $was = ''): Throwable
{
    try {
        $fall();
    } catch (Throwable $e) {
        if (!($e instanceof $klasse)) {
            throw new RuntimeException(
                ($was !== '' ? $was . ': ' : '') . "erwartet $klasse, bekommen " . get_class($e)
            );
        }
        return $e;
    }
    throw new RuntimeException(($was !== '' ? $was . ': ' : '') . "erwartet $klasse, aber nichts geworfen");
}

function skriptText(string $datei): string
{
    $pfad = __DIR__ . '/../' . $datei;
    if (!is_file($pfad)) {
        throw new RuntimeException("Skript fehlt: $datei");
    }
    return (string)file_get_contents($pfad);
}

/** Kommentarzeilen weg — Waechtertests pruefen CODE, nicht Prosa. */
function skriptCode(string $datei): string
{
    $zeilen = preg_split('/\R/', skriptText($datei)) ?: [];
    $code = [];
    foreach ($zeilen as $zeile) {
        $ohneKommentar = preg_replace('~//.*$~', '', $zeile) ?? $zeile;
        $code[] = $ohneKommentar;
    }
    return implode("\n", $code);
}

function vollstaendigeKonfig(array $ueberschreiben = []): array
{
    return $ueberschreiben + [
        'host_id'            => 'BANKHOST',
        'host_url'           => 'https://ebics.example.test/ebicsweb',
        'partner_id'         => 'K1234567',
        'user_id'            => 'T7654321',
        'ebics_version'      => 'H005',
        'keyring_password'   => 'geheim',
        'ablage_verzeichnis' => '/ablage',
    ];
}

// ── 1. Versionswahl (H005/H004) ─────────────────────────────────────────────

echo "\nVersionswahl\n";

pruefe('H005 wird erkannt', function () {
    gleich('H005', ebicsVersionKennung(['ebics_version' => 'H005']));
});

pruefe('H004 wird erkannt', function () {
    gleich('H004', ebicsVersionKennung(['ebics_version' => 'H004']));
});

pruefe('Kleinschreibung und Leerzeichen stoeren nicht', function () {
    gleich('H005', ebicsVersionKennung(['ebics_version' => ' h005 ']));
});

pruefe('H003 wird abgewiesen (der Abholer kann nur H004/H005)', function () {
    wirft(EbicsKonfigFehler::class, fn() => ebicsVersionKennung(['ebics_version' => 'H003']));
});

pruefe('Fehlende Version wird abgewiesen', function () {
    wirft(EbicsKonfigFehler::class, fn() => ebicsVersionKennung([]));
});

pruefe('Der Platzhalter aus der Vorlage wird abgewiesen', function () {
    wirft(EbicsKonfigFehler::class, fn() => ebicsVersionKennung(['ebics_version' => 'HIER-H005-ODER-H004']));
});

// ── 2. Konfigurations-Pruefung ──────────────────────────────────────────────

echo "\nKonfigurations-Pruefung\n";

pruefe('Vollstaendige Konfiguration ist fehlerfrei', function () {
    gleich([], ebicsKonfigFehler(vollstaendigeKonfig()));
});

pruefe('Fehlender Pflichtwert wird beim Namen genannt', function () {
    $cfg = vollstaendigeKonfig();
    unset($cfg['host_id']);
    $fehler = ebicsKonfigFehler($cfg);
    gleich(1, count($fehler), 'Anzahl Fehler');
    wahr(str_contains($fehler[0], 'host_id'), 'Fehlertext nennt den Schluessel: ' . $fehler[0]);
});

pruefe('Nicht ausgefuellter Platzhalter zaehlt als Fehler', function () {
    $fehler = ebicsKonfigFehler(vollstaendigeKonfig(['partner_id' => 'HIER-KUNDEN-ID']));
    gleich(1, count($fehler), 'Anzahl Fehler');
    wahr(str_contains($fehler[0], 'partner_id'), 'Fehlertext nennt den Schluessel');
});

pruefe('Fehlendes Ablage-Verzeichnis ist ein Fehler', function () {
    $cfg = vollstaendigeKonfig();
    unset($cfg['ablage_verzeichnis']);
    $fehler = ebicsKonfigFehler($cfg);
    gleich(1, count($fehler), 'Anzahl Fehler');
    wahr(str_contains($fehler[0], 'ablage_verzeichnis'), 'Fehlertext nennt den Schluessel');
});

pruefe('Relatives Ablage-Verzeichnis wird abgewiesen', function () {
    $fehler = ebicsKonfigFehler(vollstaendigeKonfig(['ablage_verzeichnis' => 'ablage']));
    gleich(1, count($fehler), 'Anzahl Fehler');
    wahr(str_contains($fehler[0], 'absolut'), 'Fehlertext erklaert das Warum: ' . $fehler[0]);
});

pruefe('⚠️ Klartext-HTTP zur Bank wird abgewiesen', function () {
    $fehler = ebicsKonfigFehler(vollstaendigeKonfig(['host_url' => 'http://ebics.example.test/ebicsweb']));
    gleich(1, count($fehler), 'Anzahl Fehler');
    wahr(str_contains($fehler[0], 'https'), 'Fehlertext nennt https: ' . $fehler[0]);
});

pruefe('Unbrauchbare Version taucht als Konfigurationsfehler auf', function () {
    $fehler = ebicsKonfigFehler(vollstaendigeKonfig(['ebics_version' => 'H003']));
    gleich(1, count($fehler), 'Anzahl Fehler');
    wahr(str_contains($fehler[0], 'ebics_version'), 'Fehlertext nennt den Schluessel');
});

pruefe('Mehrere Fehler werden alle gemeldet, nicht nur der erste', function () {
    $cfg = vollstaendigeKonfig(['ebics_version' => 'H003', 'host_url' => 'http://x.test']);
    unset($cfg['user_id']);
    gleich(3, count(ebicsKonfigFehler($cfg)), 'Anzahl Fehler');
});

// ── 2b. Pfade zu Konfiguration und Schluesseldatei ──────────────────────────

echo "\nPfade\n";

pruefe('Ohne Umgebungsvariablen gilt das gemountete Geheimnis-Verzeichnis', function () {
    gleich('/geheim/config.php', ebicsKonfigPfad([]));
    gleich('/geheim/keyring.json', ebicsKeyringPfad([]));
});

pruefe('Die Umgebung darf beide Pfade umlenken (Initialisierung am Laptop)', function () {
    gleich('/app/config.php', ebicsKonfigPfad(['JNPT_EBICS_CONFIG' => '/app/config.php']));
    gleich('/app/keyring.json', ebicsKeyringPfad(['JNPT_EBICS_KEYRING' => '/app/keyring.json']));
});

pruefe('Ein leerer Wert zaehlt nicht als Umlenkung', function () {
    gleich('/geheim/config.php', ebicsKonfigPfad(['JNPT_EBICS_CONFIG' => '']));
    gleich('/geheim/keyring.json', ebicsKeyringPfad(['JNPT_EBICS_KEYRING' => '   ']));
});

// ── 3. Datumsfenster (nur fuer den Nachzug von Hand) ────────────────────────

echo "\nDatumsfenster\n";

pruefe('Ohne Argumente gibt es KEIN Fenster (taeglicher Lauf)', function () {
    gleich(null, ebicsZeitfensterAusArgumenten([]));
});

pruefe('Von/bis wird gelesen', function () {
    $fenster = ebicsZeitfensterAusArgumenten(['--von=2026-08-01', '--bis=2026-08-05']);
    wahr(is_array($fenster), 'Fenster ist ein Array');
    gleich('2026-08-01', $fenster[0]->format('Y-m-d'));
    gleich('2026-08-05', $fenster[1]->format('Y-m-d'));
});

pruefe('Ein einzelner Tag ist ein gueltiges Fenster', function () {
    $fenster = ebicsZeitfensterAusArgumenten(['--von=2026-08-01', '--bis=2026-08-01']);
    gleich('2026-08-01', $fenster[0]->format('Y-m-d'));
});

pruefe('Nur --von ist ein Fehler (halbes Fenster gibt es nicht)', function () {
    wirft(EbicsKonfigFehler::class, fn() => ebicsZeitfensterAusArgumenten(['--von=2026-08-01']));
});

pruefe('Nur --bis ist ein Fehler', function () {
    wirft(EbicsKonfigFehler::class, fn() => ebicsZeitfensterAusArgumenten(['--bis=2026-08-01']));
});

pruefe('Verdrehtes Fenster wird abgewiesen', function () {
    wirft(EbicsKonfigFehler::class, fn() => ebicsZeitfensterAusArgumenten(['--von=2026-08-05', '--bis=2026-08-01']));
});

pruefe('Unlesbares Datum wird abgewiesen', function () {
    wirft(EbicsKonfigFehler::class, fn() => ebicsZeitfensterAusArgumenten(['--von=01.08.2026', '--bis=2026-08-05']));
});

pruefe('Kalendarisch unmoegliches Datum wird abgewiesen', function () {
    wirft(EbicsKonfigFehler::class, fn() => ebicsZeitfensterAusArgumenten(['--von=2026-02-30', '--bis=2026-03-01']));
});

pruefe('Unbekanntes Argument wird abgewiesen statt still ignoriert', function () {
    wirft(EbicsKonfigFehler::class, fn() => ebicsZeitfensterAusArgumenten(['--alles']));
});

// ── 4. Container zerlegen ───────────────────────────────────────────────────

echo "\nContainer zerlegen\n";

pruefe('ZIP wird an der Signatur erkannt', function () {
    wahr(ebicsIstZip("PK\x03\x04irgendwas"), 'PK-Signatur');
    wahr(!ebicsIstZip('<?xml version="1.0"?><Document/>'), 'XML ist kein ZIP');
    wahr(!ebicsIstZip(''), 'leer ist kein ZIP');
});

pruefe('ZIP wird entpackt, Reihenfolge bleibt', function () {
    $entpacker = fn(string $roh) => ['b.xml' => '<b/>', 'a.xml' => '<a/>'];
    gleich(['<b/>', '<a/>'], ebicsInhalte("PK\x03\x04...", $entpacker));
});

pruefe('Kaputte und leere ZIP-Eintraege fliegen raus', function () {
    $entpacker = fn(string $roh) => ['a.xml' => '<a/>', 'kaputt.xml' => false, 'leer.xml' => '', 'c.xml' => '<c/>'];
    gleich(['<a/>', '<c/>'], ebicsInhalte("PK\x03\x04...", $entpacker));
});

pruefe('Nicht-ZIP kommt als genau ein Inhalt zurueck (H004-Fall)', function () {
    $entpacker = function () {
        throw new RuntimeException('der Entpacker darf hier gar nicht gerufen werden');
    };
    gleich(['<Document/>'], ebicsInhalte('<Document/>', $entpacker));
});

pruefe('Leere Rohdaten ergeben keine Datei', function () {
    gleich([], ebicsInhalte('   ', fn() => []));
});

// ── 5. Dateinamen ───────────────────────────────────────────────────────────

echo "\nDateinamen\n";

pruefe('Name wird aus Datum und laufender Nummer gebildet', function () {
    gleich('camt053-2026-08-20-01.xml', ebicsDateiname('2026-08-20', 1));
    gleich('camt053-2026-08-20-12.xml', ebicsDateiname('2026-08-20', 12));
});

pruefe('⚠️ Der Name kommt NIE von der Bank', function () {
    // Ein Eintragsname aus dem ZIP der Bank ist Fremdeingabe. Er darf den
    // Ablagepfad nicht beeinflussen — sonst genuegt ein Eintrag namens
    // "../../etc/cron.d/boese", um aus dem Ablage-Verzeichnis auszubrechen.
    $name = ebicsDateiname('2026-08-20', 1);
    wahr(!str_contains($name, '/') && !str_contains($name, '\\'), 'kein Pfadtrenner im Namen');
    wahr(!str_contains($name, '..'), 'kein Auf-/Abstieg im Namen');
    wahr((bool)preg_match('/^camt053-\d{4}-\d{2}-\d{2}-\d{2,}\.xml$/', $name), "Name folgt der Form: $name");
});

pruefe('Freier Name bleibt unveraendert', function () {
    gleich('/ablage/camt053-2026-08-20-01.xml', ebicsFreierPfad('/ablage', 'camt053-2026-08-20-01.xml', fn() => false));
});

pruefe('⚠️ Belegter Name wird nummeriert, NIE ueberschrieben', function () {
    $belegt = ['/ablage/camt053-2026-08-20-01.xml' => true];
    gleich(
        '/ablage/camt053-2026-08-20-01-2.xml',
        ebicsFreierPfad('/ablage', 'camt053-2026-08-20-01.xml', fn(string $p) => isset($belegt[$p]))
    );
});

pruefe('Auch die Nummerierung weicht weiter aus', function () {
    $belegt = [
        '/ablage/camt053-2026-08-20-01.xml'   => true,
        '/ablage/camt053-2026-08-20-01-2.xml' => true,
        '/ablage/camt053-2026-08-20-01-3.xml' => true,
    ];
    gleich(
        '/ablage/camt053-2026-08-20-01-4.xml',
        ebicsFreierPfad('/ablage', 'camt053-2026-08-20-01.xml', fn(string $p) => isset($belegt[$p]))
    );
});

pruefe('Ein volles Verzeichnis endet mit einem Fehler statt in einer Endlosschleife', function () {
    wirft(EbicsAblageFehler::class, fn() => ebicsFreierPfad('/ablage', 'x.xml', fn() => true));
});

// ── 6. Exit-Codes ───────────────────────────────────────────────────────────

echo "\nExit-Codes\n";

pruefe('Erfolgreicher Lauf ist 0', function () {
    gleich(0, ebicsExitCode(null, null));
});

pruefe('⚠️ "Keine neuen Daten" ist ERFOLG (Exception-Klasse)', function () {
    gleich(0, ebicsExitCode('EbicsApi\\Ebics\\Exceptions\\NoDownloadDataAvailableException', null));
});

pruefe('⚠️ "Keine neuen Daten" ist ERFOLG (EBICS-Code 090005)', function () {
    gleich(0, ebicsExitCode('EbicsApi\\Ebics\\Exceptions\\IrgendwasAnderes', '090005'));
});

pruefe('Konfigurationsfehler ist 1', function () {
    gleich(1, ebicsExitCode(EbicsKonfigFehler::class, null));
});

pruefe('Echter Bankfehler ist 2', function () {
    gleich(2, ebicsExitCode('EbicsApi\\Ebics\\Exceptions\\AuthenticationFailedException', '061001'));
});

pruefe('Gescheiterte Ablage ist 3 (Daten bleiben bei der Bank)', function () {
    gleich(3, ebicsExitCode(EbicsAblageFehler::class, null));
});

pruefe('⚠️ Bankschluesselwechsel ist Exit 4 (Fehlerklasse)', function () {
    // Der Fehler kommt von der BANK: In jedem Abruf-Auftrag reisen die
    // Pruefsummen der uns bekannten Bankschluessel mit. Passen sie nicht mehr,
    // bricht die Bank die Transaktionsinitialisierung ab — vor jedem
    // Datenfluss und vor jeder Quittung.
    gleich(4, ebicsExitCode('EbicsApi\\Ebics\\Exceptions\\BankPubkeyUpdateRequiredException', null));
});

pruefe('⚠️ Bankschluesselwechsel ist Exit 4 (EBICS-Code 091008)', function () {
    // Auch dann, wenn die Bibliothek den Code eines Tages einer anderen Klasse
    // zuordnet: Der Code ist die belastbarere Aussage.
    gleich(4, ebicsExitCode('EbicsApi\\Ebics\\Exceptions\\IrgendwasAnderes', '091008'));
});

pruefe('⚠️ 061001 bleibt Exit 2 — das ist der ANDERE Fehler', function () {
    // 091008: die Bank weist unsere Sicht auf IHRE Schluessel zurueck.
    // 061001: die Bank weist UNSERE Signatur zurueck. Zwei verschiedene
    // Handgriffe — sie duerfen nie denselben Exit-Code tragen.
    gleich(2, ebicsExitCode('EbicsApi\\Ebics\\Exceptions\\AuthenticationFailedException', '061001'));
    gleich(2, ebicsExitCode('EbicsApi\\Ebics\\Exceptions\\AuthenticationFailedException', null));
});

pruefe('Die Codes 0 bis 3 bleiben, wie sie waren', function () {
    gleich(0, ebicsExitCode(null, null));
    gleich(0, ebicsExitCode('EbicsApi\\Ebics\\Exceptions\\NoDownloadDataAvailableException', null));
    gleich(0, ebicsExitCode('EbicsApi\\Ebics\\Exceptions\\IrgendwasAnderes', '090005'));
    gleich(1, ebicsExitCode(EbicsKonfigFehler::class, null));
    gleich(3, ebicsExitCode(EbicsAblageFehler::class, null));
});

pruefe('Ein Leerlauf bleibt ein Leerlauf (Reihenfolge der Zweige)', function () {
    // 090005 ist ERFOLG und darf von keinem neuen Zweig eingefangen werden.
    gleich(0, ebicsExitCode('EbicsApi\\Ebics\\Exceptions\\NoDownloadDataAvailableException', '090005'));
});

// ── 7. Protokollzeile — nie Kontodaten ──────────────────────────────────────

echo "\nProtokollzeile\n";

pruefe('Die Protokollzeile nennt Anzahl und Dateinamen', function () {
    $satz = ebicsLogSatz(['camt053-2026-08-20-01.xml', 'camt053-2026-08-20-02.xml']);
    wahr(str_contains($satz, '2'), 'Anzahl steht drin');
    wahr(str_contains($satz, 'camt053-2026-08-20-01.xml'), 'Dateiname steht drin');
});

pruefe('⚠️ Die Protokollzeile kennt den Inhalt gar nicht', function () {
    // Sie nimmt ausschliesslich Namen entgegen — ein Umsatz kann strukturell
    // nicht hineingeraten, nicht nur "wird nicht hineingeschrieben".
    $r = new ReflectionFunction('ebicsLogSatz');
    gleich(1, $r->getNumberOfParameters(), 'genau ein Parameter');
    gleich('namen', $r->getParameters()[0]->getName(), 'und der heisst');
});

pruefe('Leerlauf wird als Leerlauf protokolliert', function () {
    $satz = ebicsLogSatz([]);
    wahr(str_contains($satz, '0'), 'Null steht drin: ' . $satz);
});

// ── 8. Schluesselwechsel der Bank (#617, rein) ──────────────────────────────

echo "\nSchluesselwechsel der Bank\n";

pruefe('Die Meldung nennt Ursache, naechsten Handgriff und Runbook', function () {
    $meldung = ebicsSchluesselwechselMeldung();
    wahr(str_contains($meldung, '091008'), 'der EBICS-Code steht drin');
    wahr(str_contains($meldung, 'Bankschluessel'), 'die Ursache steht drin');
    wahr(str_contains($meldung, '--pruefen'), 'der naechste Handgriff steht drin');
    wahr(str_contains($meldung, 'ebics-abholer.md'), 'das Runbook steht drin');
});

pruefe('⚠️ Die Meldung kann gar keinen Bank-Report-Text tragen', function () {
    // Der Fehlerpfad der Bibliothek fuehrt den Report-Text der Bank mit. Bei
    // Exit 4 gibt fetch.php DIESE Meldung aus, nicht $e->getMessage() — und
    // sie nimmt strukturell nichts entgegen, in das ein Report passte.
    $r = new ReflectionFunction('ebicsSchluesselwechselMeldung');
    gleich(0, $r->getNumberOfParameters(), 'kein Parameter');
});

pruefe('Unveraenderte Pruefsummen sind unveraendert', function () {
    $lage = ebicsPruefsummenLage(
        ['X002' => ['schluessel' => 'AA BB', 'zertifikat' => 'CC DD'], 'E002' => ['schluessel' => '11 22', 'zertifikat' => null]],
        ['X002' => ['schluessel' => 'AA BB', 'zertifikat' => 'CC DD'], 'E002' => ['schluessel' => '11 22', 'zertifikat' => null]]
    );
    gleich(false, $lage['geaendert']);
    gleich([], $lage['schluessel_geaendert']);
    gleich([], $lage['nur_zertifikat']);
});

pruefe('Geaenderte Bankschluessel werden erkannt', function () {
    $lage = ebicsPruefsummenLage(
        ['X002' => ['schluessel' => 'AA BB', 'zertifikat' => null], 'E002' => ['schluessel' => '11 22', 'zertifikat' => null]],
        ['X002' => ['schluessel' => 'FF EE', 'zertifikat' => null], 'E002' => ['schluessel' => '11 22', 'zertifikat' => null]]
    );
    gleich(true, $lage['geaendert']);
    gleich(['X002'], $lage['schluessel_geaendert']);
    gleich([], $lage['nur_zertifikat']);
});

pruefe('Darstellung ist egal — nur die Hex-Zeichen zaehlen', function () {
    // Bank-Schreiben drucken die Pruefsumme in Paaren, mal gross, mal klein,
    // manche mit Doppelpunkten. Verglichen werden die Zeichen.
    gleich('aabbcc', ebicsPruefsummeNormalisieren('AA BB CC'));
    gleich('aabbcc', ebicsPruefsummeNormalisieren('aa:bb:cc'));
    gleich('aabbcc', ebicsPruefsummeNormalisieren("  AaBb\ncc  "));

    $lage = ebicsPruefsummenLage(
        ['X002' => ['schluessel' => 'AA BB CC', 'zertifikat' => null]],
        ['X002' => ['schluessel' => 'aa:bb:cc', 'zertifikat' => null]]
    );
    gleich(false, $lage['geaendert']);
});

pruefe('⚠️ Gleiche Schluessel, neues Zertifikat gilt als geaendert', function () {
    // Fanggrube H005: An die Bank geht der ZERTIFIKATS-Fingerabdruck
    // (DigestResolverV3), nicht der Public-Key-Digest. Erneuert die Bank nur
    // ihr Zertifikat, waere der Schluessel „unveraendert" — und der naechste
    // Abruf liefe trotzdem in 091008.
    $lage = ebicsPruefsummenLage(
        ['X002' => ['schluessel' => 'AA BB', 'zertifikat' => '00 11']],
        ['X002' => ['schluessel' => 'AA BB', 'zertifikat' => '22 33']]
    );
    gleich(true, $lage['geaendert']);
    gleich([], $lage['schluessel_geaendert'], 'der Schluessel ist derselbe');
    gleich(['X002'], $lage['nur_zertifikat'], 'aber das Zertifikat nicht');
});

pruefe('Ohne Argumente ist es der Erstabruf', function () {
    gleich(EBICS_HPB_ERSTABRUF, ebicsUebernahmeArgumente([])['modus']);
});

pruefe('--pruefen wird erkannt', function () {
    gleich(EBICS_HPB_PRUEFEN, ebicsUebernahmeArgumente(['--pruefen'])['modus']);
});

pruefe('--uebernehmen nimmt beide Pruefsummen entgegen', function () {
    $aufruf = ebicsUebernahmeArgumente(['--uebernehmen', '--x002=AA BB', '--e002=CC DD']);
    gleich(EBICS_HPB_UEBERNEHMEN, $aufruf['modus']);
    gleich('AA BB', $aufruf['x002']);
    gleich('CC DD', $aufruf['e002']);
});

pruefe('⚠️ --uebernehmen ohne beide Pruefsummen wird abgewiesen', function () {
    // Ohne den Abgleich gaebe es keinen Schutz gegen einen untergeschobenen
    // Gegenueber. Ein halber Abgleich ist keiner.
    wirft(EbicsKonfigFehler::class, fn() => ebicsUebernahmeArgumente(['--uebernehmen']));
    wirft(EbicsKonfigFehler::class, fn() => ebicsUebernahmeArgumente(['--uebernehmen', '--x002=AA']));
    wirft(EbicsKonfigFehler::class, fn() => ebicsUebernahmeArgumente(['--uebernehmen', '--e002=AA']));
    wirft(EbicsKonfigFehler::class, fn() => ebicsUebernahmeArgumente(['--uebernehmen', '--x002=AA', '--e002=']));
});

pruefe('Pruefsummen ohne --uebernehmen bewirken nichts und werden abgewiesen', function () {
    wirft(EbicsKonfigFehler::class, fn() => ebicsUebernahmeArgumente(['--x002=AA', '--e002=BB']));
    wirft(EbicsKonfigFehler::class, fn() => ebicsUebernahmeArgumente(['--pruefen', '--x002=AA']));
});

pruefe('--pruefen und --uebernehmen schliessen einander aus', function () {
    wirft(
        EbicsKonfigFehler::class,
        fn() => ebicsUebernahmeArgumente(['--pruefen', '--uebernehmen', '--x002=AA', '--e002=BB'])
    );
});

pruefe('⚠️ Unbekanntes Argument wird abgewiesen — auch ein Geheimnis', function () {
    // hpb.php kennt genau vier Argumente. Ein Schluesselwort auf der
    // Kommandozeile landete in der Prozessliste und im Shell-Verlauf; es kommt
    // ausschliesslich aus der config.php.
    wirft(EbicsKonfigFehler::class, fn() => ebicsUebernahmeArgumente(['--was-auch-immer']));
    wirft(EbicsKonfigFehler::class, fn() => ebicsUebernahmeArgumente(['--passwort=geheim']));
    wirft(EbicsKonfigFehler::class, fn() => ebicsUebernahmeArgumente(['--uebernehmen', '--ja']));
});

pruefe('Passende Pruefsummen lassen die Uebernahme zu', function () {
    ebicsUebernahmePruefen(
        ['X002' => 'aa:bb:cc', 'E002' => '11 22 33'],
        ['X002' => 'AA BB CC', 'E002' => '11 22 33']
    );
});

pruefe('⚠️ Eine falsche Pruefsumme verhindert das Speichern', function () {
    // Der Abgleich ist der EINZIGE Schutz gegen einen untergeschobenen
    // Gegenueber. Eine von beiden reicht, um alles anzuhalten.
    wirft(EbicsUebernahmeFehler::class, fn() => ebicsUebernahmePruefen(
        ['X002' => 'AA BB CC', 'E002' => 'FF FF FF'],
        ['X002' => 'AA BB CC', 'E002' => '11 22 33']
    ));
    wirft(EbicsUebernahmeFehler::class, fn() => ebicsUebernahmePruefen(
        ['X002' => 'FF FF FF', 'E002' => '11 22 33'],
        ['X002' => 'AA BB CC', 'E002' => '11 22 33']
    ));
});

pruefe('⚠️ Eine leere Pruefsumme ist keine Bestaetigung', function () {
    wirft(EbicsUebernahmeFehler::class, fn() => ebicsUebernahmePruefen(
        ['X002' => '', 'E002' => ''],
        ['X002' => '', 'E002' => '']
    ));
});

pruefe('Die Meldung nennt gelieferte UND eingegebene Pruefsumme', function () {
    $fehler = wirft(EbicsUebernahmeFehler::class, fn() => ebicsUebernahmePruefen(
        ['X002' => 'FFFFFF', 'E002' => '112233'],
        ['X002' => 'AABBCC', 'E002' => '112233']
    ));
    wahr(str_contains($fehler->getMessage(), 'AABBCC'), 'die gelieferte Pruefsumme steht drin');
    wahr(str_contains($fehler->getMessage(), 'FFFFFF'), 'die eingegebene Pruefsumme steht drin');
    wahr(str_contains($fehler->getMessage(), 'X002'), 'und welcher Schluessel gemeint ist');
});

pruefe('Die Sicherung liegt neben der Schluesseldatei', function () {
    gleich(
        '/geheim/sicherung-01.keyring.json',
        ebicsSicherungsPfad('/geheim/keyring.json', fn() => false)
    );
});

pruefe('⚠️ Die Sicherung ueberschreibt nie', function () {
    // Dieselbe Regel wie bei den Auszuegen: Ein belegter Name wird nummeriert.
    // Eine still ersetzte Schluesseldatei waere der Verlust des Bankzugangs.
    $belegt = ['/geheim/sicherung-01.keyring.json' => true];
    gleich(
        '/geheim/sicherung-01.keyring-2.json',
        ebicsSicherungsPfad('/geheim/keyring.json', fn(string $p) => isset($belegt[$p]))
    );
});

pruefe('⚠️ Der blanke hpb.php-Aufruf wird bei vorhandenen Bankschluesseln verweigert', function () {
    // Sonst holte er neue Bankschluessel und schriebe sie UNGEPRUEFT ueber die
    // alten — genau der Weg, auf dem ein untergeschobener Gegenueber
    // hereinkaeme.
    $fehler = wirft(EbicsKonfigFehler::class, fn() => ebicsErstabrufPruefen(true));
    wahr(str_contains($fehler->getMessage(), '--pruefen'), 'die Meldung nennt den richtigen Weg');
    wahr(str_contains($fehler->getMessage(), '--uebernehmen'), 'und den zweiten Schritt');
});

pruefe('Der Erstabruf bleibt erlaubt, solange keine Bankschluessel da sind', function () {
    ebicsErstabrufPruefen(false);
});

// ── 9. Waechter auf den Skripten ────────────────────────────────────────────

echo "\nWaechter (Scope-Grenzen und Geheimnis-Disziplin)\n";

$alleSkripte = ['logik.php', 'start.php', 'version.php', 'init.php', 'hpb.php', 'letter.php', 'fetch.php'];

pruefe('⚠️ NUR ABHOLEN: kein Skript kann etwas einreichen', function () use ($alleSkripte) {
    // Scope-Grenze aus #549. Bankseitig soll der Teilnehmer ohnehin nur
    // Abruf-Rechte haben — hier steht die zweite Haelfte derselben Regel.
    foreach ($alleSkripte as $skript) {
        $code = skriptCode($skript);
        foreach (['executeUploadOrder', 'Orders\\BTU', 'Orders\\FUL', 'new BTU', 'new FUL'] as $verboten) {
            wahr(!str_contains($code, $verboten), "$skript enthaelt '$verboten'");
        }
    }
});

pruefe('⚠️ letter.php sendet strukturell nichts', function () {
    // Es baut das Protokoll aus der vorhandenen Schluesseldatei. Ohne
    // EbicsClient gibt es gar keinen Weg zur Bank — staerker als "wir rufen
    // execute…Order halt nicht auf".
    $code = skriptCode('letter.php');
    wahr(!str_contains($code, 'EbicsClient'), 'letter.php kennt keinen EbicsClient');
    wahr(!str_contains($code, 'execute'), 'letter.php ruft keinen Auftrag aus');
});

pruefe('⚠️ fetch.php gibt nie Auftragsdaten aus', function () {
    // Kontoumsaetze duerfen nicht ins journald. Geprueft wird der Code, nicht
    // die Kommentare.
    $code = skriptCode('fetch.php');
    foreach (['echo $rohdaten', 'echo $inhalt', 'print_r($inhalt', 'var_dump'] as $verboten) {
        wahr(!str_contains($code, $verboten), "fetch.php enthaelt '$verboten'");
    }
    wahr(!preg_match('~(echo|printf|fwrite)[^;\n]*getOrderData\(\)~', $code), 'fetch.php protokolliert getOrderData()');
    wahr(!preg_match('~(echo|printf|fwrite)[^;\n]*->getData\(\)~', $code), 'fetch.php protokolliert getData()');
});

pruefe('⚠️ Kein Skript haengt einen Logger in die Bibliothek', function () use ($alleSkripte) {
    // Der Default der Bibliothek ist ein ArrayLogger (sammelt im Speicher,
    // schreibt nirgendwohin). Ihr eigener Fehlerpfad protokolliert den
    // Report-Text der Bank — ein Logger nach stdout truege den ins journald.
    foreach ($alleSkripte as $skript) {
        wahr(!str_contains(skriptCode($skript), 'setLogger'), "$skript setzt einen Logger");
    }
});

pruefe('⚠️ hpb.php speichert nur ueber den einen, gesicherten Weg', function () {
    // Genau EIN saveKeyring() im ganzen Skript — und der steckt im Helfer, der
    // vorher die alte Datei sichert und danach atomar umbenennt. Zwei
    // Schreibwege waeren zwei Gelegenheiten, die Sicherung zu vergessen.
    $code = skriptCode('hpb.php');
    gleich(1, substr_count($code, 'saveKeyring('), 'genau ein saveKeyring()');
    wahr(str_contains($code, 'ebicsSchluesseldateiSichern('), 'es gibt den Sicherungs-Helfer');
    wahr(str_contains($code, 'rename('), 'geschrieben wird ueber Zwischendatei + rename');
    wahr(!str_contains($code, 'file_put_contents'), 'die Schluesseldatei wird nie roh geschrieben');
    wahr(!str_contains($code, 'unlink($ziel'), 'die alte Datei wird nie geloescht');
});

pruefe('⚠️ fetch.php gibt bei Exit 4 die eigene Meldung aus, nicht die der Bank', function () {
    // Der Fehlerpfad der Bibliothek fuehrt den Report-Text der Bank mit. Er
    // darf nicht ins journald.
    $code = skriptCode('fetch.php');
    wahr(str_contains($code, 'ebicsSchluesselwechselMeldung()'), 'die eigene Meldung wird verwendet');
});

pruefe('⚠️ Kein Skript nimmt das Schluesselwort von der Kommandozeile', function () use ($alleSkripte) {
    // Ein Schluesselwort als Argument landete in der Prozessliste und im
    // Shell-Verlauf. Es kommt ausschliesslich aus der config.php.
    foreach ($alleSkripte as $skript) {
        $code = skriptCode($skript);
        foreach (['--passwort', '--password', '--keyring-password', 'getopt('] as $verboten) {
            wahr(!str_contains($code, $verboten), "$skript enthaelt '$verboten'");
        }
    }
});

pruefe('⚠️ Kein Skript nimmt einen Namen aus dem Bank-Container als Pfad', function () {
    $code = skriptCode('fetch.php');
    foreach (['getNameIndex', 'array_keys($inhalte', 'basename($eintrag'] as $verboten) {
        wahr(!str_contains($code, $verboten), "fetch.php enthaelt '$verboten'");
    }
});

pruefe('⚠️ In der Vorlage steht kein echter Zugang', function () {
    $cfg = require __DIR__ . '/../config.example.php';
    foreach (['host_id', 'host_url', 'partner_id', 'user_id', 'ebics_version', 'keyring_password'] as $k) {
        wahr(isset($cfg[$k]), "Vorlage kennt '$k'");
        wahr(str_starts_with((string)$cfg[$k], 'HIER-'), "'$k' ist ein Platzhalter, kein echter Wert");
    }
});

pruefe('⚠️ .gitignore sperrt Zugangsdaten und Schluessel aus', function () {
    $ignore = skriptText('.gitignore');
    foreach (['config.php', 'keyring.json', 'vendor/', '*.pdf', 'sicherung-'] as $muster) {
        wahr(str_contains($ignore, $muster), ".gitignore deckt '$muster' ab");
    }
});

pruefe('Die Bibliothek ist auf die erprobten Pakete festgelegt', function () {
    // ⚠️ Fanggrube 20.08.2026: `fpdf/fpdf` ist NICHT das Paket, das die
    // Bibliothek meint — ohne `setasign/fpdf` scheitert die PDF-Erzeugung.
    $composer = json_decode(skriptText('composer.json'), true);
    wahr(isset($composer['require']['ebics-api/ebics-client-php']), 'Bibliothek steht in composer.json');
    gleich(true, isset($composer['require']['setasign/fpdf']), 'setasign/fpdf steht in composer.json');
    gleich(false, isset($composer['require']['fpdf/fpdf']), 'fpdf/fpdf steht NICHT in composer.json');
});

// ── 10. Ablage gegen ein echtes Dateisystem ─────────────────────────────────
//
// Die Ablage ist die einzige Stelle, an der ein echtes Betriebssystem-Primitiv
// (rename) die Aussage traegt — sie wird deshalb nicht nachgebaut, sondern
// gefahren. Dafuer muessen fetch.php und hpb.php ladbar sein, und das braucht
// vendor/.

echo "\nAblage (echtes Dateisystem)\n";

if (!is_file(__DIR__ . '/../vendor/autoload.php')) {
    echo "  --   uebersprungen: vendor/ fehlt (diese Faelle laufen im gebauten Image)\n";
} else {
    require_once __DIR__ . '/../fetch.php';
    require_once __DIR__ . '/../hpb.php';

    $frischesVerzeichnis = static function (): string {
        $pfad = sys_get_temp_dir() . '/ablage-' . bin2hex(random_bytes(6));
        mkdir($pfad, 0755, true);
        return $pfad;
    };

    pruefe('Zwei Inhalte werden datumsbenannt abgelegt', function () use ($frischesVerzeichnis) {
        $dir = $frischesVerzeichnis();
        $geschrieben = ebicsAblegen($dir, ['<a/>', '<b/>'], '2026-08-20');

        gleich(2, count($geschrieben), 'Anzahl Dateien');
        gleich($dir . '/camt053-2026-08-20-01.xml', $geschrieben[0]);
        gleich($dir . '/camt053-2026-08-20-02.xml', $geschrieben[1]);
        gleich('<a/>', file_get_contents($geschrieben[0]), 'Inhalt Datei 1');
        gleich('<b/>', file_get_contents($geschrieben[1]), 'Inhalt Datei 2');
    });

    pruefe('⚠️ Ein zweiter Lauf ueberschreibt nichts', function () use ($frischesVerzeichnis) {
        $dir = $frischesVerzeichnis();
        ebicsAblegen($dir, ['<erster-lauf/>'], '2026-08-20');
        $zweiter = ebicsAblegen($dir, ['<zweiter-lauf/>'], '2026-08-20');

        gleich($dir . '/camt053-2026-08-20-01-2.xml', $zweiter[0], 'zweiter Lauf weicht aus');
        gleich(
            '<erster-lauf/>',
            file_get_contents($dir . '/camt053-2026-08-20-01.xml'),
            'die Datei des ersten Laufs ist unveraendert'
        );
    });

    pruefe('⚠️ Scheitert eine Datei, bleibt KEIN Halbzustand zurueck', function () use ($frischesVerzeichnis) {
        $dir = $frischesVerzeichnis();

        // Ein Verzeichnis am Platz der Zwischendatei laesst das Schreiben der
        // ZWEITEN Datei scheitern — die erste ist zu dem Zeitpunkt schon da.
        mkdir($dir . '/.camt053-2026-08-20-02.xml.teil');

        wirft(EbicsAblageFehler::class, fn() => ebicsAblegen($dir, ['<a/>', '<b/>'], '2026-08-20'));

        gleich([], glob($dir . '/camt053-*') ?: [], 'nichts liegt im Verzeichnis');
    });

    pruefe('Nach einem gelungenen Lauf liegt keine Zwischendatei mehr da', function () use ($frischesVerzeichnis) {
        $dir = $frischesVerzeichnis();
        ebicsAblegen($dir, ['<a/>'], '2026-08-20');

        gleich([], glob($dir . '/.*.teil') ?: [], 'keine .teil-Datei uebrig');
    });

    pruefe('Der Abruf-Auftrag laesst sich fuer H005 und H004 bauen', function () {
        // Baut die Auftrags-Objekte gegen die ECHTE Bibliothek — ohne Bank,
        // ohne Netz. Er faengt genau das, was ein Logiktest nicht kann: einen
        // Methodennamen, den es in der installierten Fassung nicht gibt.
        $h005 = ebicsAbrufAuftrag(
            vollstaendigeKonfig(),
            null,
            new EbicsApi\Ebics\Contexts\RequestContext()
        );
        gleich('BTD', $h005->getOrderType(), 'H005 laeuft als BTD');

        $h004 = ebicsAbrufAuftrag(
            vollstaendigeKonfig(['ebics_version' => 'H004']),
            [new DateTimeImmutable('2026-08-01'), new DateTimeImmutable('2026-08-05')],
            new EbicsApi\Ebics\Contexts\RequestContext()
        );
        gleich('FDL', $h004->getOrderType(), 'H004 laeuft als FDL');
    });

    pruefe('⚠️ Das Laden von fetch.php ruft KEINEN Abruf aus', function () {
        // Waere das anders, haette allein dieser Testlauf gerade versucht,
        // mit einer Bank zu sprechen. Der Beleg ist, dass wir hier noch leben:
        // fetch.php ist oben geladen worden, und die Strecke laeuft weiter.
        wahr(function_exists('ebicsAbholen'), 'fetch.php ist geladen');
        wahr(function_exists('ebicsAblegen'), 'und seine Funktionen sind da');
    });

    pruefe('⚠️ Das Laden von hpb.php holt KEINE Bankschluessel', function () {
        // Dieselbe Schranke wie bei fetch.php — sonst spraeche allein der
        // Testlauf mit einer Bank.
        wahr(function_exists('ebicsHpbLauf'), 'hpb.php ist geladen');
        wahr(function_exists('ebicsBankPruefsummen'), 'und seine Funktionen sind da');
    });

    // ── 11. Schluesselwechsel gegen Bibliothek und Dateisystem ──────────────

    echo "\nSchluesselwechsel (echte Bibliothek, echtes Dateisystem)\n";

    pruefe('Die Ausnahmeklasse fuer 091008 gibt es und sie traegt den Code', function () {
        // Faengt eine Umbenennung beim Bibliotheks-Update: Ohne diese Klasse
        // faende ebicsIstBankschluesselwechsel() den Kurznamen nie wieder.
        $klasse = 'EbicsApi\\Ebics\\Exceptions\\BankPubkeyUpdateRequiredException';
        wahr(class_exists($klasse), "die Klasse $klasse gibt es");

        $fehler = new $klasse();
        gleich('091008', $fehler->getResponseCode(), 'sie traegt 091008');
        wahr(
            $fehler instanceof EbicsApi\Ebics\Exceptions\EbicsResponseException,
            'und sie ist eine EbicsResponseException (fetch.php liest getResponseCode())'
        );
        gleich(4, ebicsExitCode(get_class($fehler), $fehler->getResponseCode()), 'zusammen ergibt das Exit 4');
    });

    pruefe('⚠️ Die Bibliothek prueft den Antwortcode VOR dem Auspacken', function () {
        // Das ist der Grund, warum ein Bankschluesselwechsel keinen halben
        // Auszug hinterlaesst: Bei 091008 gibt es weder Auftragsdaten noch
        // eine Quittung. Statt eine Bankantwort zu faelschen, wird die
        // Reihenfolge in der Quelle festgehalten — sie ist die Aussage.
        $quelle = (string)file_get_contents(
            __DIR__ . '/../vendor/ebics-api/ebics-client-php/src/EbicsClient.php'
        );
        $ausschnitt = substr($quelle, (int)strpos($quelle, 'function retrieveInitializationSegment'));
        $pruefung = strpos($ausschnitt, 'checkH00XReturnCode');
        $auspacken = strpos($ausschnitt, 'extractInitializationSegment');

        wahr($pruefung !== false, 'checkH00XReturnCode kommt vor');
        wahr($auspacken !== false, 'extractInitializationSegment kommt vor');
        wahr($pruefung < $auspacken, 'und die Pruefung steht VOR dem Auspacken');
    });

    pruefe('⚠️ Sicherung und atomares Speichern gegen ein echtes Dateisystem', function () use ($frischesVerzeichnis) {
        $dir = $frischesVerzeichnis();
        $ziel = $dir . '/keyring.json';
        file_put_contents($ziel, '{"alt":true}');
        $vorher = (string)file_get_contents($ziel);

        $manager = new EbicsApi\Ebics\Services\FileKeyringManager();
        $keyring = $manager->createKeyring(EbicsApi\Ebics\Models\Keyring::VERSION_30);
        $keyring->setUserSignatureAVersion(EbicsApi\Ebics\Contracts\SignatureInterface::A_VERSION6);
        $keyring->setPassword('nur-fuer-den-test');

        ebicsHpbSpeichern($manager, $keyring, $ziel);

        gleich(
            $vorher,
            (string)file_get_contents($dir . '/sicherung-01.keyring.json'),
            'die alte Datei liegt byte-gleich als Sicherung'
        );
        wahr((string)file_get_contents($ziel) !== $vorher, 'die neue Datei steht am Platz');
        gleich([], glob($dir . '/.*.teil') ?: [], 'keine Zwischendatei uebrig');
    });

    pruefe('⚠️ Ein zweiter Wechsel ueberschreibt die erste Sicherung nicht', function () use ($frischesVerzeichnis) {
        $dir = $frischesVerzeichnis();
        $ziel = $dir . '/keyring.json';
        file_put_contents($ziel, '{"erster":true}');

        $manager = new EbicsApi\Ebics\Services\FileKeyringManager();
        $keyring = $manager->createKeyring(EbicsApi\Ebics\Models\Keyring::VERSION_30);
        $keyring->setUserSignatureAVersion(EbicsApi\Ebics\Contracts\SignatureInterface::A_VERSION6);
        $keyring->setPassword('nur-fuer-den-test');

        ebicsHpbSpeichern($manager, $keyring, $ziel);
        ebicsHpbSpeichern($manager, $keyring, $ziel);

        gleich(
            '{"erster":true}',
            (string)file_get_contents($dir . '/sicherung-01.keyring.json'),
            'die erste Sicherung ist unveraendert'
        );
        wahr(is_file($dir . '/sicherung-01.keyring-2.json'), 'die zweite Sicherung liegt daneben');
    });

    pruefe('⚠️ --pruefen kann strukturell nicht speichern', function () {
        // Der Zweig, der --pruefen bedient, bekommt AUSSCHLIESSLICH die beiden
        // Pruefsummen-Aufstellungen — keinen Keyring, keinen Manager, keinen
        // Pfad. Er kann gar nichts schreiben, nicht nur „er tut es nicht".
        $r = new ReflectionFunction('ebicsHpbPruefen');
        gleich(2, $r->getNumberOfParameters(), 'genau zwei Parameter');
        foreach ($r->getParameters() as $parameter) {
            gleich('array', (string)$parameter->getType(), 'und beide sind array: ' . $parameter->getName());
        }
    });
}

// ── Ergebnis ────────────────────────────────────────────────────────────────

echo "\n";
if ($GLOBALS['jnpt_rot'] === []) {
    printf("%d Faelle gruen.\n", $GLOBALS['jnpt_gruen']);
    exit(0);
}
printf("%d gruen, %d ROT:\n", $GLOBALS['jnpt_gruen'], count($GLOBALS['jnpt_rot']));
foreach ($GLOBALS['jnpt_rot'] as $rot) {
    echo "  - $rot\n";
}
exit(1);
