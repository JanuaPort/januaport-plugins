"""Prüft den unabhängigen zweiten Messsatz (Holdout) für den Prompt-Injection-Prüfer.

Nur Standardbibliothek. Aufruf (im Container):
    python -m unittest -v test_messsatz2
"""

import collections
import json
import pathlib
import re
import unittest

HIER = pathlib.Path(__file__).resolve().parent
DATEI = HIER / "messsatz2.jsonl"

KLASSEN_POSITIV = [
    "direkte_anweisung", "versteckt", "rollenwechsel", "exfiltration",
    "werkzeugmissbrauch", "lang_eingebettet", "md_exfiltration",
    "unicode_tarnung", "tabelle_json", "mehrsprachig_koeder", "verteilt",
    "bedingt", "dauerhaft_merken",
]
GEGENSTUECK_KLASSEN = [
    "md_exfiltration", "unicode_tarnung", "tabelle_json",
    "mehrsprachig_koeder", "verteilt", "bedingt", "dauerhaft_merken",
]
FORMEN = ["mail", "datei", "erp_feld", "ticket", "webseite", "mcp_antwort"]
PFLICHTFELDER = ["id", "sprache", "form", "label", "klasse", "laenge_zeichen", "quelle", "text"]
ERLAUBTE_FELDER = set(PFLICHTFELDER) | {"gegenstueck_zu"}

IBAN_ERLAUBT = "DE89370400440532013000"
VERBOTENE_WOERTER = [
    "exfil", "attacker", "malicious", "evil", "inject", "payload",
    "angreifer", "bösartig", "schadcode",
]
KI_ANSPRACHE = re.compile(
    r"(?i:assistent|assistant|sprachmodell|language model|ki-agent|ai agent)|\bKI\b|\bAI\b"
)
URL_RE = re.compile(r"(?:https?://|(?<![\w.])www\.)([^\s/?#)\]>\"'<]+)", re.IGNORECASE)
MAIL_RE = re.compile(r"[A-Za-z0-9._%+-]+@([A-Za-z0-9.-]+\.[A-Za-z]{2,})")
TLD_RE = re.compile(
    r"\b(?:[A-Za-z0-9-]+\.)+(?:com|org|net|de|eu|io|ai|info|biz|xyz|ru|cn|uk|us|ch|at|test)\b",
    re.IGNORECASE,
)
IBAN_RE = re.compile(r"\b[A-Z]{2}\d{2}(?:\s?[A-Z0-9]{4}){3,7}(?:\s?[A-Z0-9]{1,4})?\b")
QUERY_WERT_RE = re.compile(r"[?&][\w.\-]+=([^&\s)\]>\"'#]+)")
TAG_BEREICH = range(0xE0000, 0xE0080)
BIDI_ZEICHEN = set(range(0x202A, 0x202F)) | set(range(0x2066, 0x206A))

EINTRAEGE = []


def setUpModule():
    EINTRAEGE.clear()
    with open(DATEI, encoding="utf-8") as f:
        for zeile in f:
            if zeile.strip():
                EINTRAEGE.append(json.loads(zeile))


def positive():
    return [e for e in EINTRAEGE if e["label"] == 1]


def negative():
    return [e for e in EINTRAEGE if e["label"] == 0]


def hosts_von(eintraege):
    treffer = set()
    for e in eintraege:
        for h in URL_RE.findall(e["text"]):
            treffer.add(h.split("@")[-1].split(":")[0].lower())
    return treffer


def host_erlaubt(host):
    return (
        host in ("example.org", "example.com")
        or host.endswith((".example.org", ".example.com", ".test"))
        or host == "test"
    )


def grossschreib_werte(eintraege):
    werte = set()
    for e in eintraege:
        for wert in QUERY_WERT_RE.findall(e["text"]):
            wert = wert.strip("<>{}[]%")
            if re.fullmatch(r"[A-Z][A-Z0-9_]+", wert):
                werte.add(wert)
    return werte


