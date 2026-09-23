# JanuaPort kartei-rpa - eine Kartei-Arbeitsliste von einem RPA-Werkzeug abarbeiten lassen.
#
# Die vier Beruehrungen eines Roboter-Laufs mit JanuaPort ueber /mcp:
# Arbeitsliste holen, Satz sperren, Ergebnis schreiben, Fehler schreiben.
# Jede Schreibfunktion liest den Satz vorher (get), prueft Status UND eigene
# Lauf-Kennung und schickt beim update ALLE gelesenen Felder zurueck.
#
# Bewusst NICHT hier: die Schleife (sie lebt im RPA-Flow, weil jede Skript-
# Aktion ein eigener Prozess ohne geteilten Zustand ist), ein Retry auf
# Schreibschritten, das Aufraeumen haengender Saetze, add und delete.
#
# Feldnamen, Quelle und Credential-Ziel sind Einstellungen (Parameter von
# Connect-JnptKartei oder die optionale Datei kartei-rpa.einstellungen.psd1
# neben diesem Modul). Die Status-Werte sind es NICHT - sie sind der Vertrag
# des Musters arbeitsliste-rpa.
#
# ASCII-only und ohne BOM: Windows PowerShell 5.1 liest eine Datei ohne BOM als
# ANSI - ein einziges Sonderzeichen bricht den Parser. Ein Test prueft das.
#
# Kein Set-StrictMode: Der Transport lebt davon, dass ein fehlendes Feld $null
# ist (isError, structuredContent.record).

$script:JnptEinstellungsdatei = Join-Path $PSScriptRoot 'kartei-rpa.einstellungen.psd1'

# Die Status-Werte des Musters arbeitsliste-rpa - fest, nicht einstellbar.
$script:StatusFrei     = 'geprueft'
$script:StatusGesperrt = 'in_buchung'
$script:StatusVerbucht = 'verbucht'
$script:StatusFehler   = 'fehler'

# Felder, die das Modul selbst setzt; ein eingestellter Feldname darf keins davon sein.
$script:ReservierteFelder = @('id', 'status', 'source', 'as_of')

# Hoechstlaenge des Fehlertexts - der Text ist eine Meldung, kein Protokoll.
$script:JnptFehlerTextMax = 200

# Die Suche liefert hoechstens so viele Saetze je Aufruf.
$script:JnptSuchDeckel = 100

$script:JnptRpcId = 0

# ---------------------------------------------------------------------------
# Transport - JSON-RPC ueber Streamable HTTP, drei POSTs fuer den ersten Aufruf
# ---------------------------------------------------------------------------

function Invoke-JnptRpc {
    param(
        [Parameter(Mandatory)][string]$Endpoint,
        [Parameter(Mandatory)][string]$Token,
        [Parameter(Mandatory)][hashtable]$Message,
        [int]$TimeoutSec = 60
    )
    # Mcp-Protocol-Version wird bewusst NICHT gesetzt: der Server leitet die
    # Fassung aus dem Handschlag ab, ein falscher Kopf bricht ihn.
    $headers = @{
        'Authorization' = "Bearer $Token"
        'Accept'        = 'application/json, text/event-stream'
    }
    $body = ConvertTo-Json $Message -Depth 12 -Compress
    $resp = Invoke-WebRequest -Uri $Endpoint -Method Post -Headers $headers `
                -ContentType 'application/json' -Body ([Text.Encoding]::UTF8.GetBytes($body)) `
                -TimeoutSec $TimeoutSec -UseBasicParsing
    if ($resp.StatusCode -eq 202) { return $null }   # Notification: kein Rumpf
    $text = [Text.Encoding]::UTF8.GetString($resp.RawContentStream.ToArray())
    if ($resp.Headers['Content-Type'] -match 'text/event-stream') {
        $text = (($text -split "`n") | Where-Object { $_.StartsWith('data:') } |
                 ForEach-Object { $_.Substring(5).Trim() }) -join ''
    }
    $rpc = ConvertFrom-Json $text
    if ($rpc.PSObject.Properties.Name -contains 'error') {
        throw "JSON-RPC $($rpc.error.code): $($rpc.error.message)"
    }
    return $rpc.result
}

