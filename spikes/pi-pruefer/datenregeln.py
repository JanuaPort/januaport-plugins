"""Ausführbarer Datenvertrag des isolierten Spikes, kein Produktfilter."""

import json
import re
from collections import Counter
from pathlib import Path
from urllib.parse import urlsplit

FORMEN = {"mail", "datei", "erp_feld", "ticket", "webseite", "mcp_antwort"}
ANGRIFFE = {"direkte_anweisung", "versteckt", "rollenwechsel", "exfiltration",
            "werkzeugmissbrauch", "lang_eingebettet", "md_exfiltration", "unicode_tarnung",
            "tabelle_json", "mehrsprachig_koeder", "verteilt", "bedingt", "dauerhaft_merken"}
NEUE_KLASSEN = {"md_exfiltration": 10, "unicode_tarnung": 10, "tabelle_json": 10,
                "mehrsprachig_koeder": 10, "verteilt": 5, "bedingt": 5, "dauerhaft_merken": 5}
FELDER = {"id", "sprache", "form", "label", "klasse", "laenge_zeichen", "quelle", "text"}
# Jede Sperre hat einen Negativtest und eine einzeln abschaltbare Mutationsstelle.
REGELN = {
    "pflichtfelder": True,
    "id": True,
    "sprache": True,
    "form": True,
    "label": True,
    "klasse": True,
    "text": True,
    "laenge": True,
    "quelle": True,
    "iban": True,
    "links": True,
    "email": True,
    "umfang": True,
    "sprachbalance": True,
    "labelbalance": True,
    "formabdeckung": True,
    "hartanteil": True,
    "klassenabdeckung": True,
    "langklasse": True,
    "langanteil": True,
    "gegenstueck": True,
    "neuklassen_umfang": True,
    "gegenstueck_abdeckung": True,
    "gegenstueck_formen": True,
    "mittelanteil": True,
    "mittelabdeckung": True,
    "unicode_zeichen": True,
    "unicode_abdeckung": True,
    "unicode_roh": True,
    "md_hosts": True,
    "md_query": True,
    "ki_ansprache": True,
    "md_datei_form": True,
    "md_datei": True,
    "md_strukturiert": True,
    "md_vorlagen": True,
    "md_varianten": True,
    "md_badge": True,
    "md_eigene_daten": True,
}


def fordere(ok, regel):
    if not ok and REGELN[regel]:
        raise ValueError(regel)


def host_erlaubt(host):
    host = (host or "").lower().rstrip(".")
    return (host in {"example.org", "example.com"}
            or host.endswith((".example.org", ".example.com", ".test")))


def unicode_art(zeichen):
    punkt = ord(zeichen)
    if 0xe0000 <= punkt <= 0xe007f:
        return "tag"
    if 0x202a <= punkt <= 0x202e or 0x2066 <= punkt <= 0x2069:
        return "bidi"
    if 0x400 <= punkt <= 0x4ff:
        return "kyrillisch"
    if 0x370 <= punkt <= 0x3ff:
        return "griechisch"
    return None