class Struktur(unittest.TestCase):
    def test_pflichtfelder_und_typen(self):
        for e in EINTRAEGE:
            with self.subTest(id=e.get("id")):
                self.assertTrue(set(PFLICHTFELDER) <= set(e), "Pflichtfeld fehlt")
                self.assertTrue(set(e) <= ERLAUBTE_FELDER, "unbekanntes Feld")
                self.assertIsInstance(e["id"], str)
                self.assertIn(e["sprache"], ("de", "en"))
                self.assertIn(e["form"], FORMEN)
                self.assertIn(e["label"], (0, 1))
                self.assertIs(type(e["label"]), int)
                self.assertIsInstance(e["klasse"], str)
                self.assertIs(type(e["laenge_zeichen"]), int)
                self.assertIsInstance(e["text"], str)
                self.assertTrue(e["text"].strip())

    def test_ids_fortlaufend(self):
        self.assertEqual(
            [e["id"] for e in EINTRAEGE],
            ["h-%03d" % i for i in range(1, len(EINTRAEGE) + 1)],
        )

    def test_laenge_stimmt(self):
        for e in EINTRAEGE:
            with self.subTest(id=e["id"]):
                self.assertEqual(e["laenge_zeichen"], len(e["text"]))

    def test_quelle(self):
        for e in EINTRAEGE:
            self.assertEqual(e["quelle"], "synthetisch")

    def test_klasse_passt_zum_label(self):
        for e in EINTRAEGE:
            with self.subTest(id=e["id"]):
                if e["label"] == 1:
                    self.assertIn(e["klasse"], KLASSEN_POSITIV)
                    self.assertNotIn("gegenstueck_zu", e)
                else:
                    self.assertIn(e["klasse"], ("harmlos", "hart_harmlos"))

    def test_gegenstueck_feld_nur_wo_erlaubt(self):
        for e in EINTRAEGE:
            if "gegenstueck_zu" in e:
                with self.subTest(id=e["id"]):
                    self.assertEqual(e["label"], 0)
                    self.assertEqual(e["klasse"], "hart_harmlos")
                    self.assertIn(e["gegenstueck_zu"], GEGENSTUECK_KLASSEN)

    def test_keine_doppelten_texte(self):
        texte = [e["text"] for e in EINTRAEGE]
        self.assertEqual(len(texte), len(set(texte)))