function Connect-Jnpt {
    param([Parameter(Mandatory)][string]$Endpoint, [Parameter(Mandatory)][string]$Token,
          [int]$TimeoutSec = 60)
    $script:JnptRpcId++
    $init = Invoke-JnptRpc -Endpoint $Endpoint -Token $Token -TimeoutSec $TimeoutSec -Message @{
        jsonrpc = '2.0'; id = $script:JnptRpcId; method = 'initialize'
        params  = @{
            protocolVersion = '2025-11-25'
            capabilities    = @{}
            clientInfo      = @{ name = 'januaport-kartei-rpa'; version = '1.0' }
        }
    }
    Invoke-JnptRpc -Endpoint $Endpoint -Token $Token -TimeoutSec $TimeoutSec -Message @{
        jsonrpc = '2.0'; method = 'notifications/initialized'
    } | Out-Null
    return $init.protocolVersion
}

function Invoke-JnptTool {
    param(
        [Parameter(Mandatory)][string]$Endpoint,
        [Parameter(Mandatory)][string]$Token,
        [Parameter(Mandatory)][string]$Tool,
        [hashtable]$Arguments = @{},
        [int]$TimeoutSec = 60
    )
    $script:JnptRpcId++
    $result = Invoke-JnptRpc -Endpoint $Endpoint -Token $Token -TimeoutSec $TimeoutSec -Message @{
        jsonrpc = '2.0'; id = $script:JnptRpcId; method = 'tools/call'
        params  = @{ name = $Tool; arguments = $Arguments }
    }
    if ($result.isError) { throw "Werkzeug $Tool meldet: $($result.content[0].text)" }
    return $result
}

# ---------------------------------------------------------------------------
# Token aus dem Windows-Credential-Manager
#
# Der Token gehoert NICHT in eine Variable des RPA-Werkzeugs: PAD ersetzt seine
# Variablen im Skripttext VOR der Ausfuehrung, der Wert stuende damit im Flow,
# im Export und in jedem Screenshot. Der Credential-Manager ist benutzer- UND
# geraetegebunden - cmdkey muss unter dem Konto gelaufen sein, unter dem der
# Flow laeuft.
# ---------------------------------------------------------------------------

function Get-JnptTokenFromCredentialManager {
    param([Parameter(Mandatory)][string]$Ziel)   # der /generic:-Name aus cmdkey
    if (-not ('Jnpt.Cred' -as [type])) {
        Add-Type -Namespace Jnpt -Name Cred -MemberDefinition @'
[StructLayout(LayoutKind.Sequential, CharSet=CharSet.Unicode)]
public struct CREDENTIAL {
  public uint Flags; public uint Type; public string TargetName; public string Comment;
  public System.Runtime.InteropServices.ComTypes.FILETIME LastWritten;
  public uint CredentialBlobSize; public IntPtr CredentialBlob; public uint Persist;
  public uint AttributeCount; public IntPtr Attributes; public string TargetAlias; public string UserName;
}
[DllImport("advapi32.dll", CharSet=CharSet.Unicode, SetLastError=true)]
public static extern bool CredReadW(string target, uint type, uint flags, out IntPtr credential);
[DllImport("advapi32.dll")] public static extern void CredFree(IntPtr buffer);
public static string Read(string target) {
  IntPtr p;
  if (!CredReadW(target, 1, 0, out p)) throw new Exception("Credential \"" + target + "\" nicht gefunden.");
  try {
    CREDENTIAL c = (CREDENTIAL)Marshal.PtrToStructure(p, typeof(CREDENTIAL));
    return Marshal.PtrToStringUni(c.CredentialBlob, (int)(c.CredentialBlobSize / 2));
  } finally { CredFree(p); }
}
'@
    }
    return [Jnpt.Cred]::Read($Ziel)
}

