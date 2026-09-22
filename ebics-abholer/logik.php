<?php
// Reine Logik des EBICS-Abholers (#549) — die Regeln, die ohne Bank gelten.
//
// ⚠️ Diese Datei haengt an KEINER Bibliothek und macht KEIN I/O. Genau deshalb
// ist sie testbar, ohne dass irgendetwas installiert wird oder eine Bank
// angesprochen wuerde. Alles, was mit der Bank spricht, steht in fetch.php und
// ist dort eine duenne Schale um diese Funktionen.
//
// Wer hier etwas ergaenzt: es gehoert nur hierher, wenn es sich mit reinen
// Werten pruefen laesst. Sonst gehoert es in die Schale.

declare(strict_types=1);

/** Die Konfiguration ist unbrauchbar — es wurde nichts abgerufen. Exit 1. */
final class EbicsKonfigFehler extends RuntimeException
{
}

/**
 * Abgerufen, aber nicht abgelegt. Exit 3.
 *
 * ⚠️ Das ist der einzige Fehler, der NACH dem Reden mit der Bank auftreten
 * kann. Er wird deshalb bewusst getrennt gefuehrt: In diesem Fall quittiert
 * fetch.php den Empfang NEGATIV, damit die Bank die Daten weiter als neu
 * fuehrt und der naechste Lauf sie erneut bekommt.
 */
final class EbicsAblageFehler extends RuntimeException
{
}

/**
 * Die eingegebenen Pruefsummen passen nicht zu denen, die die Bank gerade
 * geliefert hat. Exit 5 von `hpb.php --uebernehmen`.
 *
 * ⚠️ Er wird getrennt gefuehrt, weil er das genaue Gegenteil eines
 * Betriebsfehlers ist: Das Werkzeug hat funktioniert, der Abgleich hat
 * ANGESCHLAGEN. Es wurde nichts gespeichert und nichts gesichert; die alte
 * Schluesseldatei liegt unveraendert am Platz.
 */
final class EbicsUebernahmeFehler extends RuntimeException
{
}

// ── Versionswahl ────────────────────────────────────────────────────────────

/**
 * Liefert 'H005' oder 'H004' — die einzigen beiden Staende, die der Abholer
 * bedient. Welcher gilt, steht im Schreiben der Bank; im Zweifel sagt es die
 * Bank selbst per HEV (Versions-Selbstauskunft, siehe Runbook).
 */
function ebicsVersionKennung(array $cfg): string
{
    $v = strtoupper(trim((string)($cfg['ebics_version'] ?? '')));

    return match ($v) {
        'H005', 'H004' => $v,
        default => throw new EbicsKonfigFehler(
            "config.php: 'ebics_version' muss 'H005' oder 'H004' sein (steht im Schreiben der Bank)."
        ),
    };
}

// ── Konfigurations-Pruefung ─────────────────────────────────────────────────

/** Pflichtwerte der config.php, in der Reihenfolge der Vorlage. */
const EBICS_PFLICHTWERTE = [
    'host_id',
    'host_url',
    'partner_id',
    'user_id',
    'ebics_version',
    'keyring_password',
    'ablage_verzeichnis',
];

/**
 * Alle Beanstandungen der Konfiguration als Klartext-Liste — bewusst ALLE
 * auf einmal: Wer eine frische config.php ausfuellt, soll nicht siebenmal
 * hintereinander denselben Lauf starten muessen.
 *
 * @return list<string> leer = brauchbar
 */
