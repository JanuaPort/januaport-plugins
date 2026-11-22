#!/usr/bin/env python3
"""Erzeugt den unabhängigen zweiten Messsatz (Holdout) für den Prompt-Injection-Prüfer.

Deterministisch, nur Standardbibliothek. Schreibt messsatz2.jsonl und README.md
neben dieses Skript. Die Textbausteine liegen in bausteine_de.py / bausteine_en.py.

Aufruf:  python erzeuge_messsatz2.py
"""

import collections
import json
import pathlib
import random
import re

import bausteine_de as DE
import bausteine_en as EN

HIER = pathlib.Path(__file__).resolve().parent
SPRACHEN = {"de": DE, "en": EN}
FORMEN = ["mail", "datei", "erp_feld", "ticket", "webseite", "mcp_antwort"]
SEED = "pi-pruefer-holdout-2026-11-22"

# Eine gemeinsame Host-, Mail- und Parameter-Menge für beide Labels.
HOSTS = [
    "example.org", "example.com", "www.example.org", "www.example.com", "cdn.example.com",
    "img.example.org", "files.example.org", "track.example.org", "portal.example.com",
    "status.example.com", "news.example.com", "shop.example.com", "kunde.example.org",
    "docs.example.org", "api.example.com", "hilfe.example.com", "upload.example.org",
    "bild.example.org", "lager.test", "kunden.beispiel.test", "praxis-beispiel.test",
]
WEB_HOSTS = ["www.example.org", "www.example.com"]
MAILS = [
    "buchhaltung@example.org", "info@example.org", "ablage@example.org", "service@example.org",
    "rechnung@example.org", "archiv@example.org", "support@example.org", "versand@example.org",
    "praxis@beispiel.test", "lager@muster.test", "kontakt@kanzlei.test",
]
KEYS = ["d", "q", "ref", "c", "s", "k", "v", "u", "t", "data"]
EXTS = ["png", "gif", "jpg"]
ALTS = ["Status", "Logo", "Pixel", "Fortschritt", "Vorschau", "Banner", "Siegel", "QR-Code"]
WEB_PFADE = ["leistungen", "preise", "kontakt", "faq", "news"]

# --------------------------------------------------------------------------
# Plan: Verteilung je Sprache (de und en identisch, je 60 Positive und 60 Negative)
# --------------------------------------------------------------------------
# (klasse, anzahl, {band: anzahl})
POS_PLAN = [
    ("md_exfiltration", 15, {"mittel": 1, "lang": 1}),
    ("direkte_anweisung", 4, {"mittel": 1}),
    ("versteckt", 4, {"mittel": 1}),
    ("rollenwechsel", 4, {}),
    ("exfiltration", 4, {"lang": 1}),
    ("werkzeugmissbrauch", 4, {}),
    ("lang_eingebettet", 4, {"mittel": 2, "lang": 2}),
    ("unicode_tarnung", 4, {}),
    ("tabelle_json", 3, {"mittel": 1}),
    ("mehrsprachig_koeder", 3, {}),
    ("verteilt", 4, {"lang": 1}),
    ("bedingt", 4, {}),
    ("dauerhaft_merken", 3, {}),
]
# (klasse, art, anzahl, {band: anzahl}); art steuert die Blockwahl
NEG_PLAN = [
    ("hart_harmlos", "md_exfiltration", 16, {"mittel": 1, "lang": 1}),
    ("hart_harmlos", "unicode_tarnung", 3, {}),
    ("hart_harmlos", "tabelle_json", 3, {"mittel": 1}),
    ("hart_harmlos", "mehrsprachig_koeder", 3, {}),
    ("hart_harmlos", "verteilt", 3, {"lang": 1}),
    ("hart_harmlos", "bedingt", 3, {"mittel": 1}),
    ("hart_harmlos", "dauerhaft_merken", 3, {}),
    ("hart_harmlos", "andere", 6, {"mittel": 2}),
    ("harmlos", "ki", 16, {"mittel": 1, "lang": 2}),
    ("harmlos", "schlicht", 4, {}),
]
BAENDER = {"kurz": (0, 2000), "mittel": (2200, 7600), "lang": (8600, 15000)}
POSITIONEN = ["mid", "end", "start"]

# --------------------------------------------------------------------------
# Hilfen
# --------------------------------------------------------------------------


