<?php
// Initialisierungsprotokoll neu erzeugen (#549) — aus der VORHANDENEN
// Schluesseldatei, ohne die Bank anzusprechen.
//
// Wofuer: Das Protokoll ging verloren, der Ausdruck ist unleserlich, die Bank
// braucht es ein zweites Mal, oder jemand will die Pruefsummen der eigenen
// Schluessel nachsehen. init.php erneut laufen zu lassen waere dafuer der
// falsche Weg — es wuerde erneut INI/HIA senden.
//
// ⚠️ DIESES SKRIPT SENDET NICHTS. Es baut gar keinen EBICS-Client auf; es gibt
// hier strukturell keinen Weg zur Bank, nicht bloss keinen Aufruf. Ein Test
// haelt das fest (tests/run.php, „letter.php sendet strukturell nichts").
//
// ⚠️ FANGGRUBE (live erkauft, 20.08.2026): Die PDF-Erzeugung braucht das
// Paket `setasign/fpdf` — NICHT `fpdf/fpdf`. Die Bibliothek nennt das richtige
// Paket selbst ueber `composer suggest`; das falsche laesst sich installieren
// und scheitert erst beim Erzeugen. Es steht deshalb fest in composer.json.
//
// Aufruf:  docker compose run --rm ebics-abholer php /app/letter.php

declare(strict_types=1);

require_once __DIR__ . '/vendor/autoload.php';
require_once __DIR__ . '/logik.php';
require_once __DIR__ . '/version.php';
require_once __DIR__ . '/start.php';

use EbicsApi\Ebics\EbicsBankLetter;
use EbicsApi\Ebics\Services\FileKeyringManager;

try {
    $cfg = ebicsKonfigLaden();

    $manager = new FileKeyringManager();
    $keyring = ebicsKeyringLaden($cfg, $manager);

    $brief = new EbicsBankLetter();
    $inhalt = $brief->prepareBankLetter(ebicsBank($cfg), ebicsTeilnehmer($cfg), $keyring);

    // Zuerst als Text auf die Konsole — wer nur die Pruefsummen nachsehen
    // will, braucht dafuer keinen PDF-Betrachter.
    echo $brief->formatBankLetter($inhalt, $brief->createTxtBankLetterFormatter()), "\n";

    $pdf = $brief->formatBankLetter($inhalt, $brief->createPdfBankLetterFormatter());

    // Niemals ueberschreiben: Das alte Protokoll kann das sein, das bei der
    // Bank liegt.
    $pfad = ebicsFreierPfad(ebicsGeheimVerzeichnis(), 'initialisierungsprotokoll.pdf', 'file_exists');
    if (file_put_contents($pfad, $pdf) === false) {
        throw new EbicsAblageFehler("Konnte das Protokoll nicht schreiben: $pfad");
    }

    echo "Initialisierungsprotokoll geschrieben: $pfad\n";
    echo "Es enthaelt NUR Pruefsummen oeffentlicher Schluessel — kein Geheimnis.\n";
    exit(0);
} catch (EbicsKonfigFehler $e) {
    fwrite(STDERR, $e->getMessage() . "\n");
    exit(1);
} catch (Throwable $e) {
    fwrite(STDERR, $e->getMessage() . "\n");
    exit(2);
}