function ebicsKonfigFehler(array $cfg): array
{
    $fehler = [];

    foreach (EBICS_PFLICHTWERTE as $schluessel) {
        $wert = trim((string)($cfg[$schluessel] ?? ''));

        if ($wert === '') {
            $fehler[] = "config.php: '$schluessel' fehlt oder ist leer.";
            continue;
        }

        if (str_starts_with($wert, 'HIER-')) {
            $fehler[] = "config.php: '$schluessel' ist noch nicht ausgefuellt.";
            continue;
        }

        if ($schluessel === 'ebics_version' && !in_array(strtoupper($wert), ['H005', 'H004'], true)) {
            $fehler[] = "config.php: 'ebics_version' muss 'H005' oder 'H004' sein (steht im Schreiben der Bank).";
        }

        // ⚠️ EBICS laeuft ueber TLS. Eine Klartext-URL waere kein Tippfehler,
        // sondern ein offener Kanal zum Firmenkonto — hier endet der Lauf,
        // bevor irgendetwas gesendet wird.
        if ($schluessel === 'host_url' && !str_starts_with(strtolower($wert), 'https://')) {
            $fehler[] = "config.php: 'host_url' muss mit https:// beginnen — EBICS wird nie im Klartext gesprochen.";
        }

        // Ein relativer Pfad haengt am Arbeitsverzeichnis des Aufrufers. Der
        // Aufrufer ist hier ein systemd-Timer — sein Arbeitsverzeichnis ist
        // nichts, worauf sich eine Ablage verlassen darf.
        if ($schluessel === 'ablage_verzeichnis' && !str_starts_with($wert, '/')) {
            $fehler[] = "config.php: 'ablage_verzeichnis' muss ein absoluter Pfad sein (mit / beginnen).";
        }
    }

    return $fehler;
}

// ── Pfade ───────────────────────────────────────────────────────────────────

/**
 * Wo die Zugangsdaten liegen.
 *
 * Der Code steckt im Image (`/app`), die Geheimnisse kommen von aussen: Ein
 * eigenes, restriktiv berechtigtes Verzeichnis wird hineingemountet. Deshalb
 * nicht `__DIR__` — sonst muesste die Schluesseldatei ins Image.
 *
 * Umlenkbar per `JNPT_EBICS_CONFIG`, damit derselbe Werkzeugkasten auch ad hoc
 * aus einem bind-gemounteten Ordner laufen kann (so lief die Initialisierung
 * am 20.08.2026).
 */
function ebicsKonfigPfad(array $env): string
{
    return ebicsPfadAusUmgebung($env, 'JNPT_EBICS_CONFIG', '/geheim/config.php');
}

/** Wo die Schluesseldatei liegt. Umlenkbar per `JNPT_EBICS_KEYRING`. */
function ebicsKeyringPfad(array $env): string
{
    return ebicsPfadAusUmgebung($env, 'JNPT_EBICS_KEYRING', '/geheim/keyring.json');
}

/** Ein gesetzter, aber leerer Wert ist keine Umlenkung, sondern ein Versehen. */
function ebicsPfadAusUmgebung(array $env, string $schluessel, string $default): string
{
    $wert = trim((string)($env[$schluessel] ?? ''));

    return $wert === '' ? $default : $wert;
}

// ── Datumsfenster ───────────────────────────────────────────────────────────

/**
 * Das Abrufsfenster aus den Kommandozeilen-Argumenten — oder `null`.
 *
 * ⚠️ `null` ist der NORMALFALL und keine Luecke: Ohne Zeitraum liefert die
 * Bank genau das, was dieser Teilnehmer noch nicht quittiert hat. Das ist die
 * Buchfuehrung, auf der der taegliche Lauf steht — sie macht einen verpassten
 * Tag folgenlos und einen doppelten Lauf harmlos. Ein Fenster ist der
 * ausdrueckliche Nachzug von Hand („hol mir den 1. bis 5. nochmal"), und der
 * kann Dateien doppelt bringen (sie werden dann nummeriert, nie ueberschrieben).
 *
 * @param list<string> $argumente ohne den Skriptnamen
 * @return array{0: DateTimeImmutable, 1: DateTimeImmutable}|null
 */
function ebicsZeitfensterAusArgumenten(array $argumente): ?array
{
    $von = null;
    $bis = null;

    foreach ($argumente as $argument) {
        if (str_starts_with($argument, '--von=')) {
            $von = ebicsDatum(substr($argument, 6), '--von');
            continue;
        }
        if (str_starts_with($argument, '--bis=')) {
            $bis = ebicsDatum(substr($argument, 6), '--bis');
            continue;
        }
        throw new EbicsKonfigFehler(
            "Unbekanntes Argument '$argument'. Erlaubt sind nur --von=JJJJ-MM-TT und --bis=JJJJ-MM-TT."
        );
    }

    if ($von === null && $bis === null) {
        return null;
    }

    if ($von === null || $bis === null) {
        throw new EbicsKonfigFehler('--von und --bis gehoeren zusammen — ein halbes Fenster gibt es nicht.');
    }

    if ($von > $bis) {
        throw new EbicsKonfigFehler('--von liegt nach --bis.');
    }

    return [$von, $bis];
}