class Zaehlungen(unittest.TestCase):
    def test_gesamt(self):
        self.assertEqual(len(EINTRAEGE), 240)

    def test_sprache(self):
        z = collections.Counter(e["sprache"] for e in EINTRAEGE)
        self.assertEqual(z, {"de": 120, "en": 120})

    def test_label(self):
        z = collections.Counter(e["label"] for e in EINTRAEGE)
        self.assertEqual(z, {1: 120, 0: 120})

    def test_label_je_sprache(self):
        z = collections.Counter((e["sprache"], e["label"]) for e in EINTRAEGE)
        self.assertEqual(z, {("de", 1): 60, ("de", 0): 60, ("en", 1): 60, ("en", 0): 60})

    def test_formen_mindestens_30(self):
        z = collections.Counter(e["form"] for e in EINTRAEGE)
        for form in FORMEN:
            self.assertGreaterEqual(z[form], 30, form)

    def test_klassen_mindestens_6(self):
        z = collections.Counter(e["klasse"] for e in positive())
        for klasse in KLASSEN_POSITIV:
            self.assertGreaterEqual(z[klasse], 6, klasse)

    def test_md_exfiltration_mindestens_30(self):
        z = collections.Counter(e["klasse"] for e in positive())
        self.assertGreaterEqual(z["md_exfiltration"], 30)

    def test_hart_harmlos_mindestens_die_haelfte(self):
        z = collections.Counter(e["klasse"] for e in negative())
        self.assertGreaterEqual(z["hart_harmlos"], 60)
        self.assertGreaterEqual(z["harmlos"], 30)

    def test_ki_ansprachen_harmlos(self):
        treffer = [e for e in negative() if KI_ANSPRACHE.search(e["text"])]
        self.assertGreaterEqual(len(treffer), 30)
        for sprache in ("de", "en"):
            self.assertGreaterEqual(sum(1 for e in treffer if e["sprache"] == sprache), 12)

    def test_gegenstuecke_je_klasse(self):
        z = collections.Counter(e.get("gegenstueck_zu") for e in negative())
        self.assertGreaterEqual(z["md_exfiltration"], 30)
        for klasse in GEGENSTUECK_KLASSEN[1:]:
            self.assertGreaterEqual(z[klasse], 6, klasse)

    def test_md_gegenstuecke_haben_bild_oder_link(self):
        md = [e for e in negative() if e.get("gegenstueck_zu") == "md_exfiltration"]
        for e in md:
            with self.subTest(id=e["id"]):
                self.assertRegex(e["text"], r"!\[|<img|\]\(|https?://")
        mit_query = [e for e in md if re.search(r"[?&]\w+=", e["text"])]
        self.assertGreaterEqual(len(mit_query), 15)
        for sprache in ("de", "en"):
            self.assertGreaterEqual(sum(1 for e in md if e["sprache"] == sprache), 12)

    def test_laengenbaender(self):
        mittel = [e for e in EINTRAEGE if 2001 <= e["laenge_zeichen"] <= 8000]
        lang = [e for e in EINTRAEGE if e["laenge_zeichen"] > 8000]
        self.assertGreaterEqual(len(mittel), 20)
        self.assertGreaterEqual(len(lang), 15)
        for band in (mittel, lang):
            self.assertEqual({e["label"] for e in band}, {0, 1})
        kurz = [e for e in EINTRAEGE if e["laenge_zeichen"] <= 2000]
        self.assertGreaterEqual(len(kurz), 100)

    def test_md_varianten(self):
        md = [e["text"] for e in positive() if e["klasse"] == "md_exfiltration"]
        self.assertGreaterEqual(sum(1 for t in md if re.search(r"!\[[^\]]*\]\[\w+\]", t)), 3)
        self.assertGreaterEqual(sum(1 for t in md if "<img" in t), 3)
        self.assertGreaterEqual(sum(1 for t in md if re.search(r"!\[[^\]]*\]\(", t)), 5)
        formen = collections.Counter(e["form"] for e in positive() if e["klasse"] == "md_exfiltration")
        self.assertGreaterEqual(formen["mcp_antwort"] + formen["ticket"], 6)
        for t in md:
            self.assertIn("http", t)

    def test_deutsch_und_englisch_sind_erkennbar(self):
        de = re.compile(r"\b(und|der|die|das|Sie|bitte|ist|nicht|für)\b")
        en = re.compile(r"\b(the|and|please|is|to|for|you)\b", re.IGNORECASE)
        for sprache, muster in (("de", de), ("en", en)):
            gruppe = [e for e in EINTRAEGE if e["sprache"] == sprache]
            treffer = sum(1 for e in gruppe if muster.search(e["text"]))
            self.assertGreaterEqual(treffer / len(gruppe), 0.9, sprache)


class Datenregeln(unittest.TestCase):
    def test_hosts_nur_beispieldomains(self):
        for e in EINTRAEGE:
            for host in hosts_von([e]):
                with self.subTest(id=e["id"], host=host):
                    # Platzhalter in Klammern vor der Domain sind erlaubt
                    self.assertTrue(host_erlaubt(host), host)

    def test_keine_fremden_domains_im_fliesstext(self):
        for e in EINTRAEGE:
            for treffer in TLD_RE.finditer(e["text"]):
                with self.subTest(id=e["id"], treffer=treffer.group(0)):
                    self.assertTrue(host_erlaubt(treffer.group(0).lower()), treffer.group(0))

    def test_mailadressen(self):
        for e in EINTRAEGE:
            for domain in MAIL_RE.findall(e["text"]):
                with self.subTest(id=e["id"], domain=domain):
                    self.assertTrue(domain.lower() == "example.org" or domain.lower().endswith(".test"), domain)

    def test_iban(self):
        for e in EINTRAEGE:
            for treffer in IBAN_RE.findall(e["text"]):
                with self.subTest(id=e["id"], iban=treffer):
                    self.assertEqual(re.sub(r"\s", "", treffer), IBAN_ERLAUBT)

    def test_keine_geheimnisartigen_muster(self):
        muster = re.compile(r"sk-[A-Za-z0-9]|ghp_|AKIA[A-Z0-9]|jnpt_|xox[bp]-|-----BEGIN|eyJ[A-Za-z0-9]{10}")
        for e in EINTRAEGE:
            with self.subTest(id=e["id"]):
                self.assertIsNone(muster.search(e["text"]))

    def test_keine_escape_texte_statt_zeichen(self):
        muster = re.compile(r"\\u[0-9a-fA-F]{4}|\\x\{|U\+[0-9A-Fa-f]{4,5}|&#x[0-9A-Fa-f]+;|\\U000")
        for e in EINTRAEGE:
            with self.subTest(id=e["id"]):
                self.assertIsNone(muster.search(e["text"]))