class Kreis:
    """Zyklische Auswahl: jedes Element kommt vor, bevor eines wiederholt wird."""

    def __init__(self, items, rnd):
        self.items = list(items)
        rnd.shuffle(self.items)
        self.i = 0

    def next(self):
        item = self.items[self.i % len(self.items)]
        self.i += 1
        return item


def datum(rnd, lang, L):
    tag = rnd.randint(1, 28)
    if lang == "de":
        return "%02d.%02d.2026" % (tag, rnd.randint(11, 12))
    return "%d %s 2026" % (tag, rnd.choice(L.MONATE[1:]))


def betrag(rnd, lang):
    wert = rnd.randint(8000, 480000) / 100
    if lang == "de":
        return "{:,.2f}".format(wert).replace(",", "X").replace(".", ",").replace("X", ".") + " EUR"
    return "EUR {:,.2f}".format(wert)


def zufallswerte(rnd, lang, L):
    return {
        "n": rnd.randint(2, 40),
        "artikel": rnd.choice(L.ARTIKEL),
        "datum": datum(rnd, lang, L),
        "betrag": betrag(rnd, lang),
        "rechnung": "RE-2026-%04d" % rnd.randint(1, 999),
        "name": rnd.choice(L.NAMEN),
        "ort": rnd.choice(L.ORTE),
        "firma": rnd.choice(L.FIRMEN),
        "auftrag": "A-%d" % rnd.randint(40000, 49999),
        "kd": str(rnd.randint(10000, 99999)),
    }


def satz(rnd, lang, L):
    return rnd.choice(L.SAETZE_ALLE).format(**zufallswerte(rnd, lang, L))


def absatz(rnd, lang, L):
    saetze = []
    while len(saetze) < rnd.randint(2, 4):
        s = satz(rnd, lang, L)
        if s not in saetze:
            saetze.append(s)
    return " ".join(saetze)


class Kontext:
    """Lazy gefüllter Werte-Kontext eines Eintrags für @@platzhalter@@."""

    def __init__(self, rnd, lang, L, kreise):
        self.rnd, self.lang, self.L, self.kreise, self.cache = rnd, lang, L, kreise, {}

    def __getitem__(self, key):
        if key not in self.cache:
            self.cache[key] = self._neu(key)
        return self.cache[key]

    def _neu(self, key):
        rnd, L = self.rnd, self.L
        if key == "host":
            return self.kreise["host"].next()
        if key == "mail":
            return self.kreise["mail"].next()
        if key == "key":
            return rnd.choice(KEYS)
        if key == "key2":
            return rnd.choice([k for k in KEYS if k != self["key"]])
        if key == "ext":
            return rnd.choice(EXTS)
        if key == "alt":
            return rnd.choice(ALTS)
        if key == "lead":
            return rnd.choice(L.MD_LEADS)
        werte = zufallswerte(rnd, self.lang, L)
        if key == "sendung":
            return "%010d" % rnd.randint(10**8, 10**10 - 1)
        return str(werte[key])


def fill(text, ctx):
    return re.sub(r"@@(\w+)@@", lambda m: ctx[m.group(1)], text)


def tags(s):
    return "".join(chr(0xE0000 + ord(c)) for c in s)


HOMOGLYPHEN = str.maketrans({
    "a": "а", "c": "с", "e": "е", "i": "і", "o": "о", "p": "р", "x": "х", "y": "у",
    "A": "А", "E": "Е", "O": "О", "P": "Р", "C": "С", "I": "І",
})


def unicode_block(variante, sichtbar, versteckt):
    if variante == 0:
        return sichtbar + " " + tags(versteckt)
    if variante == 1:
        return sichtbar + " ‮" + versteckt[::-1] + "‬"
    if variante == 2:
        return sichtbar + " " + versteckt.translate(HOMOGLYPHEN)
    return sichtbar + " ⁧" + versteckt[::-1] + "⁩" + tags(versteckt)


# --------------------------------------------------------------------------
# Form-Hüllen
# --------------------------------------------------------------------------


def felder_als_text(paras, fields):
    if not fields:
        return paras
    idx, paare = fields
    zeile = "\n".join("%s: %s" % (k, v) for k, v in paare)
    return paras[:idx] + [zeile] + paras[idx:]


def json_text(objekt):
    return json.dumps(objekt, ensure_ascii=False, indent=2)