/** Ein Datum in der Form JJJJ-MM-TT — streng, inklusive Kalenderpruefung. */
function ebicsDatum(string $roh, string $wofuer): DateTimeImmutable
{
    $datum = DateTimeImmutable::createFromFormat('!Y-m-d', $roh);

    // createFromFormat rollt Unsinn still weiter (2026-02-30 wird der 2. Maerz).
    // Der Rueckvergleich ist die Stelle, an der das auffliegt.
    if ($datum === false || $datum->format('Y-m-d') !== $roh) {
        throw new EbicsKonfigFehler("$wofuer: '$roh' ist kein Datum in der Form JJJJ-MM-TT.");
    }

    return $datum;
}

// ── Container zerlegen ──────────────────────────────────────────────────────

/** Traegt der Datenstrom die ZIP-Signatur? */
function ebicsIstZip(string $daten): bool
{
    return str_starts_with($daten, "PK\x03\x04");
}

/**
 * Die einzelnen Auszugs-Dokumente aus dem, was die Bank geliefert hat.
 *
 * H005 liefert einen ZIP-Container (BTF-Kennung `Container=ZIP`), H004 in der
 * Regel ein einzelnes Dokument. Beides wird hier auf dieselbe Liste gebracht,
 * damit die Ablage nur einen Fall kennt.
 *
 * @param callable(string): array<string, string|false> $zipEntpacker
 * @return list<string>
 */
function ebicsInhalte(string $rohdaten, callable $zipEntpacker): array
{
    if (trim($rohdaten) === '') {
        return [];
    }

    if (!ebicsIstZip($rohdaten)) {
        return [$rohdaten];
    }

    $inhalte = [];
    foreach ($zipEntpacker($rohdaten) as $inhalt) {
        // Ein nicht lesbarer oder leerer Eintrag wird uebergangen — eine leere
        // Datei in der Ablage waere fuer den Datei-Weg schlimmer als keine.
        if (!is_string($inhalt) || trim($inhalt) === '') {
            continue;
        }
        $inhalte[] = $inhalt;
    }

    return $inhalte;
}

// ── Dateinamen ──────────────────────────────────────────────────────────────

/**
 * Der Ablage-Name eines Dokuments: Datum des Abrufs plus laufende Nummer.
 *
 * ⚠️ Der Name wird GEBILDET, nie von der Bank uebernommen. Die Eintragsnamen
 * eines Bank-Containers sind Fremdeingabe; ein Eintrag namens
 * `../../etc/cron.d/boese` waere sonst genau das, wonach er aussieht.
 */
function ebicsDateiname(string $datum, int $laufendeNummer): string
{
    return sprintf('camt053-%s-%02d.xml', $datum, $laufendeNummer);
}

/**
 * Der erste freie Pfad zu diesem Namen — bei Kollision wird nummeriert.
 *
 * ⚠️ Ueberschrieben wird NIE. Ein Kontoauszug ist Geld; eine still ersetzte
 * Datei waere ein lautloser Verlust. Lieber zwei Dateien zu viel im
 * Verzeichnis als eine fehlende Buchung.
 *
 * @param callable(string): bool $existiert
 */
