<?php
// Bankschluessel holen und abgleichen (#549, Schluesselwechsel #617).
//
// ⚠️ DER ABGLEICH VON HAND IST PFLICHT, KEIN RITUAL: Die Pruefsummen, die
// dieses Skript ausgibt, muessen mit denen im Schreiben der Bank
// uebereinstimmen. Erst dieser Vergleich belegt, dass am anderen Ende wirklich
// die Bank steht und nicht jemand, der sich dazwischengeschoben hat. Stimmen
// sie nicht: STOPP, nichts abrufen, und der Betreiber meldet sich bei seiner
// Bank.
//
// Datenschutz-Linie: Dieses Skript holt KEINE Umsaetze. Es tauscht
// ausschliesslich Schluesselmaterial.
//
// Drei Betriebsarten:
//
//   php /app/hpb.php
//       Erstabruf nach der Freischaltung. Verweigert, sobald bereits
//       Bankschluessel in der Schluesseldatei stehen.
//
//   php /app/hpb.php --pruefen
//       Bankschluessel holen, NICHT speichern, mit den gespeicherten
//       vergleichen. Laeuft mit `/geheim` schreibgeschuetzt.
//
//   php /app/hpb.php --uebernehmen --x002=<hex> --e002=<hex>
//       Neue Bankschluessel uebernehmen — nur, wenn BEIDE Pruefsummen aus dem
//       NEUEN Schreiben der Bank passen. Die alte Schluesseldatei bleibt als
//       nummerierte Sicherung liegen.
//
// Exit-Codes:
//   0  in Ordnung (Erstabruf gelaufen · gepruefte Lage unveraendert · uebernommen)
//   1  Konfiguration oder Aufruf unbrauchbar — es wurde nichts gespeichert
//   2  echter Fehler (Bank, Netz, Protokoll)
//   4  --pruefen: die Bankschluessel haben sich GEAENDERT (nichts gespeichert)
//   5  --uebernehmen: die eingegebenen Pruefsummen passen nicht — NICHTS gespeichert

declare(strict_types=1);

require_once __DIR__ . '/vendor/autoload.php';
require_once __DIR__ . '/logik.php';
require_once __DIR__ . '/version.php';
require_once __DIR__ . '/start.php';

use EbicsApi\Ebics\Contracts\SignatureInterface;
use EbicsApi\Ebics\EbicsClient;
use EbicsApi\Ebics\Factories\Crypt\RSAFactory;
use EbicsApi\Ebics\Models\Keyring;
use EbicsApi\Ebics\Orders\HPB;
use EbicsApi\Ebics\Services\CryptService;
use EbicsApi\Ebics\Services\FileKeyringManager;
use EbicsApi\Ebics\Services\Processor\AESEncryptor;
use EbicsApi\Ebics\Services\Processor\Base64Encoder;
use EbicsApi\Ebics\Services\RandomService;
use EbicsApi\Ebics\Services\TransactionKeyResolver;

/** Der Kryptodienst der Bibliothek, zusammengesetzt wie in EbicsBankLetter. */
function ebicsCryptService(): CryptService
{
    $aes = new AESEncryptor(new TransactionKeyResolver());

    return new CryptService(new RSAFactory($aes, null), $aes, new RandomService(), new Base64Encoder());
}

/** Hex-Zeichen in der Darstellung, in der Bank-Schreiben sie drucken. */
function ebicsHexGruppiert(string $hex): string
{
    return implode(' ', str_split(strtoupper($hex), 2));
}

/**
 * Die Pruefsumme eines Bankschluessels in der Form, in der Bank-Schreiben sie
 * drucken: SHA-256 ueber „Exponent Modulus", hexadezimal in Paaren.
 */
function ebicsPruefsumme(CryptService $crypt, SignatureInterface $signatur): string
{
    return ebicsHexGruppiert(bin2hex($crypt->calculatePublicKeyDigest($signatur)));
}

/**
 * Der Fingerabdruck des Bank-Zertifikats — NICHT derselbe Wert wie die
 * Pruefsumme des Schluessels.
 *
 * ⚠️ Bei H005 sendet die Bibliothek diesen Fingerabdruck an die Bank
 * (DigestResolverV3), nicht den Public-Key-Digest. Erneuert die Bank nur ihr
 * Zertifikat bei gleichem Schluesselpaar, waere der Schluessel „unveraendert"
 * — und der naechste Abruf liefe trotzdem in 091008. Deshalb wird er
 * mitverglichen. Er steht NICHT im Bankschreiben und ist deshalb auch nicht
 * das, was bei `--uebernehmen` bestaetigt wird.
 */
