<?php
// Versions-Auflösung fuer alle Skripte des Abholers (#549).
//
// Diese Datei ist die duenne Schale um `ebicsVersionKennung()` aus logik.php:
// Sie uebersetzt 'H005'/'H004' in die Konstanten der Bibliothek und setzt,
// was an der jeweiligen Version haengt. Die Regel selbst (was ist gueltig?)
// steht in der reinen Logik und ist dort getestet.
//
// ⚠️ FANGGRUBE, live erkauft am 20.08.2026 (erster Lauf, Code 090004
// „ungueltiges Auftragsdatenformat"): Bei H005 (EBICS 3.0) reisen die
// Teilnehmerschluessel als SELBST-SIGNIERTE ZERTIFIKATE. Ohne vorab gesetzten
// Zertifikatsgenerator lehnt die Bank INI ab. „Mit Schluesseln" im Bank-
// Schreiben heisst nur: keine Zertifizierungsstelle noetig — NICHT: kein
// Zertifikatsformat. Der Fehlversuch hinterliess nichts (Auftrag abgewiesen,
// nichts registriert); mit neu erzeugten Schluesseln lief der zweite Versuch
// sauber durch.
//
// H004 (EBICS 2.5) kennt das klassische Schluesselformat und braucht den
// Generator nicht.

declare(strict_types=1);

use EbicsApi\Ebics\Models\Bank;
use EbicsApi\Ebics\Models\Keyring;
use EbicsApi\Ebics\Models\X509\BankX509Generator;

/** Die Versions-Konstante der Bibliothek zur Kennung aus der Konfiguration. */
function ebicsKeyringVersion(array $cfg): string
{
    return match (ebicsVersionKennung($cfg)) {
        'H005' => Keyring::VERSION_30,
        'H004' => Keyring::VERSION_25,
    };
}

/** Setzt, was an der Version haengt — bei H005 die Zertifikatshuelle. */
function ebicsApplyVersionSpecifics(array $cfg, Keyring $keyring, Bank $bank): void
{
    if (ebicsKeyringVersion($cfg) !== Keyring::VERSION_30) {
        return;
    }

    $generator = new BankX509Generator();
    $generator->setCertificateOptionsByBank($bank);
    $keyring->setCertificateGenerator($generator);
}