# ---------------------------------------------------------------------------
# Einstellungen: Voreinstellung <- Datei <- Parameter
# ---------------------------------------------------------------------------

function Get-JnptKarteiEinstellungen {
    <#
    .SYNOPSIS
      Liefert die wirksamen Einstellungen: Voreinstellung, darueber die
      optionale Datei, darueber -Vorgabe (die Parameter des Aufrufers).
    .DESCRIPTION
      Fehlt die Datei, gelten die Voreinstellungen. Ein unbekannter Schluessel
      ist ein Fehler - ein Tippfehler darf nicht still auf die Voreinstellung
      zurueckfallen.
    #>
    param(
        [string]$Pfad = $script:JnptEinstellungsdatei,
        [hashtable]$Vorgabe = @{}
    )
    $e = @{
        CredentialTarget = 'jnpt-roboter'
        Quelle           = 'roboter:buchung'
        Felder           = @{
            Buchungsnr   = 'buchungs_nr'
            Lauf         = 'roboter_lauf'
            FehlerText   = 'fehler_text'
            BearbeitetAm = 'bearbeitet_am'
        }
        Ausgabefelder    = @('beleg_nummer', 'nummer_art', 'betrag', 'buchungsdatum')
    }
    if (Test-Path -LiteralPath $Pfad -PathType Leaf) {
        Merge-JnptEinstellungen -Ziel $e -Quelle (Import-PowerShellDataFile -LiteralPath $Pfad) `
            -Herkunft ('Einstellungsdatei {0}' -f $Pfad)
    }
    Merge-JnptEinstellungen -Ziel $e -Quelle $Vorgabe -Herkunft 'Parameter'
    Assert-JnptEinstellungen -Einstellungen $e
    return $e
}

function Merge-JnptEinstellungen {
    param([Parameter(Mandatory)][hashtable]$Ziel, [hashtable]$Quelle = @{}, [Parameter(Mandatory)][string]$Herkunft)
    foreach ($k in $Quelle.Keys) {
        switch ($k) {
            'Felder' {
                $felder = $Quelle[$k]
                if ($felder -isnot [hashtable]) { throw ('{0}: Felder muss eine Hashtable sein.' -f $Herkunft) }
                foreach ($f in $felder.Keys) {
                    if (-not $Ziel.Felder.ContainsKey($f)) {
                        throw ('{0}: unbekannter Schluessel Felder.{1} - erlaubt: {2}.' -f $Herkunft, $f,
                               (($Ziel.Felder.Keys | Sort-Object) -join ', '))
                    }
                    $Ziel.Felder[$f] = [string]$felder[$f]
                }
            }
            'Ausgabefelder'    { $Ziel.Ausgabefelder = @($Quelle[$k] | ForEach-Object { [string]$_ }) }
            'CredentialTarget' { $Ziel.CredentialTarget = [string]$Quelle[$k] }
            'Quelle'           { $Ziel.Quelle = [string]$Quelle[$k] }
            default {
                throw ('{0}: unbekannter Schluessel {1} - erlaubt: CredentialTarget, Quelle, Felder, Ausgabefelder.' -f $Herkunft, $k)
            }
        }
    }
}

function Assert-JnptEinstellungen {
    param([Parameter(Mandatory)][hashtable]$Einstellungen)
    foreach ($k in @('CredentialTarget', 'Quelle')) {
        if ([string]::IsNullOrWhiteSpace($Einstellungen[$k])) { throw ('Einstellung {0} ist leer.' -f $k) }
    }
    $namen = @($Einstellungen.Felder.Values)
    foreach ($f in $Einstellungen.Felder.Keys) {
        $name = $Einstellungen.Felder[$f]
        if ([string]::IsNullOrWhiteSpace($name)) { throw ('Einstellung Felder.{0} ist leer.' -f $f) }
        if ($script:ReservierteFelder -contains $name) {
            throw ('Einstellung Felder.{0} = "{1}" ist ein Feld, das das Modul selbst setzt.' -f $f, $name)
        }
    }
    if (@($namen | Sort-Object -Unique).Count -ne $namen.Count) {
        throw 'Einstellung Felder: zwei Rollen zeigen auf dasselbe Feld.'
    }
    $aus = @($Einstellungen.Ausgabefelder | Where-Object { -not [string]::IsNullOrWhiteSpace($_) })
    if ($aus.Count -eq 0 -or $aus.Count -ne @($Einstellungen.Ausgabefelder).Count) {
        throw 'Einstellung Ausgabefelder: mindestens ein Feldname, keiner leer.'
    }
}

# ---------------------------------------------------------------------------
# Kartei-Eigenheiten
# ---------------------------------------------------------------------------

function ConvertTo-JnptSaetze {
    # search liefert structuredContent.records[] (Mehrzahl), get liefert
    # structuredContent.record (Einzahl) - beides hier auf eine Liste bringen.
    param($Result)
    if ($null -eq $Result.structuredContent) { return @() }
    if ($null -ne $Result.structuredContent.record) { return @($Result.structuredContent.record) }
    if ($null -eq $Result.structuredContent.records) { return @() }
    return @($Result.structuredContent.records)
}

function New-JnptUpdateArgs {
    # Vollstaendiges Ueberschreiben: alle Felder aus get zurueck, dann aendern.
    param([Parameter(Mandatory)]$Satz, [hashtable]$Aenderung = @{}, [Parameter(Mandatory)][string]$Quelle)
    $a = @{}
    foreach ($p in $Satz.fields.PSObject.Properties) { $a[$p.Name] = $p.Value }
    foreach ($k in $Aenderung.Keys) { $a[$k] = $Aenderung[$k] }
    $a['id']     = $Satz.id
    $a['source'] = $Quelle
    $a['as_of']  = (Get-Date -Format 'yyyy-MM-dd')
    return $a
}

function ConvertTo-JnptAusgabe {
    <#
    .SYNOPSIS
      Die Ausgabe eines Satzes fuer den RPA-Flow: id und die Ausgabefelder,
      in dieser Reihenfolge - nichts sonst.
    #>
    param([Parameter(Mandatory)]$Session, [Parameter(Mandatory)]$Satz)
    $a = [ordered]@{ id = [string]$Satz.id }
    foreach ($f in $Session.Ausgabefelder) { $a[$f] = Get-JnptFeld -Satz $Satz -Name $f }
    return $a
}

# ---------------------------------------------------------------------------
# Der Lauf - je Funktion eine Beruehrung mit JanuaPort
# ---------------------------------------------------------------------------

function Connect-JnptKartei {
    <#
    .SYNOPSIS
      Einstellungen pruefen, Token holen, Handschlag - liefert das Sitzungsobjekt.
    .DESCRIPTION
      Ein Aufruf je Skript-Aktion (jede ist ein eigener Prozess). Die
      Einstellungen werden VOR dem Token geprueft; ein Tippfehler in der Datei
      endet ohne jeden Netzaufruf. -Token ist fuer einen Handlauf von der
      Konsole; im Betrieb kommt der Token aus dem Credential-Manager.
    #>
    param(
        [Parameter(Mandatory)][string]$Endpoint,
        [Parameter(Mandatory)][string]$Satzart,
        [string]$CredentialTarget,
        [string]$Token,
        [int]$TimeoutSec = 60,
        [string]$Quelle,
        [hashtable]$Felder,
        [string[]]$Ausgabefelder,
        [string]$Einstellungsdatei = $script:JnptEinstellungsdatei
    )
    $vorgabe = @{}
    foreach ($k in @('CredentialTarget', 'Quelle', 'Felder', 'Ausgabefelder')) {
        if ($PSBoundParameters.ContainsKey($k)) { $vorgabe[$k] = $PSBoundParameters[$k] }
    }
    $e = Get-JnptKarteiEinstellungen -Pfad $Einstellungsdatei -Vorgabe $vorgabe

    if ([string]::IsNullOrWhiteSpace($Token)) {
        $Token = Get-JnptTokenFromCredentialManager -Ziel $e.CredentialTarget
    }
    $fassung = Connect-Jnpt -Endpoint $Endpoint -Token $Token -TimeoutSec $TimeoutSec
    return [pscustomobject]@{
        Endpoint         = $Endpoint
        Token            = $Token
        Satzart          = $Satzart
        Protokollfassung = $fassung
        TimeoutSec       = $TimeoutSec
        Quelle           = $e.Quelle
        Felder           = $e.Felder
        Ausgabefelder    = $e.Ausgabefelder
    }
}

function New-JnptLauf {
    <#
    .SYNOPSIS
      Die Lauf-Kennung: JJJJ-MM-TTThhmm-<RECHNERNAME>.
    #>
    param()
    $rechner = $env:COMPUTERNAME
    if ([string]::IsNullOrWhiteSpace($rechner)) { $rechner = [System.Environment]::MachineName }
    return '{0}-{1}' -f (Get-Date -Format 'yyyy-MM-ddTHHmm'), $rechner
}

function Get-JnptArbeitsliste {
    <#
    .SYNOPSIS
      Vorpruefung auf haengende Saetze, danach die freigegebenen Saetze.
    .DESCRIPTION
      Steht schon ein Satz auf in_buchung, ist ein frueherer Lauf mittendrin
      gestorben: abbrechen und einen Menschen fragen - nicht aufraeumen. Der
      haengende Satz ist kein Schaden, sondern die Bremse.
      Liefert IMMER ein Array (5.1 packt eine einelementige Liste sonst aus).
    #>
    param(
        [Parameter(Mandatory)]$Session,
        [ValidateRange(1, 100)][int]$Limit = 100
    )
    $haenger = @(Find-JnptSaetze -Session $Session -Status $script:StatusGesperrt -Limit $script:JnptSuchDeckel)
    if ($haenger.Count -gt 0) {
        throw ('Abbruch: {0} Satz/Saetze stehen auf {1} - erst pruefen.' -f $haenger.Count, $script:StatusGesperrt)
    }
    return @(Find-JnptSaetze -Session $Session -Status $script:StatusFrei -Limit $Limit)
}

function Lock-JnptSatz {
    <#
    .SYNOPSIS
      Setzt einen freigegebenen Satz auf in_buchung - der Status IST die Sperre.
    .DESCRIPTION
      Liest den Satz frisch, verlangt status = geprueft und schreibt in_buchung
      mit der eigenen Lauf-Kennung. Liefert den GELESENEN Satz zurueck - aus
      ihm nimmt die Klickfolge im Fachsystem ihre Werte.
    #>
    param(
        [Parameter(Mandatory)]$Session,
        [Parameter(Mandatory)][string]$Id,
        [Parameter(Mandatory)][string]$Lauf
    )
    $satz = Get-JnptSatz -Session $Session -Id $Id
    $status = Get-JnptFeld -Satz $satz -Name 'status'
    if ($status -ne $script:StatusFrei) {
        throw ('Satz {0}: Status ist "{1}", erwartet "{2}" - zwischenzeitlich geaendert.' -f $Id, $status, $script:StatusFrei)
    }
    $aenderung = New-JnptAenderung -Session $Session -Status $script:StatusGesperrt -Lauf $Lauf
    Set-JnptSatz -Session $Session -Satz $satz -Aenderung $aenderung
    return $satz
}

function Set-JnptSatzErgebnis {
    <#
    .SYNOPSIS
      Schreibt die Buchungsnummer und setzt den Satz auf verbucht.
    .DESCRIPTION
      Nur fuer Saetze, die dieser Lauf selbst gesperrt hat, und nur, wenn das
      Buchungsnummer-Feld noch leer ist - nie doppelt buchen.
    #>
    param(
        [Parameter(Mandatory)]$Session,
        [Parameter(Mandatory)][string]$Id,
        [Parameter(Mandatory)][string]$Lauf,
        [Parameter(Mandatory)][string]$Buchungsnr
    )
    if ([string]::IsNullOrWhiteSpace($Buchungsnr)) {
        throw ('Satz {0}: leere Buchungsnummer - ohne Beleg wird nicht auf {1} gesetzt.' -f $Id, $script:StatusVerbucht)
    }
    $satz = Get-JnptSatz -Session $Session -Id $Id
    Assert-JnptEigenerLauf -Session $Session -Satz $satz -Id $Id -Lauf $Lauf
    $feld = $Session.Felder.Buchungsnr
    $alt = Get-JnptFeld -Satz $satz -Name $feld
    if (-not [string]::IsNullOrWhiteSpace($alt)) {
        throw ('Satz {0}: {1} ist schon gesetzt ("{2}") - nicht doppelt buchen.' -f $Id, $feld, $alt)
    }
    $aenderung = New-JnptAenderung -Session $Session -Status $script:StatusVerbucht -Lauf $Lauf
    $aenderung[$feld] = $Buchungsnr.Trim()
    Set-JnptSatz -Session $Session -Satz $satz -Aenderung $aenderung
    return [pscustomobject]@{ id = $Id; status = $script:StatusVerbucht }
}

function Set-JnptSatzFehler {
    <#
    .SYNOPSIS
      Setzt den Satz auf fehler und schreibt Schritt und Meldung hinein.
    .DESCRIPTION
      Im Fachsystem bleibt der Beleg unangetastet; ein Mensch entscheidet
      spaeter. Der Text traegt Schrittnamen und Oberflaechen-Meldung, KEINE
      personenbezogenen Daten - und wird auf eine Zeile gebracht und auf 200
      Zeichen gedeckelt, damit niemand ein Protokoll in die Kartei kippt.
    #>
    param(
        [Parameter(Mandatory)]$Session,
        [Parameter(Mandatory)][string]$Id,
        [Parameter(Mandatory)][string]$Lauf,
        [Parameter(Mandatory)][string]$Schritt,
        [Parameter(Mandatory)][string]$Meldung
    )
    $satz = Get-JnptSatz -Session $Session -Id $Id
    Assert-JnptEigenerLauf -Session $Session -Satz $satz -Id $Id -Lauf $Lauf
    $aenderung = New-JnptAenderung -Session $Session -Status $script:StatusFehler -Lauf $Lauf
    $aenderung[$Session.Felder.FehlerText] = Format-JnptFehlerText -Schritt $Schritt -Meldung $Meldung
    Set-JnptSatz -Session $Session -Satz $satz -Aenderung $aenderung
    return [pscustomobject]@{ id = $Id; status = $script:StatusFehler }
}

# ---------------------------------------------------------------------------
# Innere Helfer (nicht exportiert)
# ---------------------------------------------------------------------------

function Find-JnptSaetze {
    param([Parameter(Mandatory)]$Session, [Parameter(Mandatory)][string]$Status, [Parameter(Mandatory)][int]$Limit)
    return @(ConvertTo-JnptSaetze (Invoke-JnptTool -Endpoint $Session.Endpoint -Token $Session.Token `
                -TimeoutSec $Session.TimeoutSec -Tool ('{0}_search' -f $Session.Satzart) `
                -Arguments @{ status = $Status; limit = $Limit }))
}

function Get-JnptSatz {
    param([Parameter(Mandatory)]$Session, [Parameter(Mandatory)][string]$Id)
    $treffer = @(ConvertTo-JnptSaetze (Invoke-JnptTool -Endpoint $Session.Endpoint -Token $Session.Token `
                    -TimeoutSec $Session.TimeoutSec -Tool ('{0}_get' -f $Session.Satzart) `
                    -Arguments @{ id = $Id }))
    if ($treffer.Count -ne 1) {
        throw ('Satz {0}: nicht gefunden (get lieferte {1} Saetze).' -f $Id, $treffer.Count)
    }
    return $treffer[0]
}

function Set-JnptSatz {
    # Das einzige Schreiben im Modul: genau ein update, kein Retry.
    param([Parameter(Mandatory)]$Session, [Parameter(Mandatory)]$Satz, [hashtable]$Aenderung = @{})
    Invoke-JnptTool -Endpoint $Session.Endpoint -Token $Session.Token -TimeoutSec $Session.TimeoutSec `
        -Tool ('{0}_update' -f $Session.Satzart) `
        -Arguments (New-JnptUpdateArgs -Satz $Satz -Quelle $Session.Quelle -Aenderung $Aenderung) | Out-Null
}

function New-JnptAenderung {
    # Was jeder Schreibschritt setzt: Status, eigene Lauf-Kennung, Zeitpunkt.
    param([Parameter(Mandatory)]$Session, [Parameter(Mandatory)][string]$Status, [Parameter(Mandatory)][string]$Lauf)
    $a = @{ status = $Status }
    $a[$Session.Felder.Lauf] = $Lauf
    $a[$Session.Felder.BearbeitetAm] = (Get-Date -Format 'yyyy-MM-dd HH:mm')
    return $a
}

function Get-JnptFeld {
    param([Parameter(Mandatory)]$Satz, [Parameter(Mandatory)][string]$Name)
    $wert = $Satz.fields.$Name
    if ($null -eq $wert) { return '' }
    return [string]$wert
}

function Assert-JnptEigenerLauf {
    # Der Roboter schreibt nur Saetze fort, die er in diesem Lauf selbst gesperrt hat.
    param([Parameter(Mandatory)]$Session, [Parameter(Mandatory)]$Satz,
          [Parameter(Mandatory)][string]$Id, [Parameter(Mandatory)][string]$Lauf)
    $status = Get-JnptFeld -Satz $Satz -Name 'status'
    if ($status -ne $script:StatusGesperrt) {
        throw ('Satz {0}: Status ist "{1}", erwartet "{2}".' -f $Id, $status, $script:StatusGesperrt)
    }
    $feld = $Session.Felder.Lauf
    $satzLauf = Get-JnptFeld -Satz $Satz -Name $feld
    if ($satzLauf -ne $Lauf) {
        throw ('Satz {0}: {1} ist "{2}", dieser Lauf ist "{3}" - fremder Lauf, nicht anfassen.' -f $Id, $feld, $satzLauf, $Lauf)
    }
}

function Format-JnptFehlerText {
    param([Parameter(Mandatory)][string]$Schritt, [Parameter(Mandatory)][string]$Meldung)
    $text = '{0}: {1}' -f $Schritt, $Meldung
    $text = $text -replace '[\x00-\x1F\x7F]', ' '
    $text = ($text -replace '\s+', ' ').Trim()
    if ($text.Length -gt $script:JnptFehlerTextMax) {
        $text = $text.Substring(0, $script:JnptFehlerTextMax)
    }
    return $text
}

Export-ModuleMember -Function @(
    'Invoke-JnptRpc', 'Connect-Jnpt', 'Invoke-JnptTool',
    'Get-JnptTokenFromCredentialManager',
    'Get-JnptKarteiEinstellungen',
    'ConvertTo-JnptSaetze', 'New-JnptUpdateArgs', 'ConvertTo-JnptAusgabe',
    'Connect-JnptKartei', 'New-JnptLauf', 'Get-JnptArbeitsliste',
    'Lock-JnptSatz', 'Set-JnptSatzErgebnis', 'Set-JnptSatzFehler'
)