function ebicsFreierPfad(string $verzeichnis, string $name, callable $existiert): string
{
    $verzeichnis = rtrim($verzeichnis, '/');
    $endung = pathinfo($name, PATHINFO_EXTENSION);
    $stamm = $endung === '' ? $name : substr($name, 0, -(strlen($endung) + 1));

    for ($nummer = 1; $nummer <= 999; $nummer++) {
        $kandidat = $nummer === 1 ? $stamm : $stamm . '-' . $nummer;
        $pfad = $verzeichnis . '/' . ($endung === '' ? $kandidat : $kandidat . '.' . $endung);

        if (!$existiert($pfad)) {
            return $pfad;
        }
    }

    throw new EbicsAblageFehler(
        "Kein freier Name fuer '$name' in '$verzeichnis' nach 999 Versuchen — Verzeichnis pruefen."
    );
}

// ── Exit-Codes ──────────────────────────────────────────────────────────────

/**
 * Der Exit-Code fuer den systemd-Timer.
 *
 * | 0 | geholt — oder nichts Neues da (das ist KEIN Fehler)            |
 * | 1 | Konfiguration unbrauchbar — es wurde nichts abgerufen          |
 * | 2 | echter Fehler beim Abruf (Bank, Netz, Protokoll)               |
 * | 3 | abgerufen, aber nicht abgelegt — negativ quittiert, Daten bleiben bei der Bank |
 * | 4 | die Bankschluessel passen nicht mehr (091008) — nichts abgerufen, nichts quittiert |
 *
 * Die Klasse kommt als Zeichenkette herein, damit diese Entscheidung ohne die
 * Bibliothek pruefbar bleibt.
 */
function ebicsExitCode(?string $fehlerKlasse, ?string $ebicsCode): int
{
    if ($fehlerKlasse === null) {
        return 0;
    }

    // ⚠️ Der Leerlauf zuerst: 090005 ist ERFOLG und darf von keinem der
    // folgenden Zweige eingefangen werden.
    if (ebicsIstLeerlauf($fehlerKlasse, $ebicsCode)) {
        return 0;
    }

    if (ebicsIstBankschluesselwechsel($fehlerKlasse, $ebicsCode)) {
        return 4;
    }

    return match ($fehlerKlasse) {
        EbicsKonfigFehler::class => 1,
        EbicsAblageFehler::class => 3,
        default => 2,
    };
}

/**
 * „Die Bank hat nichts Neues" — der haeufigste Ausgang eines taeglichen Laufs.
 *
 * Die Bibliothek meldet das als Ausnahme (EBICS-Antwortcode 090005). Fuer den
 * Timer ist es ein gelungener Lauf: An einem Wochenende oder Feiertag gibt es
 * schlicht keinen Auszug. Ein Dienst, der sonntags rot wird, erzieht Betreiber
 * dazu, rote Dienste zu ignorieren.
 */
function ebicsIstLeerlauf(?string $fehlerKlasse, ?string $ebicsCode): bool
{
    if ($ebicsCode === '090005') {
        return true;
    }

    if ($fehlerKlasse === null) {
        return false;
    }

    $kurz = substr((string)strrchr('\\' . $fehlerKlasse, '\\'), 1);

    return $kurz === 'NoDownloadDataAvailableException';
}

// ── Schluesselwechsel der Bank (#617) ───────────────────────────────────────

/**
 * „Die Bank hat ihre Schluessel gewechselt" — der Ausgang, den es vor #617
 * nicht als eigenen Fall gab.
 *
 * ⚠️ Der Fehler kommt von der BANK, nicht von uns: In jedem Abruf-Auftrag
 * reisen die Pruefsummen der uns bekannten Bankschluessel mit
 * (`addBankPubKeyDigests`). Passen sie nicht mehr, bricht die Bank die
 * Transaktionsinitialisierung mit 091008 ab. Die Bibliothek prueft den
 * Antwortcode direkt nach dem Senden und VOR dem Auspacken — es gibt also
 * weder Auftragsdaten noch eine Quittung, und die Auszuege bleiben bei der
 * Bank liegen.
 *
 * Abgrenzung: 061001 (die Bank weist UNSERE Signatur zurueck) ist etwas
 * anderes und bleibt Exit 2.
 */
function ebicsIstBankschluesselwechsel(?string $fehlerKlasse, ?string $ebicsCode): bool
{
    if ($ebicsCode === '091008') {
        return true;
    }

    if ($fehlerKlasse === null) {
        return false;
    }

    $kurz = substr((string)strrchr('\\' . $fehlerKlasse, '\\'), 1);

    return $kurz === 'BankPubkeyUpdateRequiredException';
}