function ebicsZertifikatsAbdruck(CryptService $crypt, SignatureInterface $signatur): ?string
{
    $inhalt = $signatur->getCertificateContent();

    if ($inhalt === null || $inhalt === '') {
        return null;
    }

    return ebicsHexGruppiert(bin2hex($crypt->calculateCertificateFingerprint($inhalt)));
}

/**
 * Die Lage der beiden Bankschluessel: je Rolle die Pruefsumme des Schluessels
 * und der Fingerabdruck des Zertifikats.
 *
 * Die Rollen heissen hier fest `X002`/`E002` — dieselben Namen wie die
 * Argumente von `--uebernehmen`. Wie die Bibliothek sie benennt, reist als
 * `bezeichnung` mit und dient nur der Anzeige.
 *
 * @return array<string, array{bezeichnung: string, schluessel: string, zertifikat: ?string}>
 */
function ebicsBankPruefsummen(Keyring $keyring): array
{
    $crypt = ebicsCryptService();

    $rollen = [
        'X002' => [$keyring->getBankSignatureXVersion(), $keyring->getBankSignatureX()],
        'E002' => [$keyring->getBankSignatureEVersion(), $keyring->getBankSignatureE()],
    ];

    $lage = [];
    foreach ($rollen as $rolle => [$bezeichnung, $signatur]) {
        $lage[$rolle] = $signatur === null
            ? ['bezeichnung' => $bezeichnung, 'schluessel' => '', 'zertifikat' => null]
            : [
                'bezeichnung' => $bezeichnung,
                'schluessel' => ebicsPruefsumme($crypt, $signatur),
                'zertifikat' => ebicsZertifikatsAbdruck($crypt, $signatur),
            ];
    }

    return $lage;
}

/**
 * Die Pruefsummen zum Abgleich mit dem Schreiben der Bank.
 *
 * @param array<string, array{bezeichnung: string, schluessel: string, zertifikat: ?string}> $lage
 */
function ebicsBankPruefsummenAusgeben(array $lage): void
{
    foreach ($lage as $eintrag) {
        if ($eintrag['schluessel'] === '') {
            printf("  %-6s (kein Schluessel in der Schluesseldatei)\n", $eintrag['bezeichnung']);
            continue;
        }

        printf("  %-6s %s\n", $eintrag['bezeichnung'], $eintrag['schluessel']);

        if ($eintrag['zertifikat'] !== null) {
            printf("  %-6s Zertifikat: %s\n", '', $eintrag['zertifikat']);
        }
    }
}

/**
 * Die alte Schluesseldatei als nummerierte Sicherung ablegen.
 *
 * ⚠️ VOR dem Schreiben, nie danach. Aendert die Bibliothek eines Tages ihr
 * Dateiformat oder geht beim Schreiben etwas schief, ist der Bankzugang sonst
 * weg — und mit ihm der elektronische Weg zurueck.
 */
function ebicsSchluesseldateiSichern(string $keyringPfad): string
{
    $sicherung = ebicsSicherungsPfad($keyringPfad, 'file_exists');

    if (!@copy($keyringPfad, $sicherung)) {
        throw new EbicsAblageFehler(
            "Konnte die alte Schluesseldatei nicht sichern: $sicherung (" . ebicsLetzterFehler() . ")\n"
            . 'Es wurde NICHTS geaendert. Ist /geheim schreibbar gemountet?'
        );
    }

    return $sicherung;
}

/**
 * Die Schluesseldatei ersetzen — der EINZIGE schreibende Weg dieses Skripts.
 *
 * Erst die alte Datei sichern, dann unter einem versteckten Zwischennamen
 * schreiben und umbenennen (`rename` im selben Verzeichnis ist atomar).
 * Dieselbe Technik wie bei den Auszuegen: Eine halb geschriebene
 * Schluesseldatei waere ein verlorener Bankzugang.
 */
