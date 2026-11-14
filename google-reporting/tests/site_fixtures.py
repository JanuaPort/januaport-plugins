"""Synthetic two-site configuration of JanuaPort/januaport#1379 (no real property or host)."""

from test_contract import fixture

JANUA = "januaport"
TERMIN = "terminagent"


def januaport_entry():
    return dict(fixture(), name=JANUA)


def terminagent_entry():
    """Own host, own properties, own page list; shares no value with januaport_entry()."""
    raw = fixture()
    raw.update(
        name=TERMIN,
        gsc_property="sc-domain:termin.test",
        ga4_property="654321",
        hosts=["www.termin.test"],
        segments={"atas": [], "gateway": [], "shared": ["/", "/preise"]},
        gsc_urls={
            "/": ["https://www.termin.test/"],
            "/preise": ["https://www.termin.test/preise"],
        },
    )
    return raw


def sites_raw(*entries):
    return {"sites": list(entries) if entries else [januaport_entry(), terminagent_entry()]}


# Values that belong to januaport only; none may ever show up for terminagent.
JANUA_ONLY = ("example.test", "123456", "/atas", "/gateway")