/**
 * Was der Betreiber im Protokoll liest, wenn die Bankschluessel nicht mehr
 * passen.
 *
 * ⚠️ Sie nimmt NICHTS entgegen. Der Fehlerpfad der Bibliothek fuehrt den
 * Report-Text der Bank mit; er darf nicht ins journald (Leitplanke: keine
 * Bankinhalte im Protokoll). Eine Meldung ohne Parameter kann strukturell
 * keinen tragen.
 */
function ebicsSchluesselwechselMeldung(): string
{
    return "Bankschluessel passen nicht mehr (EBICS 091008) — hat die Bank ihre Schluessel gewechselt?\n"
        . "Es wurde NICHTS abgerufen und NICHTS quittiert; die Auszuege liegen weiter bei der Bank.\n"
        . "Naechster Handgriff:  php /app/hpb.php --pruefen  (holt die Bankschluessel, speichert nichts).\n"
        . 'Siehe docs/ebics-abholer.md, Abschnitt "Schluesselwechsel der Bank".';
}

/**
 * Eine Pruefsumme auf das reduzieren, was zaehlt: die Hex-Zeichen.
 *
 * Bank-Schreiben drucken sie mal in Paaren, mal mit Doppelpunkten, mal gross,
 * mal klein. Gross-/Kleinschreibung und Gruppierung sind reine Darstellung —
 * diese Regel steht seit dem ersten Abgleich in der Doku und wird hier Code,
 * damit sie nicht von Hand angewandt werden muss.
 */
function ebicsPruefsummeNormalisieren(?string $roh): string
{
    return strtolower((string)preg_replace('~[^0-9A-Fa-f]~', '', (string)$roh));
}

/**
 * Vergleicht die gespeicherten mit den soeben geholten Bankschluesseln.
 *
 * ⚠️ Verglichen werden ZWEI Werte je Schluessel: der Public-Key-Digest (das,
 * was im Bankschreiben steht) UND der Fingerabdruck des Zertifikats. Bei H005
 * sendet die Bibliothek den ZERTIFIKATS-Fingerabdruck an die Bank
 * (DigestResolverV3), nicht den Schluessel-Digest. Erneuert die Bank nur ihr
 * Zertifikat bei gleichem Schluesselpaar, waere der Schluessel „unveraendert"
 * — und der naechste Abruf liefe trotzdem in 091008. Genau dieser Sonderfall
 * wird getrennt gemeldet.
 *
 * @param array<string, array{schluessel: string, zertifikat?: ?string}> $alt
 * @param array<string, array{schluessel: string, zertifikat?: ?string}> $neu
 * @return array{geaendert: bool, schluessel_geaendert: list<string>, nur_zertifikat: list<string>}
 */
function ebicsPruefsummenLage(array $alt, array $neu): array
{
    $schluesselGeaendert = [];
    $nurZertifikat = [];

    foreach (array_keys($alt + $neu) as $version) {
        $altSchluessel = ebicsPruefsummeNormalisieren($alt[$version]['schluessel'] ?? null);
        $neuSchluessel = ebicsPruefsummeNormalisieren($neu[$version]['schluessel'] ?? null);

        if ($altSchluessel !== $neuSchluessel) {
            $schluesselGeaendert[] = (string)$version;
            continue;
        }

        if (
            ebicsPruefsummeNormalisieren($alt[$version]['zertifikat'] ?? null)
            !== ebicsPruefsummeNormalisieren($neu[$version]['zertifikat'] ?? null)
        ) {
            $nurZertifikat[] = (string)$version;
        }
    }

    return [
        'geaendert' => $schluesselGeaendert !== [] || $nurZertifikat !== [],
        'schluessel_geaendert' => $schluesselGeaendert,
        'nur_zertifikat' => $nurZertifikat,
    ];
}

/** Die drei Betriebsarten von `hpb.php`. */
const EBICS_HPB_ERSTABRUF = 'erstabruf';
const EBICS_HPB_PRUEFEN = 'pruefen';
const EBICS_HPB_UEBERNEHMEN = 'uebernehmen';