function ebicsHpbSpeichern(FileKeyringManager $manager, Keyring $keyring, string $ziel): void
{
    if (is_file($ziel)) {
        echo 'Alte Schluesseldatei gesichert: ' . ebicsSchluesseldateiSichern($ziel) . "\n";
    }

    $zwischen = dirname($ziel) . '/.' . basename($ziel) . '.teil';
    @unlink($zwischen);

    $manager->saveKeyring($keyring, $zwischen);

    if (!is_file($zwischen)) {
        throw new EbicsAblageFehler(
            "Konnte die Schluesseldatei nicht schreiben: $zwischen (" . ebicsLetzterFehler() . ")\n"
            . 'Es wurde NICHTS geaendert. Ist /geheim schreibbar gemountet?'
        );
    }

    if (!@rename($zwischen, $ziel)) {
        @unlink($zwischen);

        throw new EbicsAblageFehler(
            "Konnte die Schluesseldatei nicht ablegen als $ziel (" . ebicsLetzterFehler() . ')'
        );
    }
}

/**
 * `--pruefen`: die beiden Aufstellungen vergleichen und berichten.
 *
 * ⚠️ Diese Funktion bekommt AUSSCHLIESSLICH die beiden Pruefsummen-
 * Aufstellungen — keinen Keyring, keinen Manager, keinen Pfad. Sie kann
 * strukturell nichts schreiben; das ist mehr als „sie tut es nicht".
 *
 * @param array<string, array{bezeichnung: string, schluessel: string, zertifikat: ?string}> $alt
 * @param array<string, array{bezeichnung: string, schluessel: string, zertifikat: ?string}> $neu
 * @return int Exit-Code
 */
function ebicsHpbPruefen(array $alt, array $neu): int
{
    $lage = ebicsPruefsummenLage($alt, $neu);

    if (!$lage['geaendert']) {
        echo "UNVERAENDERT — die Bankschluessel sind dieselben wie in der Schluesseldatei.\n";
        echo "Es wurde nichts geschrieben.\n";

        return 0;
    }

    echo "\n";
    echo "════════════════════════════════════════════════════════════════════\n";
    echo "GEAENDERT — die Bank liefert andere Schluessel als gespeichert.\n";
    echo "Es wurde NICHTS gespeichert.\n";
    echo "════════════════════════════════════════════════════════════════════\n\n";

    if ($lage['schluessel_geaendert'] !== []) {
        echo 'Neue Bankschluessel: ' . implode(', ', $lage['schluessel_geaendert']) . "\n";
    }

    if ($lage['nur_zertifikat'] !== []) {
        // Der Sonderfall, den ein reiner Schluesselvergleich uebersaehe.
        echo 'Schluessel unveraendert, aber Bankzertifikat gewechselt: '
            . implode(', ', $lage['nur_zertifikat']) . "\n";
        echo "Auch das laesst den Abruf in 091008 laufen — bei H005 reist der Zertifikats-\n";
        echo "Fingerabdruck zur Bank, nicht die Schluessel-Pruefsumme.\n";
    }

    echo "\nWas die Bank gerade geliefert hat:\n\n";
    ebicsBankPruefsummenAusgeben($neu);

    echo "\n";
    echo "════════════════════════════════════════════════════════════════════\n";
    echo "⚠️ DIESE AUSGABE IST KEINE BESTAETIGUNG.\n";
    echo "Die Werte fuer --uebernehmen stammen aus dem NEUEN SCHREIBEN DER BANK,\n";
    echo "niemals von diesem Bildschirm. Wer hier abschreibt, bestaetigt genau den\n";
    echo "Gegenueber, den er pruefen wollte.\n\n";
    echo "  php /app/hpb.php --uebernehmen --x002=<hex> --e002=<hex>\n\n";
    echo "Runbook: docs/ebics-abholer.md, Abschnitt \"Schluesselwechsel der Bank\".\n";
    echo "════════════════════════════════════════════════════════════════════\n";

    return 4;
}

/** Der Hinweis nach jedem Schreiben der Schluesseldatei. */
function ebicsHpbAbgleichHinweis(): void
{
    echo "\n";
    echo "════════════════════════════════════════════════════════════════════\n";
    echo "PFLICHT: Diese Werte gegen das Schreiben der Bank vergleichen.\n";
    echo "Verglichen werden die HEX-ZEICHEN — Gross-/Kleinschreibung und die\n";
    echo "Gruppierung in Paaren sind reine Darstellung und duerfen abweichen.\n";
    echo "Bei einer Abweichung in den Zeichen: STOPP — nichts abrufen, bei der\n";
    echo "Bank melden.\n";
    echo "════════════════════════════════════════════════════════════════════\n";
}

