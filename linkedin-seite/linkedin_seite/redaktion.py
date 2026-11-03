"""Stufe B1 (#1237): pure contract of the write side. No IO, no LinkedIn, no gateway.

What the Beisteller posts is bound to what a human approved: the write tools take only the
Kartei-ID; text, poll and media come from the entry itself; the hash runs over the canonical
form of the posted fields and the BYTES of every file (SEC c-6575359082 Bed. 2). The approval
lives in a second Kartei the AI cannot write; only records written via SSO count.
"""
from dataclasses import dataclass
import hashlib
import json
import math
import re
from urllib.parse import urlsplit
from . import source_contract as s
from .domain import ReportError, strict_json

AKTIONEN = {'create_post': 'posten', 'edit_post': 'bearbeiten', 'delete_post': 'loeschen', 'create_comment': 'kommentar'}
VORSCHAU = 'vorschau'
ROUTES = frozenset(AKTIONEN) | {VORSCHAU}
AKTION_WERTE = frozenset(AKTIONEN.values())

MAX_MEDIUM_BYTES = 32 * 1024 * 1024      # = hard cap of the Ablage get_file (internal/fileconnector)
MIN_MEDIUM_BYTES = 1024
MAX_MEDIEN_GESAMT = 64 * 1024 * 1024    # all files of one entry together, RAM bound of one job
MAX_BODY = 1024
HASH_PREFIX = b'jnpt-linkedin-seite-freigabe/v1\n'
SSO = 'SSO'                              # cmd/jnpt/principal_teams.go principalViaSSO

KARTEI_ID = re.compile(r'[0-9a-f]{16}')  # internal/kartei/service.go newID: 16 hex
HASH = re.compile(r'[0-9a-f]{64}')
NAME = re.compile(r'[a-z0-9][a-z0-9_-]{0,62}')
DATEI = re.compile(r'[A-Za-z0-9][A-Za-z0-9._-]{0,119}')
HOST = r'[a-z0-9](?:[a-z0-9-]{0,61}[a-z0-9])?(?:\.[a-z0-9](?:[a-z0-9-]{0,61}[a-z0-9])?)*'
GATEWAY_URL = re.compile(r'(?P<scheme>http|https)://(?P<host>' + HOST + r'):(?P<port>[1-9][0-9]{0,4})/mcp')
POST_URN = re.compile(r'urn:li:(?:share|ugcPost):[0-9]{1,30}')
ASSET_URN = {s.IMAGE: re.compile(r'urn:li:image:[A-Za-z0-9_-]{1,100}'),
             s.DOCUMENT: re.compile(r'urn:li:document:[A-Za-z0-9_-]{1,100}'),
             s.VIDEO: re.compile(r'urn:li:video:[A-Za-z0-9_-]{1,100}')}
COMMENT_ID = re.compile(r'[0-9]{1,30}|urn:li:comment:\(urn:li:(?:activity|share|ugcPost):[0-9]{1,30},[0-9]{1,30}\)')
ARTEN = {'.png': 'bild', '.jpg': 'bild', '.jpeg': 'bild', '.gif': 'bild', '.pdf': 'dokument', '.mp4': 'video'}
PSEUDONYM = ('‹', '›')         # ‹label-id›: a privacy veil token must never leave the house


def fail(status, code):
    raise ReportError(status, code)


def sha256_hex(data):
    return hashlib.sha256(data).hexdigest()


# --- configuration --------------------------------------------------------------------------

def gateway_url(value):
    """Exactly <scheme>://<host>:<port>/mcp. Never /admin/mcp, never a view, no query/userinfo."""
    match = GATEWAY_URL.fullmatch(value) if isinstance(value, str) else None
    if not match or not 1 <= int(match.group('port')) <= 65535:
        raise ValueError('configuration_invalid')
    return value


def tool_name(name, tool):
    return name.replace('-', '_') + '_' + tool  # cmd/jnpt karteiQualifiedName / fileconnector QualifiedName


