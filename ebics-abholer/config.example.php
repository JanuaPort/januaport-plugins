<?php
// ============================================================================
// VORLAGE — nach config.php kopieren und VON HAND ausfuellen.
//
// ⚠️ config.php NIEMALS durch Chat, Agenten oder Werkzeuge schleusen. Alle
// Werte stehen im EBICS-Schreiben der Bank; das Schluesselwort waehlt der
// Betreiber selbst und legt es in seinen Passwortmanager.
//
// Die fertige Datei gehoert dem Nutzer, unter dem der Abholer laeuft (UID
// 65532), und traegt 0600. Sie liegt NICHT im Repo (.gitignore).
// ============================================================================

return [
    // ── Aus dem Schreiben der Bank abtippen ─────────────────────────────────
    'host_id'  => 'HIER-EBICS-HOSTNAME',   // „EBICS-Hostname" / „Host-ID"
    'host_url' => 'HIER-URL',              // „URL des EBICS-Bankzugangs" (https://…)

    'partner_id' => 'HIER-KUNDEN-ID',      // „Kunden-ID" / „Partner-ID"
    'user_id'    => 'HIER-TEILNEHMER-ID',  // „Teilnehmer-ID" / „User-ID"

    // 'H005' (EBICS 3.0) oder 'H004' (EBICS 2.5) — steht im selben Schreiben.
    // Im Zweifel die hoechste dort genannte Version; was der Bank-Host wirklich
    // kann, sagt er selbst per HEV (Runbook, Abschnitt „Version klaeren").
    'ebics_version' => 'HIER-H005-ODER-H004',

    // Frei waehlen, im Passwortmanager ablegen — schuetzt die Schluesseldatei
    // (keyring.json). Beides wandert gemeinsam ins Backup: OHNE das Wort ist
    // die Schluesseldatei wertlos und die Initialisierung muss samt Brief neu.
    'keyring_password' => 'HIER-NEUES-PASSWORT',

    // ── Ablage ─────────────────────────────────────────────────────────────
    // Das kontrollierte Verzeichnis, aus dem JanuaPort die Auszuege liest
    // (Datei-Integrationsart). IM CONTAINER, absolut. Der Compose-Dienst
    // mountet den Host-Ordner genau hierhin.
    'ablage_verzeichnis' => '/ablage',

    // ── Optional: die Abruf-Kennung ────────────────────────────────────────
    // Die Vorgaben unten sind die DK-Standardzuordnung fuer den camt.053-
    // Tagesauszug in Deutschland. Sie passen bei den allermeisten Instituten.
    //
    // ⚠️ Verbindlich beantwortet das nicht die Hotline, sondern die Bank
    // selbst: HAA (welche Abrufe darf dieser Teilnehmer) und HTD
    // (Teilnehmer-/Kontenzuordnung) direkt nach der Freischaltung. Weicht die
    // Selbstauskunft ab, werden die Werte hier nachgezogen — dafuer sind sie
    // konfigurierbar und nicht einbetoniert.

    // H005 (EBICS 3.0): BTD mit BTF-Kennung.
    //'btf_service'          => 'EOP',       // Service („End of Period" = Auszug)
    //'btf_scope'            => 'DE',        // Geltungsbereich
    //'btf_msg_name'         => 'camt.053',  // Nachrichtenart
    //'btf_msg_name_version' => '08',        // nur setzen, wenn die Bank eine Fassung verlangt
    //'btf_service_option'   => '',          // nur setzen, wenn die Bank eine Option verlangt
    //'btf_container'        => 'ZIP',       // die Bank buendelt mehrere Auszuege

    // H004 (EBICS 2.5): FDL mit Dateiformat und Laenderkennung.
    //'h004_file_format' => 'camt.053',
    //'h004_land'        => 'DE',
];