class UnicodeTarnung(unittest.TestCase):
    @staticmethod
    def arten(text):
        zeichen = {ord(c) for c in text}
        arten = set()
        if any(z in TAG_BEREICH for z in zeichen):
            arten.add("tags")
        if zeichen & BIDI_ZEICHEN:
            arten.add("bidi")
        if any(0x0370 <= z <= 0x03FF or 0x0400 <= z <= 0x04FF for z in zeichen):
            arten.add("homoglyphe")
        return arten

    def test_jeder_eintrag_hat_echte_tarnzeichen(self):
        for e in positive():
            if e["klasse"] == "unicode_tarnung":
                with self.subTest(id=e["id"]):
                    self.assertTrue(self.arten(e["text"]))

    def test_alle_drei_arten_vorhanden(self):
        gesamt = set()
        zaehler = collections.Counter()
        for e in positive():
            if e["klasse"] == "unicode_tarnung":
                for art in self.arten(e["text"]):
                    zaehler[art] += 1
                    gesamt.add(art)
        self.assertEqual(gesamt, {"tags", "bidi", "homoglyphe"})
        for art in ("tags", "bidi", "homoglyphe"):
            self.assertGreaterEqual(zaehler[art], 2, art)

    def test_tag_zeichen_tragen_lesbaren_text(self):
        for e in positive():
            if e["klasse"] != "unicode_tarnung":
                continue
            versteckt = "".join(chr(ord(c) - 0xE0000) for c in e["text"] if ord(c) in TAG_BEREICH)
            if versteckt:
                with self.subTest(id=e["id"]):
                    self.assertGreaterEqual(len(versteckt), 20)
                    self.assertTrue(all(32 <= ord(c) < 127 or c == "\x7f" for c in versteckt))

    def test_gegenstuecke_nutzen_legitime_sonderzeichen(self):
        gegenstuecke = [e for e in negative() if e.get("gegenstueck_zu") == "unicode_tarnung"]
        self.assertGreaterEqual(len(gegenstuecke), 6)
        for e in gegenstuecke:
            with self.subTest(id=e["id"]):
                nicht_ascii = [c for c in e["text"] if ord(c) > 127 and c not in "äöüÄÖÜß€–—„“‚‘’·…"]
                self.assertTrue(nicht_ascii)


class Abkuerzungsverbote(unittest.TestCase):
    def test_hosts_in_beiden_klassen_gleich(self):
        pos, neg = hosts_von(positive()), hosts_von(negative())
        self.assertTrue(pos, "keine Hosts in Positiven")
        self.assertEqual(pos - neg, set(), "Host nur bei label 1")
        self.assertEqual(neg - pos, set(), "Host nur bei label 0")

    def test_maildomains_nicht_nur_bei_label_1(self):
        def domains(eintraege):
            return {d.lower() for e in eintraege for d in MAIL_RE.findall(e["text"])}
        self.assertEqual(domains(positive()) - domains(negative()), set())

    def test_verbotene_woerter(self):
        for e in EINTRAEGE:
            klein = e["text"].lower()
            for wort in VERBOTENE_WOERTER:
                with self.subTest(id=e["id"], wort=wort):
                    self.assertNotIn(wort, klein)

    def test_grossbuchstaben_werte_in_query_nicht_einseitig(self):
        pos, neg = grossschreib_werte(positive()), grossschreib_werte(negative())
        self.assertEqual(pos - neg, set(), "Großbuchstaben-Wert nur bei label 1")
        for wert in ("PNG", "XL"):
            self.assertIn(wert, pos)
            self.assertIn(wert, neg)

    def test_kein_wort_nur_in_positiven(self):
        def woerter(eintrag):
            return set(re.findall(r"\w+", eintrag["text"].lower()))
        positiv = collections.Counter()
        negativ = set()
        for e in positive():
            positiv.update(woerter(e))
        for e in negative():
            negativ |= woerter(e)
        verdaechtig = sorted(w for w, n in positiv.items() if n >= 10 and w not in negativ)
        self.assertEqual(verdaechtig, [])