@dataclass(frozen=True)
class WriteConfig:
    gateway_url: str
    redaktionsplan: str
    freigabe: str
    medien_ablage: str
    max_medium_bytes: int

    @classmethod
    def from_dict(cls, raw):
        allowed = {'gateway_url', 'redaktionsplan', 'freigabe', 'medien_ablage', 'max_medium_bytes'}
        if not isinstance(raw, dict) or set(raw) - allowed or not {'gateway_url', 'medien_ablage'} <= set(raw):
            raise ValueError('configuration_invalid')
        names = [raw.get('redaktionsplan', 'redaktionsplan'), raw.get('freigabe', 'redaktion_freigabe'), raw['medien_ablage']]
        if any(not isinstance(n, str) or not NAME.fullmatch(n) for n in names):
            raise ValueError('configuration_invalid')
        if len({n.replace('-', '_') for n in names}) != 3:
            raise ValueError('configuration_invalid')
        size = raw.get('max_medium_bytes', MAX_MEDIUM_BYTES)
        if type(size) is not int or not MIN_MEDIUM_BYTES <= size <= MAX_MEDIUM_BYTES:
            raise ValueError('configuration_invalid')
        return cls(gateway_url(raw['gateway_url']), names[0], names[1], names[2], size)

    @property
    def tools(self):
        """The closed list of the W2 back channel (SEC Bed. 3): nothing else is ever called."""
        return {'eintrag': tool_name(self.redaktionsplan, 'get'), 'freigabe': tool_name(self.freigabe, 'search'),
                'datei': tool_name(self.medien_ablage, 'get_file')}


# --- requests -------------------------------------------------------------------------------

@dataclass(frozen=True)
class WriteRequest:
    aktion: str
    eintrag_id: str
    vorschau: bool = False


def eintrag_id(value):
    if not isinstance(value, str) or not KARTEI_ID.fullmatch(value):
        fail(400, 'invalid_input')
    return value


def parse_write(route, raw):
    """The write tools take ONLY the Kartei-ID: no text, no file, no URN as argument."""
    if route not in AKTIONEN:
        fail(404, 'route_not_found')
    if len(raw) > MAX_BODY:
        fail(400, 'request_too_large')
    try:
        body = strict_json(raw)
    except (ValueError, UnicodeError):
        fail(400, 'invalid_input')
    if not isinstance(body, dict) or set(body) != {'eintrag_id'}:
        fail(400, 'invalid_input')
    return WriteRequest(AKTIONEN[route], eintrag_id(body['eintrag_id']))


def parse_vorschau(pairs):
    args = {}
    for key, value in pairs:
        if key not in ('eintrag_id', 'aktion') or key in args:
            fail(400, 'invalid_input')
        args[key] = value
    if set(args) != {'eintrag_id', 'aktion'} or args['aktion'] not in AKTION_WERTE:
        fail(400, 'invalid_input')
    return WriteRequest(args['aktion'], eintrag_id(args['eintrag_id']), True)


# --- entry ----------------------------------------------------------------------------------

def check_datei(name):
    """A plain base name of the ONE media Ablage: no path, no '..', no absolute, known type."""
    if (not isinstance(name, str) or not DATEI.fullmatch(name) or '..' in name
            or medium_art(name, strict=False) is None):
        fail(422, 'medium_name_ungueltig')
    return name


def medium_art(name, strict=True):
    dot = name.rfind('.')
    art = ARTEN.get(name[dot:].lower()) if dot > 0 else None
    if art is None and strict:
        fail(422, 'medium_name_ungueltig')
    return art


@dataclass(frozen=True)
class Umfrage:
    frage: str
    antworten: tuple
    dauer: int


@dataclass(frozen=True)
class Entry:
    post_text: object
    medien: tuple
    umfrage: object
    post_urn: object
    kommentar_text: object


FIELDS = ('post_text', 'medien', 'umfrage', 'post_urn', 'kommentar_text')
RELEVANT = {'posten': ('post_text', 'medien', 'umfrage'), 'bearbeiten': ('post_urn', 'post_text'),
            'loeschen': ('post_urn',), 'kommentar': ('post_urn', 'kommentar_text')}


def parse_medien(raw):
    if raw is None:
        return ()
    try:
        value = strict_json(raw)
    except (ValueError, UnicodeError):
        fail(422, 'medien_ungueltig')
    if not isinstance(value, list) or len(value) > s.MULTI_IMAGE_MAX or any(not isinstance(v, str) for v in value):
        fail(422, 'medien_ungueltig')
    if len(set(value)) != len(value):
        fail(422, 'medien_ungueltig')
    return tuple(check_datei(v) for v in value)


