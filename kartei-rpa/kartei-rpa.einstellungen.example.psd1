# JanuaPort kartei-rpa - Beispiel der optionalen Einstellungsdatei.
#
# Nur noetig, wenn Ihre Satzart andere Feldnamen traegt als die Voreinstellung
# (die Satzart "zahlungseingang" des Rezepts) oder der Token unter einem anderen
# Namen im Credential-Manager liegt. Sonst: diese Datei einfach weglassen.
#
# Gebrauch: kopieren nach "kartei-rpa.einstellungen.psd1" (ohne ".example"),
# in denselben Ordner wie JanuaPort.Kartei.psm1, und nur die Zeilen behalten,
# die Sie aendern. Was fehlt, bleibt auf der Voreinstellung. Ein unbekannter
# Schluessel ist ein Fehler - ein Tippfehler faellt nicht still zurueck.
#
# Nur ASCII (Windows PowerShell 5.1). Kein Token in diese Datei - der liegt im
# Windows-Credential-Manager unter CredentialTarget.
#
# Die Werte unten SIND die Voreinstellungen.
@{
    # Name des Credential-Manager-Eintrags (cmdkey /generic:<Name>).
    CredentialTarget = 'jnpt-roboter'

    # Was jedes update als "source" behauptet - steht im Audit der Anlage.
    Quelle = 'roboter:buchung'

    # Welches Feld der Satzart welche Rolle traegt.
    Felder = @{
        Buchungsnr   = 'buchungs_nr'     # Beleg der Buchung aus dem Fachsystem
        Lauf         = 'roboter_lauf'    # Lauf-Kennung - wer den Satz gesperrt hat
        FehlerText   = 'fehler_text'     # eine Zeile, hoechstens 200 Zeichen
        BearbeitetAm = 'bearbeitet_am'   # Zeitpunkt des letzten Schreibschritts
    }

    # Was Schritt 1 und 2 je Satz an den Flow geben (dazu immer "id").
    # Genau die Werte, die die Klickfolge im Fachsystem tippt - nicht mehr.
    Ausgabefelder = @('beleg_nummer', 'nummer_art', 'betrag', 'buchungsdatum')
}