/**
 * Die Betriebsart von `hpb.php` aus den Kommandozeilen-Argumenten.
 *
 * ⚠️ Es gibt genau vier erlaubte Argumente, alles andere wird abgewiesen.
 * Insbesondere nimmt kein Argument ein Geheimnis entgegen: Ein Schluesselwort
 * auf der Kommandozeile landete in der Prozessliste und im Shell-Verlauf. Es
 * kommt ausschliesslich aus der `config.php`.
 *
 * @param list<string> $argumente ohne den Skriptnamen
 * @return array{modus: string, x002: string, e002: string}
 */
function ebicsUebernahmeArgumente(array $argumente): array
{
    $modus = EBICS_HPB_ERSTABRUF;
    $x002 = '';
    $e002 = '';

    foreach ($argumente as $argument) {
        if ($argument === '--pruefen' || $argument === '--uebernehmen') {
            if ($modus !== EBICS_HPB_ERSTABRUF) {
                throw new EbicsKonfigFehler(
                    '--pruefen und --uebernehmen schliessen einander aus — erst pruefen, dann uebernehmen.'
                );
            }
            $modus = $argument === '--pruefen' ? EBICS_HPB_PRUEFEN : EBICS_HPB_UEBERNEHMEN;
            continue;
        }

        if (str_starts_with($argument, '--x002=')) {
            $x002 = trim(substr($argument, 7));
            continue;
        }

        if (str_starts_with($argument, '--e002=')) {
            $e002 = trim(substr($argument, 7));
            continue;
        }

        throw new EbicsKonfigFehler(
            "Unbekanntes Argument '$argument'. hpb.php kennt nur:\n"
            . "  (ohne Argument)                              Erstabruf nach der Freischaltung\n"
            . "  --pruefen                                    vergleichen, nichts speichern\n"
            . '  --uebernehmen --x002=<hex> --e002=<hex>      neue Bankschluessel uebernehmen'
        );
    }

    if ($modus === EBICS_HPB_UEBERNEHMEN && ($x002 === '' || $e002 === '')) {
        throw new EbicsKonfigFehler(
            "--uebernehmen braucht BEIDE Pruefsummen aus dem NEUEN Schreiben der Bank:\n"
            . "  --x002=<hex> --e002=<hex>\n"
            . 'Ein halber Abgleich ist keiner — ohne beide wird nichts gespeichert.'
        );
    }

    if ($modus !== EBICS_HPB_UEBERNEHMEN && ($x002 !== '' || $e002 !== '')) {
        throw new EbicsKonfigFehler(
            '--x002/--e002 gehoeren zu --uebernehmen — allein bewirken sie nichts, und still zu '
            . 'ignorieren waere hier die falsche Freundlichkeit.'
        );
    }

    return ['modus' => $modus, 'x002' => $x002, 'e002' => $e002];
}

/**
 * Der Abgleich, der ueber das Speichern entscheidet.
 *
 * ⚠️ DAS IST DER EINZIGE SCHUTZ GEGEN EINEN UNTERGESCHOBENEN GEGENUEBER. Es
 * gibt hier bewusst keine Abkuerzung: kein „--ja", kein Auto-Accept, keine
 * Teil-Bestaetigung. Eingegeben werden die Pruefsummen aus dem NEUEN Schreiben
 * der Bank — nie die, die ein Bildschirm gerade angezeigt hat.
 *
 * @param array<string, string> $bestaetigt aus dem neuen Bankschreiben, je X002/E002
 * @param array<string, string> $geliefert  was die Bank soeben geschickt hat
 * @throws EbicsUebernahmeFehler wenn auch nur eine der beiden abweicht
 */