def validiere(zeilen):
    ids = set()
    for z in zeilen:
        fordere(isinstance(z, dict) and FELDER <= set(z) <= FELDER | {"gegenstueck_zu"}, "pflichtfelder")
        fordere(isinstance(z["id"], str) and bool(z["id"]) and z["id"] not in ids, "id")
        ids.add(z["id"])
        fordere(z["sprache"] in {"de", "en"}, "sprache")
        fordere(z["form"] in FORMEN, "form")
        fordere(type(z["label"]) is int and z["label"] in {0, 1}, "label")
        fordere(z["klasse"] in (ANGRIFFE if z["label"] == 1 else {"harmlos", "hart_harmlos"}), "klasse")
        fordere("gegenstueck_zu" not in z or
                (z["label"] == 0 and z["klasse"] == "hart_harmlos" and z["gegenstueck_zu"] in NEUE_KLASSEN),
                "gegenstueck")
        fordere(isinstance(z["text"], str) and bool(z["text"].strip()), "text")
        fordere(type(z["laenge_zeichen"]) is int and z["laenge_zeichen"] == len(z["text"]), "laenge")
        # Dieser Satz ist absichtlich ausschließlich synthetisch; neue Quellen brauchen SEC-Sichtung.
        fordere(z["quelle"] == "synthetisch", "quelle")
        text = z["text"]
        fordere(z["klasse"] != "unicode_tarnung" or any(unicode_art(c) for c in text), "unicode_zeichen")
        # Exakt die Repo-Allowlist: optionale einzelne ASCII-Leerzeichen, auch kompakt.
        ohne_testiban = re.sub(r"\bDE89 ?3704 ?0044 ?0532 ?0130 ?00\b", "", text)
        fordere(not re.search(r"(?<![A-Za-z0-9])[A-Z]{2}\s*\d\s*\d(?:\s*[A-Za-z0-9]){11,30}",
                              ohne_testiban), "iban")
        for link in re.findall(r"(?:[A-Za-z][A-Za-z0-9+.-]*:)?//[^\s<>\"']+", text):
            try:
                ok = host_erlaubt(urlsplit(link).hostname)
            except ValueError:
                ok = False
            fordere(ok, "links")
        for domain in re.findall(r"[A-Za-z0-9._%+-]+@([A-Za-z0-9.-]+\.[A-Za-z]{2,})", text):
            fordere(domain == "example.org" or domain.endswith(".test"), "email")
        fordere(z["klasse"] != "lang_eingebettet" or len(text) > 8000, "langklasse")
    n = len(zeilen)
    fordere(n == 460, "umfang")
    fordere(sum(z["sprache"] == "de" for z in zeilen) * 2 == n, "sprachbalance")
    fordere(sum(z["label"] == 1 for z in zeilen) * 2 == n, "labelbalance")
    fordere(all(sum(z["form"] == f for z in zeilen) >= 25 for f in FORMEN), "formabdeckung")
    fordere(sum(z["klasse"] == "hart_harmlos" for z in zeilen) >= n / 2 * .4, "hartanteil")
    fordere({z["klasse"] for z in zeilen if z["label"] == 1} == ANGRIFFE, "klassenabdeckung")
    fordere(sum(len(z["text"]) > 8000 for z in zeilen) >= 30, "langanteil")
    mitte = [z for z in zeilen if 2001 <= len(z["text"]) <= 8000]
    fordere(len(mitte) >= 30, "mittelanteil")
    fordere({z["label"] for z in mitte} == {0, 1} and {z["form"] for z in mitte} == FORMEN,
            "mittelabdeckung")
    for klasse, anzahl in NEUE_KLASSEN.items():
        for sprache in ("de", "en"):
            angriffe = [z for z in zeilen if z["klasse"] == klasse and z["sprache"] == sprache]
            gegen = [z for z in zeilen if z.get("gegenstueck_zu") == klasse and z["sprache"] == sprache]
            fordere(len(angriffe) == anzahl, "neuklassen_umfang")
            fordere(len(gegen) == len(angriffe), "gegenstueck_abdeckung")
            fordere(Counter(z["form"] for z in gegen) == Counter(z["form"] for z in angriffe),
                    "gegenstueck_formen")
    for sprache in ("de", "en"):
        arten = {unicode_art(c) for z in zeilen if z["klasse"] == "unicode_tarnung"
                 and z["sprache"] == sprache for c in z["text"]}
        fordere({"tag", "bidi", "kyrillisch", "griechisch"} <= arten, "unicode_abdeckung")
    from md_regeln import pruefe_md
    pruefe_md(zeilen, fordere)


def lade(pfad):
    rohzeilen = Path(pfad).read_text(encoding="utf-8").splitlines()
    zeilen = [json.loads(line) for line in rohzeilen]
    validiere(zeilen)
    for z, roh in zip(zeilen, rohzeilen):
        if z["klasse"] == "unicode_tarnung":
            fordere(all(c in roh for c in set(z["text"]) if unicode_art(c)), "unicode_roh")
    return zeilen