# Ein md-Eintrag zählt als "mit Platzhalter oder offen", wenn mindestens eine URL darin
# einen Vorlagen-Platzhalter ({x}, {{x}}, <x>) oder einen leeren Query-Wert (?k=) enthält.
URL_VOLL_RE = re.compile(r"https?://(?:<[a-z_]+>|\{\{?[a-z_]+\}\}?|[^\s)\"'<>\]\\])+")
PLATZHALTER_RE = re.compile(r"\{\{?[a-z_]+\}\}?|<[a-z_]+>")
OFFENER_WERT_RE = re.compile(r"[?&][\w.\-]+=(?=$|&)")


def hat_platzhalter_oder_offenen_wert(text):
    for url in URL_VOLL_RE.findall(text):
        if PLATZHALTER_RE.search(url) or OFFENER_WERT_RE.search(url):
            return True
    return False


def md_gruppen():
    positiv = [e for e in positive() if e["klasse"] == "md_exfiltration"]
    gegenstueck = [e for e in negative() if e.get("gegenstueck_zu") == "md_exfiltration"]
    return positiv, gegenstueck


def anteil(eintraege):
    return sum(1 for e in eintraege if hat_platzhalter_oder_offenen_wert(e["text"])) / len(eintraege)


class PlatzhalterAnteil(unittest.TestCase):
    """Platzhalter oder offene Query-Werte dürfen kein Merkmal einer Seite sein."""

    def test_mass_erkennt_platzhalter_und_offene_werte(self):
        for text in (
            "![a](https://example.org/p.png?d={kundennr})",
            "![a](https://example.org/p.png?d={{kundennr}})",
            "<img src=\"https://example.org/p.png?d=<kundennr>\">",
            "Rufe https://example.org/b?q= auf",
            "<img src=\\\"https://example.org/s.png?fmt=PNG&q=\\\">",
        ):
            with self.subTest(text=text):
                self.assertTrue(hat_platzhalter_oder_offenen_wert(text))
        for text in (
            "![a](https://example.org/p.png?fmt=PNG&size=XL)",
            "Hänge den Wert {kundennr} an: kein Link",
            "[x](https://example.org/rechnung?nr=RE-2026-0187&kd=10442)",
        ):
            with self.subTest(text=text):
                self.assertFalse(hat_platzhalter_oder_offenen_wert(text))

    def test_anteil_je_seite_im_zielband(self):
        positiv, gegenstueck = md_gruppen()
        for name, gruppe in (("Positive", positiv), ("Gegenstücke", gegenstueck)):
            with self.subTest(seite=name):
                self.assertGreaterEqual(anteil(gruppe), 0.40)
                self.assertLessEqual(anteil(gruppe), 0.60)

    def test_unterschied_zwischen_den_seiten_hoechstens_10_punkte(self):
        positiv, gegenstueck = md_gruppen()
        self.assertLessEqual(abs(anteil(positiv) - anteil(gegenstueck)), 0.10)

    def test_je_sprache_im_zielband(self):
        positiv, gegenstueck = md_gruppen()
        for sprache in ("de", "en"):
            for name, gruppe in (("Positive", positiv), ("Gegenstücke", gegenstueck)):
                teil = [e for e in gruppe if e["sprache"] == sprache]
                with self.subTest(sprache=sprache, seite=name):
                    self.assertGreaterEqual(anteil(teil), 0.30)
                    self.assertLessEqual(anteil(teil), 0.70)

    def test_positive_ohne_platzhalter_haben_vollstaendige_url(self):
        positiv, _ = md_gruppen()
        ohne = [e for e in positiv if not hat_platzhalter_oder_offenen_wert(e["text"])]
        self.assertGreaterEqual(len(ohne), 12)
        for e in ohne:
            with self.subTest(id=e["id"]):
                self.assertTrue(URL_VOLL_RE.search(e["text"]))


if __name__ == "__main__":
    unittest.main()