function ebicsUebernahmePruefen(array $bestaetigt, array $geliefert): void
{
    $abweichungen = [];

    foreach (['X002', 'E002'] as $version) {
        $eingegeben = ebicsPruefsummeNormalisieren($bestaetigt[$version] ?? null);
        $gelieferte = ebicsPruefsummeNormalisieren($geliefert[$version] ?? null);

        // Eine leere Eingabe ist keine Bestaetigung — auch dann nicht, wenn
        // die Gegenseite ebenfalls nichts geliefert hat.
        if ($eingegeben === '' || $eingegeben !== $gelieferte) {
            $abweichungen[] = sprintf(
                "  %s\n    die Bank lieferte : %s\n    eingegeben wurde  : %s",
                $version,
                $gelieferte === '' ? '(kein Schluessel)' : strtoupper($gelieferte),
                $eingegeben === '' ? '(nichts)' : strtoupper($eingegeben)
            );
        }
    }

    if ($abweichungen === []) {
        return;
    }

    throw new EbicsUebernahmeFehler(
        "ABGLEICH ANGESCHLAGEN — es wurde NICHTS gespeichert.\n"
        . implode("\n", $abweichungen) . "\n\n"
        . "Verglichen werden nur die Hex-Zeichen; Gross-/Kleinschreibung und Gruppierung sind egal.\n"
        . "Stimmen die Zeichen wirklich nicht mit dem NEUEN Schreiben der Bank ueberein: STOPP,\n"
        . 'nichts abrufen, bei der Bank melden.'
    );
}

/**
 * Wohin die alte Schluesseldatei gesichert wird, bevor eine neue geschrieben
 * wird.
 *
 * ⚠️ Dieselbe Regel wie bei den Auszuegen: Ein belegter Name wird nummeriert,
 * ueberschrieben wird NIE. Eine still ersetzte Schluesseldatei waere der
 * Verlust des Bankzugangs — und ohne sie ist die volle Initialisierung samt
 * Brief und Frist wieder faellig.
 *
 * @param callable(string): bool $existiert
 */
function ebicsSicherungsPfad(string $keyringPfad, callable $existiert): string
{
    return ebicsFreierPfad(dirname($keyringPfad), 'sicherung-01.' . basename($keyringPfad), $existiert);
}

/**
 * Der blanke Aufruf von `hpb.php` ist der ERSTABRUF nach der Freischaltung —
 * und nur der.
 *
 * ⚠️ Stehen schon Bankschluessel in der Schluesseldatei, holte er neue und
 * schriebe sie UNGEPRUEFT darueber. Genau auf diesem Weg kaeme ein
 * untergeschobener Gegenueber herein. Deshalb: verweigert, mit dem Verweis auf
 * die beiden Handgriffe, die den Abgleich erzwingen.
 */
function ebicsErstabrufPruefen(bool $hatBankschluessel): void
{
    if (!$hatBankschluessel) {
        return;
    }

    throw new EbicsKonfigFehler(
        "In der Schluesseldatei stehen bereits Bankschluessel — der blanke Aufruf ist der\n"
        . "Erstabruf nach der Freischaltung und wird hier VERWEIGERT: Er holte neue Schluessel\n"
        . "und schriebe sie ungeprueft ueber die alten.\n\n"
        . "  php /app/hpb.php --pruefen                              vergleichen, aendert nichts\n"
        . "  php /app/hpb.php --uebernehmen --x002=<hex> --e002=<hex>  uebernehmen, mit Abgleich\n\n"
        . 'Die Pruefsummen stammen aus dem NEUEN Schreiben der Bank (docs/ebics-abholer.md).'
    );
}

// ── Protokoll ───────────────────────────────────────────────────────────────

/**
 * Die eine Zeile, die ein Lauf ueber seine Beute sagt.
 *
 * ⚠️ Sie nimmt AUSSCHLIESSLICH Namen entgegen. Ein Kontoumsatz kann hier
 * strukturell nicht hineingeraten — das ist staerker als die Absicht, keinen
 * hineinzuschreiben (Leitplanke: keine Kontodaten im journald).
 *
 * @param list<string> $namen
 */
function ebicsLogSatz(array $namen): string
{
    if ($namen === []) {
        return '0 Dateien abgelegt (die Bank hatte nichts Neues).';
    }

    return sprintf('%d Dateien abgelegt: %s', count($namen), implode(', ', $namen));
}
