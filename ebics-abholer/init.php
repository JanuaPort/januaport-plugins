<?php
// Erstinitialisierung des EBICS-Zugangs (#549) — Schritt 1 von 3.
//
//     Schluessel erzeugen -> INI -> HIA -> Initialisierungsprotokoll (PDF)
//
// Das PDF enthaelt ausschliesslich die Pruefsummen der OEFFENTLICHEN
// Schluessel. Der Betreiber druckt es, unterschreibt es und reicht es bei
// SEINER Bank ein (Weg je Institut verschieden: Mail, Fax, Post).
//
// ⚠️ AB DIESEM LAUF LAEUFT DIE FRIST. Ueblich sind zehn Tage bis zum Eingang
// des unterschriebenen Protokolls. Wer initialisiert und das Protokoll liegen
// laesst, darf von vorn anfangen.
//
// ⚠️ Die Schluesseldatei wird VOR dem ersten Senden gespeichert. Ginge der
// Lauf zwischen Erzeugen und Senden verloren, waeren die Schluessel weg, die
// die Bank gleich registriert.
//
// Aufruf:  docker compose run --rm ebics-abholer php /app/init.php

declare(strict_types=1);

require_once __DIR__ . '/vendor/autoload.php';
require_once __DIR__ . '/logik.php';
require_once __DIR__ . '/version.php';
require_once __DIR__ . '/start.php';

use EbicsApi\Ebics\EbicsBankLetter;
use EbicsApi\Ebics\EbicsClient;
use EbicsApi\Ebics\Orders\HIA;
use EbicsApi\Ebics\Orders\INI;
use EbicsApi\Ebics\Services\FileKeyringManager;

try {
    $cfg = ebicsKonfigLaden();

    $keyringPfad = ebicsKeyringPfad(getenv());
    $manager = new FileKeyringManager();
    $frisch = !is_file($keyringPfad);

    if ($frisch) {
        $keyring = $manager->createKeyring(ebicsKeyringVersion($cfg));
        $keyring->setPassword((string)$cfg['keyring_password']);
    } else {
        echo "Bestehende Schluesseldatei gefunden — sie wird geladen, NICHT ersetzt.\n";
        $keyring = ebicsKeyringLaden($cfg, $manager);
    }

    $bank = ebicsBank($cfg);
    $teilnehmer = ebicsTeilnehmer($cfg);

    // ⚠️ FANGGRUBE (live erkauft, 20.08.2026): Bei H005 muss die
    // Zertifikatshuelle VOR dem Erzeugen der Schluessel gesetzt sein — sonst
    // weist die Bank INI mit 090004 ab. Begruendung in version.php.
    ebicsApplyVersionSpecifics($cfg, $keyring, $bank);

    $client = new EbicsClient($bank, $teilnehmer, $keyring);

    if ($frisch) {
        echo "Erzeuge Teilnehmerschluessel (A006/X002/E002)...\n";
        $client->createUserSignatures();
        $ziel = $keyringPfad;
        $manager->saveKeyring($client->getKeyring(), $ziel); // sichern VOR dem Senden
    }

    echo "Sende INI (Signaturschluessel zur Bank)...\n";
    $client->executeStandardOrder(new INI());

    echo "Sende HIA (Authentifikations- und Verschluesselungsschluessel zur Bank)...\n";
    $client->executeStandardOrder(new HIA());

    $ziel = $keyringPfad;
    $manager->saveKeyring($client->getKeyring(), $ziel);

    echo "Erzeuge Initialisierungsprotokoll (PDF)...\n";
    $brief = new EbicsBankLetter();
    $pdf = $brief->formatBankLetter(
        $brief->prepareBankLetter($client->getBank(), $client->getUser(), $client->getKeyring()),
        $brief->createPdfBankLetterFormatter()
    );

    $pdfPfad = ebicsFreierPfad(ebicsGeheimVerzeichnis(), 'initialisierungsprotokoll.pdf', 'file_exists');
    if (file_put_contents($pdfPfad, $pdf) === false) {
        throw new EbicsAblageFehler("Konnte das Protokoll nicht schreiben: $pdfPfad");
    }

    echo "\nFERTIG.\n";
    echo "1. $pdfPfad drucken, unterschreiben und bei der Bank einreichen — HEUTE.\n";
    echo "2. Ab jetzt laeuft die Frist der Bank (ueblich: zehn Tage).\n";
    echo "3. Schluesseldatei + Schluesselwort gehoeren ins Backup bzw. den Passwortmanager.\n";
    echo "4. Nach der Freischaltung: php /app/hpb.php — mit PFLICHT-Abgleich der Pruefsummen.\n";
    exit(0);
} catch (EbicsKonfigFehler $e) {
    fwrite(STDERR, $e->getMessage() . "\n");
    exit(1);
} catch (Throwable $e) {
    // Ein abgewiesener INI/HIA-Auftrag hinterlaesst bei der Bank NICHTS —
    // Schluessel neu erzeugen (Schluesseldatei loeschen) und erneut laufen
    // lassen ist der vorgesehene Weg, kein Drama (#548, 20.08.2026).
    fwrite(STDERR, $e->getMessage() . "\n");
    exit(2);
}