def parse_umfrage(raw):
    if raw is None:
        return None
    try:
        value = strict_json(raw)
    except (ValueError, UnicodeError):
        fail(422, 'umfrage_ungueltig')
    if not isinstance(value, dict) or set(value) != {'frage', 'antworten', 'dauer'}:
        fail(422, 'umfrage_ungueltig')
    frage, antworten, dauer = value['frage'], value['antworten'], value['dauer']
    # LinkedIn limits checked before sending; nothing is shortened (SEC c-6575837332 (a)).
    if not isinstance(frage, str) or not 1 <= len(frage) <= s.POLL_QUESTION_MAX:
        fail(422, 'umfrage_ungueltig')
    if (not isinstance(antworten, list) or not s.POLL_OPTIONS_MIN <= len(antworten) <= s.POLL_OPTIONS_MAX
            or any(not isinstance(a, str) or not 1 <= len(a) <= s.POLL_OPTION_MAX for a in antworten)
            or len(set(antworten)) != len(antworten)):
        fail(422, 'umfrage_ungueltig')
    if type(dauer) is not int or dauer not in s.POLL_DURATIONS:
        fail(422, 'umfrage_ungueltig')
    return Umfrage(frage, tuple(antworten), dauer)


def entry_from_record(record, rid, aktion):
    """The Redaktionsplan record exactly as the gateway returned it; fail closed on any doubt."""
    if (not isinstance(record, dict) or record.get('id') != rid or not isinstance(record.get('fields'), dict)):
        fail(502, 'gateway_antwort_ungueltig')
    fields = record['fields']
    for name in FIELDS:
        if name in fields and not isinstance(fields[name], str):
            fail(422, 'eintrag_ungueltig')
    for name in RELEVANT[aktion]:
        if any(mark in fields.get(name, '') for mark in PSEUDONYM):
            fail(422, 'pseudonym_im_feld')
    get = lambda name: fields.get(name) or None  # the core never stores '' for optional fields
    entry = Entry(get('post_text'),
                  parse_medien(get('medien')) if aktion == 'posten' else (),
                  parse_umfrage(get('umfrage')) if aktion == 'posten' else None,
                  get('post_urn'), get('kommentar_text'))
    if aktion == 'posten':
        if entry.post_urn is not None:
            fail(409, 'bereits_veroeffentlicht')
        if entry.post_text is None:
            fail(422, 'post_text_fehlt')
        return entry
    if entry.post_urn is None:
        fail(422, 'post_urn_fehlt')
    if not POST_URN.fullmatch(entry.post_urn):
        fail(422, 'post_urn_ungueltig')
    if aktion == 'bearbeiten' and entry.post_text is None:
        fail(422, 'post_text_fehlt')
    if aktion == 'kommentar' and entry.kommentar_text is None:
        fail(422, 'kommentar_text_fehlt')
    if aktion == 'kommentar' and 'urn:li:' in entry.kommentar_text.lower():
        # Sent unescaped (little text is not documented for comments): no URN, hence no mention.
        fail(422, 'kommentar_erwaehnung')
    return entry


def check_format(entry, aktion):
    if aktion != 'posten':
        return aktion
    arten = [medium_art(name) for name in entry.medien]
    if entry.umfrage is not None:
        if arten:
            fail(422, 'umfrage_mit_medien')
        return 'umfrage'
    if not arten:
        return 'text'
    if all(a == 'bild' for a in arten):
        return 'bild' if len(arten) == 1 else 'bilder'
    if arten in (['dokument'], ['video']):
        return arten[0]
    fail(422, 'medien_kombination_ungueltig')


MAGIC = {'bild': (b'\x89PNG\r\n\x1a\n', b'\xff\xd8\xff', b'GIF87a', b'GIF89a'), 'dokument': (b'%PDF-',)}


def check_magic(art, data):
    """The bytes must be what the extension promises; the preview names the same kind."""
    head = bytes(data[:16])
    ok = head[4:8] == b'ftyp' if art == 'video' else head.startswith(MAGIC[art])
    if not ok:
        fail(422, 'medium_format_ungueltig')