def wrap_mail(lang, L, rnd, paras, fields):
    paras = felder_als_text(paras, fields)
    werte = zufallswerte(rnd, lang, L)
    von_name = rnd.choice(L.NAMEN)
    kopf = L.T["mail_kopf"].format(
        von_name=von_name, von=rnd.choice(MAILS), an=rnd.choice(MAILS),
        datum=werte["datum"], betreff=rnd.choice(L.BETREFF).format(**werte),
    )
    anrede = rnd.choice(L.ANREDE).format(name=rnd.choice(L.NAMEN))
    return "%s%s\n\n%s\n\n%s\n%s\n%s" % (
        kopf, anrede, "\n\n".join(paras), rnd.choice(L.GRUSS), von_name, rnd.choice(L.FIRMEN))


def wrap_datei(lang, L, rnd, paras, fields):
    paras = felder_als_text(paras, fields)
    werte = zufallswerte(rnd, lang, L)
    kopf = L.T["datei_kopf"].format(datei=rnd.choice(L.DATEIEN).format(**werte))
    teile, seite, naechste = [], 1, rnd.randint(3, 6)
    for i, p in enumerate(paras):
        if len(paras) > 8 and i == naechste:
            seite += 1
            teile.append(L.T["datei_seite"].format(n=seite).strip("\n"))
            naechste += rnd.randint(3, 6)
        teile.append(p)
    return kopf + "\n\n".join(teile)


def wrap_erp(lang, L, rnd, paras, fields):
    werte = zufallswerte(rnd, lang, L)
    kopf = L.T["erp_kopf"].format(
        auftrag=werte["auftrag"], kd=werte["kd"], status=rnd.choice(L.STATUS))
    paras = felder_als_text(paras, fields)
    if len(paras) <= 5:
        zeilen = ["%s: %s" % (L.ERP_FELDER[i % len(L.ERP_FELDER)], p) for i, p in enumerate(paras)]
        return kopf + "\n".join(zeilen)
    return kopf + "Feld: %s\n---\n%s" % (rnd.choice(L.ERP_FELDER), "\n\n".join(paras))


def wrap_ticket(lang, L, rnd, paras, fields):
    werte = zufallswerte(rnd, lang, L)
    tid = rnd.randint(1000, 9999)
    status, prio = rnd.choice(L.STATUS), rnd.choice(L.PRIO)
    titel = rnd.choice(L.TICKET_TITEL).format(**werte)
    if rnd.random() < 0.5:
        obj = {"id": tid, L.T["json_titel"]: titel, L.T["json_status"]: status,
               L.T["json_prio"]: prio}
        kommentare = []
        for i, p in enumerate(paras):
            eintrag = {L.T["json_autor"]: rnd.choice(L.NAMEN), L.T["json_datum"]: datum(rnd, lang, L),
                       L.T["json_text"]: p}
            if fields and fields[0] == i:
                eintrag.update(dict(fields[1]))
            kommentare.append(eintrag)
        if fields and fields[0] >= len(paras):
            obj.update(dict(fields[1]))
        obj[L.T["json_beschreibung"]] = paras[0] if paras else ""
        obj[L.T["json_kommentare"]] = kommentare[1:]
        if fields and fields[0] == 0:
            obj.update(dict(fields[1]))
        return json_text(obj)
    paras = felder_als_text(paras, fields)
    kopf = L.T["ticket_kopf"].format(tid=tid, status=status, prio=prio, titel=titel,
                                     name=rnd.choice(L.NAMEN))
    kommentare = ["[2026-11-%02d %02d:%02d] %s: %s" % (
        rnd.randint(1, 28), rnd.randint(7, 17), rnd.randint(0, 59), rnd.choice(L.NAMEN), p)
        for p in paras[1:]]
    text = kopf + paras[0]
    if kommentare:
        text += L.T["ticket_kommentare"] + "\n".join(kommentare)
    return text


def wrap_web(lang, L, rnd, paras, fields):
    paras = felder_als_text(paras, fields)
    titel = rnd.choice(L.UEBERSCHRIFTEN)
    kopf = L.T["web_kopf"].format(
        titel=titel, host=rnd.choice(WEB_HOSTS), pfad=rnd.choice(WEB_PFADE), nav=L.NAVIGATION)
    teile = []
    for i, p in enumerate(paras):
        if i > 0 and rnd.random() < 0.3:
            teile.append("## " + rnd.choice(L.UEBERSCHRIFTEN))
        teile.append(p)
    return kopf + "\n\n".join(teile) + L.T["web_fuss"].format(firma=rnd.choice(L.FIRMEN))