function ebicsHpbLauf(array $argumente): int
{
    $aufruf = ebicsUebernahmeArgumente($argumente);

    $cfg = ebicsKonfigLaden();
    $manager = new FileKeyringManager();
    $keyring = ebicsKeyringLaden($cfg, $manager);
    $bank = ebicsBank($cfg);
    $teilnehmer = ebicsTeilnehmer($cfg);
    ebicsApplyVersionSpecifics($cfg, $keyring, $bank);

    // ⚠️ Die alte Lage VOR dem HPB festhalten: `HPB::afterExecute` setzt die
    // Bankschluessel im Speicher auf genau diesem Keyring-Objekt. Danach
    // gaebe es nichts mehr zu vergleichen.
    $alt = ebicsBankPruefsummen($keyring);
    $hatBankschluessel = $alt['X002']['schluessel'] !== '' || $alt['E002']['schluessel'] !== '';

    if ($aufruf['modus'] === EBICS_HPB_ERSTABRUF) {
        ebicsErstabrufPruefen($hatBankschluessel);
    }

    $client = new EbicsClient($bank, $teilnehmer, $keyring);

    echo "Hole Bankschluessel (HPB)...\n";
    $client->executeInitializationOrder(new HPB());

    $neu = ebicsBankPruefsummen($client->getKeyring());

    // ⚠️ Der Pruef-Zweig kennt das Ziel nicht einmal dem Namen nach — er
    // bekommt nur die beiden Aufstellungen und liegt vor jeder Zeile, die
    // schreiben koennte.
    if ($aufruf['modus'] === EBICS_HPB_PRUEFEN) {
        return ebicsHpbPruefen($alt, $neu);
    }

    $ziel = ebicsKeyringPfad(getenv());

    if ($aufruf['modus'] === EBICS_HPB_UEBERNEHMEN) {
        // ⚠️ ZUERST der Abgleich, DANN das Schreiben. Schlaegt er an, ist die
        // alte Schluesseldatei unberuehrt und es gibt nicht einmal eine
        // Sicherung, die jemand aufraeumen muesste.
        ebicsUebernahmePruefen(
            ['X002' => $aufruf['x002'], 'E002' => $aufruf['e002']],
            ['X002' => $neu['X002']['schluessel'], 'E002' => $neu['E002']['schluessel']]
        );

        echo "Abgleich in Ordnung — beide Pruefsummen stimmen mit der Eingabe ueberein.\n";
        ebicsHpbSpeichern($manager, $client->getKeyring(), $ziel);
        echo "Neue Bankschluessel gespeichert in $ziel.\n\n";
        echo "Uebernommene Pruefsummen:\n\n";
        ebicsBankPruefsummenAusgeben($neu);
        echo "\nDanach: php /app/fetch.php — der naechste Abruf sollte wieder durchlaufen.\n";

        return 0;
    }

    // Erstabruf: Zuerst sichern, dann anzeigen. Ein Fehler in der Anzeige darf
    // nicht dazu fuehren, dass die gerade geholten Bankschluessel verloren
    // gehen.
    ebicsHpbSpeichern($manager, $client->getKeyring(), $ziel);
    echo "Bankschluessel gespeichert in $ziel.\n\n";
    echo "Pruefsummen der Bankschluessel, wie unser Werkzeug sie sieht:\n\n";
    ebicsBankPruefsummenAusgeben($neu);
    ebicsHpbAbgleichHinweis();
    echo "Danach: HAA/HTD als Selbstauskunft der Bank (siehe Runbook),\n";
    echo "dann der erste echte Abruf mit php /app/fetch.php.\n";

    return 0;
}

// ── Ablauf ──────────────────────────────────────────────────────────────────
//
// ⚠️ Nur, wenn diese Datei DIREKT aufgerufen wurde. Ohne diese Schranke
// spraeche allein das Einbinden von hpb.php (die Teststrecke tut das, um
// Sicherung und atomares Speichern gegen ein echtes Dateisystem zu fahren) mit
// einer Bank.

if (PHP_SAPI !== 'cli' || !isset($argv[0]) || realpath($argv[0]) !== __FILE__) {
    return;
}

try {
    exit(ebicsHpbLauf(array_slice($argv, 1)));
} catch (EbicsKonfigFehler $e) {
    fwrite(STDERR, $e->getMessage() . "\n");
    exit(1);
} catch (EbicsUebernahmeFehler $e) {
    fwrite(STDERR, $e->getMessage() . "\n");
    exit(5);
} catch (Throwable $e) {
    fwrite(STDERR, $e->getMessage() . "\n");
    exit(2);
}