# --- the approval hash (SEC Bed. 2) ---------------------------------------------------------

def canonical(aktion, entry, media):
    """Canonical JSON of exactly the posted-relevant fields; media as name, size and SHA-256 of the bytes."""
    if aktion == 'posten':
        umfrage = entry.umfrage
        obj = {'aktion': aktion, 'post_text': entry.post_text,
               'medien': [{'datei': name, 'bytes': size, 'sha256': digest} for name, size, digest in media],
               'umfrage': None if umfrage is None else {'frage': umfrage.frage, 'antworten': list(umfrage.antworten),
                                                        'dauer': umfrage.dauer}}
    elif aktion == 'bearbeiten':
        obj = {'aktion': aktion, 'post_urn': entry.post_urn, 'post_text': entry.post_text}
    elif aktion == 'loeschen':
        obj = {'aktion': aktion, 'post_urn': entry.post_urn}
    else:
        obj = {'aktion': aktion, 'post_urn': entry.post_urn, 'kommentar_text': entry.kommentar_text}
    return json.dumps(obj, ensure_ascii=False, sort_keys=True, separators=(',', ':')).encode('utf-8')


def freigabe_hash(aktion, entry, media):
    return hashlib.sha256(HASH_PREFIX + canonical(aktion, entry, media)).hexdigest()


def freigegeben(records, rid, aktion, digest):
    """Approved only by a record with the same id, action and hash, written AND last changed via SSO."""
    for rec in records if isinstance(records, list) else ():
        if not isinstance(rec, dict) or not isinstance(rec.get('fields'), dict):
            continue
        fields = rec['fields']
        if (fields.get('eintrag_id') == rid and fields.get('aktion') == aktion and fields.get('hash') == digest
                and rec.get('via') == SSO and rec.get('updated_via') == SSO):
            return True
    return False


# --- wire helpers ---------------------------------------------------------------------------

def little(text):
    """little text format: every reserved character escaped, so no mention can be formed.

    A '#' directly before a letter or digit and not inside a word stays a hashtag element.
    """
    out = []
    for i, char in enumerate(text):
        if char == '#' and i + 1 < len(text) and text[i + 1].isalnum() and (i == 0 or not text[i - 1].isalnum()):
            out.append(char)
        elif char in s.LITTLE_RESERVED:
            out.append('\\' + char)
        else:
            out.append(char)
    return ''.join(out)


def upload_ziel(url):
    """SEC V1: only https, exact host from the closed list, no port, no userinfo, fixed path prefix."""
    if not isinstance(url, str) or not 0 < len(url) <= 4096 or any(not 33 <= ord(c) <= 126 for c in url):
        fail(502, 'upload_ziel_abgewiesen')
    try:
        parts = urlsplit(url)
        host = parts.hostname
        parts.port  # a malformed port raises here
    except ValueError:
        fail(502, 'upload_ziel_abgewiesen')
    # netloc == hostname rules out userinfo ('@'), any port and a non-canonical host in one check.
    if (parts.scheme != 'https' or parts.netloc != host or host not in s.UPLOAD_HOSTS
            or parts.fragment or not parts.path.startswith(s.UPLOAD_PATH_PREFIX)):
        fail(502, 'upload_ziel_abgewiesen')
    return url


def video_teile(instructions, size):
    """SEC V2: the parts cover the file gap-free, without overlap, at most ceil(size / 4 MiB)."""
    if not isinstance(instructions, list) or not instructions or len(instructions) > math.ceil(size / s.VIDEO_PART_BYTES):
        fail(502, 'upload_teile_ungueltig')
    parts, expected = [], 0
    for item in instructions:
        if not isinstance(item, dict):
            fail(502, 'upload_teile_ungueltig')
        first, last = item.get(s.FIRST_BYTE), item.get(s.LAST_BYTE)
        if type(first) is not int or type(last) is not int or first != expected or not first <= last < size:
            fail(502, 'upload_teile_ungueltig')
        parts.append((upload_ziel(item.get(s.UPLOAD_URL)), first, last))
        expected = last + 1
    if expected != size:
        fail(502, 'upload_teile_ungueltig')
    return parts