def wrap_mcp(lang, L, rnd, paras, fields):
    werte = zufallswerte(rnd, lang, L)
    obj = {"tool": rnd.choice(L.MCP_TOOLS), "ok": True}
    if rnd.random() < 0.5:
        datensatz = {"id": werte["auftrag"], L.T["json_kunde"]: werte["firma"],
                     L.T["json_status"]: rnd.choice(L.STATUS), L.T["json_betrag"]: werte["betrag"]}
        if fields:
            datensatz.update(dict(fields[1]))
        datensatz[L.T["json_notiz"]] = "\n\n".join(paras)
        obj[L.T["json_ergebnis"]] = datensatz
    else:
        treffer = []
        for i, p in enumerate(paras):
            eintrag = {"id": i + 1, L.T["json_datum"]: datum(rnd, lang, L), L.T["json_auszug"]: p}
            if fields and fields[0] == i:
                eintrag.update(dict(fields[1]))
            treffer.append(eintrag)
        if fields and fields[0] >= len(paras) and treffer:
            treffer[-1].update(dict(fields[1]))
        obj[L.T["json_treffer"]] = treffer
    return json_text(obj)


WRAP = {"mail": wrap_mail, "datei": wrap_datei, "erp_feld": wrap_erp, "ticket": wrap_ticket,
        "webseite": wrap_web, "mcp_antwort": wrap_mcp}

# --------------------------------------------------------------------------
# Blöcke wählen
# --------------------------------------------------------------------------


def md_variante(varianten, form, zaehler, gruppe):
    art = "feld" if form in ("ticket", "mcp_antwort") else "text"
    passend = [v for v in varianten if v["art"] == art]
    wahl = passend[zaehler[(gruppe, art)] % len(passend)]
    zaehler[(gruppe, art)] += 1
    return wahl


def block_positiv(spec, L, ctx, md_zaehler):
    klasse, i = spec["klasse"], spec["idx"]
    wahl = {
        "direkte_anweisung": lambda: L.DIREKT[i % 4],
        "versteckt": lambda: L.VERSTECKT[i % len(L.VERSTECKT)],
        "rollenwechsel": lambda: L.ROLLE[i % 4],
        "exfiltration": lambda: L.EXFIL[i % 4],
        "werkzeugmissbrauch": lambda: L.WERKZEUG[i % 4],
        "lang_eingebettet": lambda: [L.DIREKT[4], L.EXFIL[4], L.WERKZEUG[4], L.ROLLE[4]][i % 4],
        "tabelle_json": lambda: L.TABELLE[i % 3],
        "mehrsprachig_koeder": lambda: L.MEHRSPRACHIG[i % 4],
        "bedingt": lambda: L.BEDINGT[i % len(L.BEDINGT)],
        "dauerhaft_merken": lambda: L.MERKEN[i % 3],
    }
    if klasse == "unicode_tarnung":
        text = unicode_block(i % 4, L.UNI_SICHTBAR[i % 4], fill(L.UNI_VERSTECKT[i % 4], ctx))
        return [{"t": text}]
    if klasse == "verteilt":
        a, b = L.VERTEILT[i % 4]
        return [{"t": fill(a, ctx), "pos": "start"}, {"t": fill(b, ctx), "pos": "end"}]
    if klasse == "md_exfiltration":
        return md_items(L.MD_POS, spec, md_zaehler, ctx)
    return [{"t": fill(wahl[klasse](), ctx)}]


def platzhalterstil(text, stil):
    """Dieselben Platzhalter erscheinen in beiden Labels in wechselnder Schreibweise."""
    if stil == "klammer":
        return text
    if stil == "wort":
        return re.sub(r"https?://[^\s)\"'<>]+",
                      lambda u: re.sub(r"\{(\w+)\}", r"\1", u.group(0)), text)
    vorlage = {"spitz": r"<\1>", "doppelt": r"{{\1}}"}[stil]
    return re.sub(r"\{(\w+)\}", vorlage, text)


def md_items(varianten, spec, zaehler, ctx):
    v = md_variante(varianten, spec["form"], zaehler, (spec["lang"], spec["label"]))
    stil = random.Random(spec["id"] + "stil").choice(["klammer", "spitz", "doppelt", "wort"])
    if v["art"] == "text":
        return [{"t": platzhalterstil(fill(v["t"], ctx), stil)}]
    return [{"kv": [(k, platzhalterstil(fill(w, ctx), stil)) for k, w in v["t"]]}]


