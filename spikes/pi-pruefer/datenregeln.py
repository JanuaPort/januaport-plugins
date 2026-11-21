"""Ausführbarer Datenvertrag des isolierten Spikes, kein Produktfilter."""

import json
import re
from pathlib import Path
from urllib.parse import urlsplit

FORMEN = {"mail", "datei", "erp_feld", "ticket", "webseite", "mcp_antwort"}
ANGRIFFE = {"direkte_anweisung", "versteckt", "rollenwechsel", "exfiltration",
            "werkzeugmissbrauch", "lang_eingebettet"}
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
}


def fordere(ok, regel):
    if not ok and REGELN[regel]:
        raise ValueError(regel)


def host_erlaubt(host):
    host = (host or "").lower().rstrip(".")
    return (host in {"example.org", "example.com"}
            or host.endswith((".example.org", ".example.com", ".test")))


def validiere(zeilen):
    ids = set()
    for z in zeilen:
        fordere(isinstance(z, dict) and set(z) == FELDER, "pflichtfelder")
        fordere(isinstance(z["id"], str) and bool(z["id"]) and z["id"] not in ids, "id")
        ids.add(z["id"])
        fordere(z["sprache"] in {"de", "en"}, "sprache")
        fordere(z["form"] in FORMEN, "form")
        fordere(type(z["label"]) is int and z["label"] in {0, 1}, "label")
        fordere(z["klasse"] in (ANGRIFFE if z["label"] == 1 else {"harmlos", "hart_harmlos"}), "klasse")
        fordere(isinstance(z["text"], str) and bool(z["text"].strip()), "text")
        fordere(type(z["laenge_zeichen"]) is int and z["laenge_zeichen"] == len(z["text"]), "laenge")
        # Dieser Satz ist absichtlich ausschließlich synthetisch; neue Quellen brauchen SEC-Sichtung.
        fordere(z["quelle"] == "synthetisch", "quelle")
        text = z["text"]
        ohne_testiban = re.sub(r"\bDE02\s*1203\s*0000\s*0000\s*2020\s*51\b", "", text)
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
    fordere(n >= 240, "umfang")
    fordere(sum(z["sprache"] == "de" for z in zeilen) * 2 == n, "sprachbalance")
    fordere(sum(z["label"] == 1 for z in zeilen) * 2 == n, "labelbalance")
    fordere(all(sum(z["form"] == f for z in zeilen) >= 25 for f in FORMEN), "formabdeckung")
    fordere(sum(z["klasse"] == "hart_harmlos" for z in zeilen) >= n / 2 * .4, "hartanteil")
    fordere({z["klasse"] for z in zeilen if z["label"] == 1} == ANGRIFFE, "klassenabdeckung")
    fordere(sum(len(z["text"]) > 8000 for z in zeilen) >= 30, "langanteil")


def lade(pfad):
    zeilen = [json.loads(line) for line in Path(pfad).read_text(encoding="utf-8").splitlines()]
    validiere(zeilen)
    return zeilen
