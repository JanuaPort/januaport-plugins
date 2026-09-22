<?php
// Gemeinsamer Einstieg aller Abholer-Skripte (#549): die duenne I/O-Schale.
//
// Hier steht ausschliesslich, was die Aussenwelt beruehrt — Datei lesen,
// Objekte der Bibliothek bauen. Jede ENTSCHEIDUNG (ist die Konfiguration
// brauchbar? welche Version? welcher Dateiname?) steht in logik.php und ist
// dort ohne Bibliothek und ohne Bank getestet.

declare(strict_types=1);

use EbicsApi\Ebics\Models\Bank;
use EbicsApi\Ebics\Models\Keyring;
use EbicsApi\Ebics\Models\User;
use EbicsApi\Ebics\Services\FileKeyringManager;

/**
 * Konfiguration lesen und pruefen. Wirft EbicsKonfigFehler mit ALLEN
 * Beanstandungen auf einmal — wer eine frische config.php ausfuellt, soll
 * nicht siebenmal denselben Lauf starten muessen.
 */
function ebicsKonfigLaden(): array
{
    $pfad = ebicsKonfigPfad(getenv());

    if (!is_file($pfad)) {
        throw new EbicsKonfigFehler(
            "Konfiguration fehlt: $pfad\n" .
            'config.example.php nach config.php kopieren und von Hand ausfuellen (docs/ebics-abholer.md).'
        );
    }

    $cfg = require $pfad;

    if (!is_array($cfg)) {
        throw new EbicsKonfigFehler("$pfad muss ein Array zurueckgeben — siehe config.example.php.");
    }

    $fehler = ebicsKonfigFehler($cfg);
    if ($fehler !== []) {
        throw new EbicsKonfigFehler(implode("\n", $fehler));
    }

    return $cfg;
}

/** Der Bank-Host aus der Konfiguration. */
function ebicsBank(array $cfg): Bank
{
    $bank = new Bank((string)$cfg['host_id'], (string)$cfg['host_url']);

    // Die Laenderkennung ist der Default fuer den H004-Abruf (FDL). Bei H005
    // steht der Geltungsbereich in der BTF-Kennung und diese Angabe ruht.
    $bank->setCountryCode(strtoupper(trim((string)($cfg['h004_land'] ?? 'DE'))));

    return $bank;
}

/** Der Teilnehmer aus der Konfiguration. */
function ebicsTeilnehmer(array $cfg): User
{
    return new User((string)$cfg['partner_id'], (string)$cfg['user_id']);
}

/**
 * Die Schluesseldatei laden.
 *
 * ⚠️ Ohne sie geht nichts, und sie wird hier NIE angelegt — das tut allein
 * init.php. Ein Skript, das stillschweigend neue Schluessel erzeugte, haette
 * den Bankzugang verworfen und einen neuen Initialisierungsbrief noetig
 * gemacht.
 */
function ebicsKeyringLaden(array $cfg, FileKeyringManager $manager): Keyring
{
    $pfad = ebicsKeyringPfad(getenv());

    if (!is_file($pfad)) {
        throw new EbicsKonfigFehler(
            "Schluesseldatei fehlt: $pfad\n" .
            'Zuerst init.php, dann — nach der Freischaltung durch die Bank — hpb.php (docs/ebics-abholer.md).'
        );
    }

    return $manager->loadKeyring($pfad, (string)$cfg['keyring_password'], ebicsKeyringVersion($cfg));
}

/** Das Verzeichnis, in dem Schluesseldatei und Protokoll liegen. */
function ebicsGeheimVerzeichnis(): string
{
    return dirname(ebicsKeyringPfad(getenv()));
}

/**
 * Der Grund der letzten unterdrueckten PHP-Warnung, fuer unsere Meldung.
 *
 * Datei-Operationen laufen hier mit `@`, damit die MELDUNG unsere ist und
 * nicht eine PHP-Warnung im journald — der Grund reist als Klartext mit.
 */
function ebicsLetzterFehler(): string
{
    $fehler = error_get_last();

    return $fehler['message'] ?? 'kein naeherer Grund verfuegbar';
}