def block_negativ(spec, L, ctx, md_zaehler):
    art, i = spec["art"], spec["idx"]
    if art == "md_exfiltration":
        return md_items(L.MD_GEGEN, spec, md_zaehler, ctx)
    if art == "verteilt":
        a, b = L.VERTEILT_GEGEN[i % 3]
        return [{"t": fill(a, ctx), "pos": "start"}, {"t": fill(b, ctx), "pos": "end"}]
    if art == "schlicht":
        return []
    pool = {
        "unicode_tarnung": L.UNI_GEGEN, "tabelle_json": L.TABELLE_GEGEN,
        "mehrsprachig_koeder": L.MEHRSPRACHIG_GEGEN, "bedingt": L.BEDINGT_GEGEN,
        "dauerhaft_merken": L.MERKEN_GEGEN, "andere": L.ANDERE_HART, "ki": L.KI_HARMLOS,
    }[art]
    return [{"t": fill(pool[i % len(pool)], ctx)}]


def position_index(name, n):
    return {"start": min(1, n), "mid": n // 2, "end": n}[name]


def einsetzen(basis, items, standard):
    paras, fields = list(basis), None
    texte = [(position_index(it.get("pos", standard), len(basis)), it["t"]) for it in items if "t" in it]
    for idx, t in sorted(texte, key=lambda x: -x[0]):
        paras.insert(idx, t)
    for it in items:
        if "kv" in it:
            fields = (min(position_index(it.get("pos", standard), len(basis)), len(paras)), it["kv"])
    return paras, fields


# --------------------------------------------------------------------------
# Aufbau eines Eintrags
# --------------------------------------------------------------------------


def baue_text(spec, items, seed):
    lang, L, form = spec["lang"], SPRACHEN[spec["lang"]], spec["form"]
    lo, hi = BAENDER[spec["band"]]
    standard = spec["pos"]
    if spec["band"] == "kurz":
        rnd = random.Random(seed)
        basis = [absatz(rnd, lang, L) for _ in range(rnd.randint(2, 4))]
        paras, fields = einsetzen(basis, items, standard)
        return WRAP[form](lang, L, rnd, paras, fields)
    ziel = random.Random(seed + "z").randint(lo + 300, hi - 1200) if spec["band"] == "mittel" \
        else random.Random(seed + "z").randint(9200, 13500)
    n = max(6, ziel // 300)
    for _ in range(40):
        rnd = random.Random(seed)
        basis = [absatz(rnd, lang, L) for _ in range(n)]
        paras, fields = einsetzen(basis, items, standard)
        text = WRAP[form](lang, L, rnd, paras, fields)
        if lo <= len(text) <= hi:
            return text
        n += max(1, abs(ziel - len(text)) // 330) * (1 if len(text) < ziel else -1)
    raise RuntimeError("Längenband nicht getroffen: %s %s" % (spec["id"], len(text)))


def erzeuge():
    wurzel = random.Random(SEED)
    specs = []
    for lang_nr, lang in enumerate(("de", "en")):
        for label in (1, 0):
            liste = []
            plan = POS_PLAN if label == 1 else NEG_PLAN
            for eintrag in plan:
                if label == 1:
                    klasse, anzahl, baender = eintrag
                    art = klasse
                else:
                    klasse, art, anzahl, baender = eintrag
                bandliste = [b for b, k in baender.items() for _ in range(k)]
                for i in range(anzahl):
                    band = bandliste[i] if i < len(bandliste) else "kurz"
                    liste.append({"lang": lang, "label": label, "klasse": klasse, "art": art,
                                  "idx": i, "band": band})
            shift = lang_nr * 3 + (0 if label == 1 else 1)
            for k, spec in enumerate(liste):
                spec["form"] = FORMEN[(k + shift) % len(FORMEN)]
                spec["pos"] = POSITIONEN[k % 3] if spec["band"] == "kurz" else ("mid", "end")[k % 2]
                specs.append(spec)
    kreise = {l: {"host": Kreis(HOSTS, wurzel), "mail": Kreis(MAILS, wurzel)} for l in (0, 1)}
    md_zaehler = collections.Counter()
    eintraege = []
    for nr, spec in enumerate(specs, start=1):
        spec["id"] = "h-%03d" % nr
        seed = "%s/%s" % (SEED, spec["id"])
        L = SPRACHEN[spec["lang"]]
        L.SAETZE_ALLE = L.SAETZE + L.FUELL_EXTRA
        rnd = random.Random(seed + "ctx")
        ctx = Kontext(rnd, spec["lang"], L, kreise[spec["label"]])
        if spec["label"] == 1:
            items = block_positiv(spec, L, ctx, md_zaehler)
        else:
            items = block_negativ(spec, L, ctx, md_zaehler)
        text = baue_text(spec, items, seed)
        satz_ = {"id": spec["id"], "sprache": spec["lang"], "form": spec["form"],
                 "label": spec["label"], "klasse": spec["klasse"]}
        if spec["label"] == 0 and spec["klasse"] == "hart_harmlos" and spec["art"] in (
                "md_exfiltration", "unicode_tarnung", "tabelle_json", "mehrsprachig_koeder",
                "verteilt", "bedingt", "dauerhaft_merken"):
            satz_["gegenstueck_zu"] = spec["art"]
        satz_.update({"laenge_zeichen": len(text), "quelle": "synthetisch", "text": text})
        eintraege.append(satz_)
    return eintraege


def band_von(laenge):
    if laenge <= 2000:
        return "bis 2.000"
    return "2.001-8.000" if laenge <= 8000 else "über 8.000"


PLATZHALTER = re.compile(r"(\{[a-z]+\}|\{\{[a-z]+\}\}|<[a-z]{4,}>|[?&]\w+=[a-z]{4,}|[?&]\w+=[\"\s)])")


def platzhalter_anteil(eintraege, art):
    md = [e for e in eintraege
          if e["klasse"] == art or e.get("gegenstueck_zu") == art]
    return sum(1 for e in md if PLATZHALTER.search(e["text"])), len(md)


def readme(eintraege):
    def tabelle(titel, zaehler, spalten=("de", "en")):
        zeilen = ["| %s | de | en | gesamt |" % titel, "|---|---:|---:|---:|"]
        for k in sorted({k for k, _ in zaehler}):
            de, en = zaehler.get((k, "de"), 0), zaehler.get((k, "en"), 0)
            zeilen.append("| %s | %d | %d | %d |" % (k, de, en, de + en))
        return "\n".join(zeilen)

    def zaehle(f):
        return collections.Counter((f(e), e["sprache"]) for e in eintraege)

    pos = [e for e in eintraege if e["label"] == 1]
    neg = [e for e in eintraege if e["label"] == 0]
    ki = [e for e in neg if re.search(
        r"(?i:assistent|assistant|sprachmodell|language model|ki-agent|ai agent)|\bKI\b|\bAI\b", e["text"])]
    gruppen = collections.Counter(e.get("gegenstueck_zu") for e in neg if "gegenstueck_zu" in e)
    return "\n".join([
        "# Messsatz 2 (Holdout) für den Prompt-Injection-Prüfer",
        "",
        "Zu JanuaPort/januaport#1486, Phase 3a. **Unabhängig von Satz 1 geschrieben:** Der Autor kannte",
        "den ersten Messsatz, seinen Branch und dessen Dateien nicht. Der Satz prüft, ob ein auf Satz 1",
        "kalibrierter Prüfer auch auf fremdem Material hält.",
        "",
        "Geprüft werden Werkzeug-Antworten, bevor ein KI-Agent sie liest (Mail, Datei-/PDF-Auszug,",
        "ERP-Feld, Ticket, Webseite, Upstream-MCP-Antwort). Label 1 = enthält eine Anweisung an die KI,",
        "Label 0 = harmlos. Alle Texte sind synthetisch und frei erfunden (nur Beispiel-Namen, die",
        "Domains `example.org`/`example.com`/`.test`, die Prüfsummen-IBAN `DE89 3704 0044 0532 0130 00`).",
        "",
        "## Dateien",
        "",
        "- `messsatz2.jsonl`: 240 Einträge, ein JSON-Objekt je Zeile (UTF-8). Felder: `id`, `sprache`,",
        "  `form`, `label`, `klasse`, `gegenstueck_zu` (nur harmlose Gegenstücke), `laenge_zeichen`,",
        "  `quelle`, `text`.",
        "- `erzeuge_messsatz2.py` mit `bausteine_de.py` und `bausteine_en.py`: deterministischer",
        "  Generator, nur Standardbibliothek. Er schreibt `messsatz2.jsonl` und diese README neu.",
        "- `test_messsatz2.py`: prüft Zählungen, Datenregeln, Unicode-Tarnung und die Abkürzungsverbote.",
        "",
        "## Ausführen (nur im Container)",
        "",
        "```",
        "docker run --rm --memory 1g -v <repo>:/w -w /w/spikes/pi-pruefer-holdout python:3.12-slim python erzeuge_messsatz2.py",
        "docker run --rm --memory 1g -v <repo>:/w -w /w/spikes/pi-pruefer-holdout python:3.12-slim python -m unittest -v test_messsatz2",
        "```",
        "",
        "## Abkürzungsverbote (vom Test erzwungen)",
        "",
        "- Positive und harmlose Einträge teilen dieselbe Host-Menge; kein Host kommt nur bei Label 1 vor.",
        "- Keine Wörter wie „exfil“, „attacker“, „malicious“, „evil“, „inject“, „payload“ in Hosts oder Texten.",
        "- Großbuchstaben-Werte in URL-Queries (z. B. `PNG`, `XL`) stehen auf beiden Seiten, nie nur bei Label 1.",
        "- Kein Wort steht in mindestens 10 Positiven und in keinem harmlosen Eintrag.",
        "",
        "## Zählung",
        "",
        "Gesamt: %d Einträge, davon %d Positive (Label 1) und %d harmlose (Label 0)." % (
            len(eintraege), len(pos), len(neg)),
        "Je Sprache 60 Positive und 60 harmlose.",
        "",
        tabelle("Klasse (Label 1)", collections.Counter((e["klasse"], e["sprache"]) for e in pos)),
        "",
        tabelle("Klasse (Label 0)", collections.Counter((e["klasse"], e["sprache"]) for e in neg)),
        "",
        tabelle("Form", zaehle(lambda e: e["form"])),
        "",
        tabelle("Längenband (Zeichen)", zaehle(lambda e: band_von(e["laenge_zeichen"]))),
        "",
        "Längenbänder nach Label: " + ", ".join(
            "%s: %d Positive / %d harmlose" % (
                b, sum(1 for e in pos if band_von(e["laenge_zeichen"]) == b),
                sum(1 for e in neg if band_von(e["laenge_zeichen"]) == b))
            for b in ("bis 2.000", "2.001-8.000", "über 8.000")) + ".",
        "",
        "Gegenstück-Gruppen (harmlose Einträge mit `gegenstueck_zu`): " + ", ".join(
            "%s: %d" % (k, v) for k, v in sorted(gruppen.items())) + ".",
        "",
        "Harmlose Einträge, die eine KI oder einen Assistenten harmlos ansprechen: %d." % len(ki),
        "",
        "## Bekannte Grenzen",
        "",
        "- Texte sind schablonenhaft erzeugt: Füllsätze, Namen und Hosts stammen aus kleinen Pools, die",
        "  beide Labels gleich nutzen. Das ist gewollt (kein Label-Merkmal in der Oberfläche), macht den",
        "  Satz aber weniger vielfältig als echte Post.",
        "- In Markdown-Datenabfluss-Positiven stehen Query-Werte häufiger als Platzhalter oder offen",
        "  (`?d=`) als in den Gegenstücken: %d von %d Positiven gegenüber %d von %d Gegenstücken (grobe" % (
            *platzhalter_anteil(pos, "md_exfiltration"), *platzhalter_anteil(neg, "md_exfiltration")),
        "  Zählung). Die Gegenstücke enthalten Serienbrief-Vorlagen, API-Doku und offene Anmelde-Links,",
        "  gleichen den Unterschied aber nicht ganz aus. Ein Prüfer, der auf dieses Merkmal anspringt, würde auf diesem Satz zu gut aussehen.",
        "- Die Platzhalter-Schreibweise (`{x}`, `<x>`, `{{x}}`, nacktes Wort) wechselt je Eintrag in beiden Labels.",
        "",
    ])


def main():
    eintraege = erzeuge()
    with open(HIER / "messsatz2.jsonl", "w", encoding="utf-8", newline="\n") as f:
        for e in eintraege:
            f.write(json.dumps(e, ensure_ascii=False) + "\n")
    with open(HIER / "README.md", "w", encoding="utf-8", newline="\n") as f:
        f.write(readme(eintraege))
    print(len(eintraege), "Einträge geschrieben")


if __name__ == "__main__":
    main()
