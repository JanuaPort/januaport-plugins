"""Stufe B3 (#1237, 0.4.0): the community adapter. Runs in its own spawned job process per call.

comments: unanswered top-level comments of third parties under ONE own post (at most 30 days old),
          newest first, plus answered ones edited after our reply. Text only here, never in a log.
reply:    one reply as the organization to one top-level comment. All checks live at LinkedIn, no store:
          own post, parent listed under exactly this post, top level, not ours, not deleted, no reply of
          ours yet (wiederholt), none to the same author under this post. Then the write request to the
          server process, the POST, the read back.
tagesliste: links only, built from checked IDs (T1); fixed notes; no API text field.

LinkedIn calls go only to source_contract.COMMUNITY_ENDPOINTS. Author/actor is always the configured
organization. No gateway, no editorial plan (SEC S1): this module never sees a gateway token.
"""
from datetime import datetime, timezone
import json
import urllib.request
from urllib.parse import quote
from . import kommentare as k
from . import source_contract as s
from .domain import ReportError
from .post_adapter import WriteTransport

HTTP_JE_LESEN = 25          # LinkedIn requests of one comments call; then it stops and hands out the cursor
HTTP_JE_TAGESLISTE = 40     # of one daily list; then unvollstaendig
HTTP_JE_ANTWORT = 12        # of one reply including POST and read back; checks stop two below
TAGESLISTE_POSTS = 20       # own posts read for the daily list (newest first)


def shape():
    raise ReportError(502, 'unexpected_source_shape')


class Budget(Exception):
    """This call's own HTTP ceiling is reached (not the daily cap)."""


class CommunityAdapter:
    def __init__(self, cfg, tokens, link, *, transport_factory=WriteTransport, now=None):
        self.cfg = cfg
        self.own = (cfg.kommentare.eigene_personen if cfg.kommentare is not None else frozenset())
        self.link = link
        self.transport = transport_factory(link.permit_http)
        self.tokens = tokens       # anything with access(): in a job the pipe to the server process
        self.now = now or (lambda: datetime.now(timezone.utc))
        self.calls = 0
        self.ceiling = None
        self.schreibversuch = False

    # --- dispatch ---------------------------------------------------------------------------

    def run(self, req):
        if isinstance(req, k.CommentsRequest):
            self.ceiling = HTTP_JE_LESEN
            return self.comments(req)
        if isinstance(req, k.TageslisteRequest):
            self.ceiling = HTTP_JE_TAGESLISTE
            return self.tagesliste(req)
        if isinstance(req, k.ReplyRequest):
            self.ceiling = HTTP_JE_ANTWORT
            try:
                return self.reply(req)
            except ReportError as exc:
                exc.nicht_ausgefuehrt = not self.schreibversuch
                raise
        raise ReportError(404, 'route_not_found')

    def von_uns(self, actor):
        return actor == self.cfg.organization or actor in self.own

    # --- LinkedIn ---------------------------------------------------------------------------

    def api(self, op, urn=None, params=None, body=None):
        if op not in s.COMMUNITY_ENDPOINTS:
            raise ReportError(404, 'route_not_found')
        method, template = s.COMMUNITY_ENDPOINTS[op]
        if '{urn}' in template:
            kind = s.COMMUNITY_URN[op]
            pattern = k.POST_URN if kind == 'post' else k.KOMMENTAR_URN
            if not isinstance(urn, str) or not pattern.fullmatch(urn):
                raise ReportError(400, 'invalid_input')
            path = template.format(urn=quote(urn, safe=''))
        elif urn is not None:
            raise ReportError(400, 'invalid_input')
        else:
            path = template
        if self.ceiling is not None and self.calls >= self.ceiling:
            raise Budget()
        self.calls += 1
        headers = {'Authorization': 'Bearer ' + self.tokens.access(), s.VERSION_HEADER: self.cfg.linkedin_version,
                   s.PROTOCOL_HEADER: s.PROTOCOL_VERSION}
        data = None
        if body is not None:
            headers['Content-Type'] = 'application/json'
            data = json.dumps(body, ensure_ascii=False, separators=(',', ':')).encode('utf-8')
        if params:
            path += '?' + '&'.join(quote(key, safe='') + '=' + quote(str(value), safe='') for key, value in params.items())
        request = urllib.request.Request(s.API_ROOT + path, data=data, headers=headers, method=method)
        # The POST answer is read for its x-restli-id header only (as B1 comment_create): an empty or
        # unexpected body must not turn a created reply into an unclear one.
        status, response_headers, answer = self.transport.exchange(
            request, expect_json=method == 'GET', before_send=self._schreiben if method != 'GET' else None)
        if status != (201 if op == 'reply_create' else 200):
            shape()
        return response_headers, answer

    def _schreiben(self):
        # After the HTTP permit, immediately before the POST: from here a failure is unclear, and the
        # server process starts the 10 minute lock for this comment and the 60 s spacing.
        self.link.write_request()
        self.schreibversuch = True

    def _listing(self, op, urn=None, params=None):
        _, answer = self.api(op, urn=urn, params=params)
        if not isinstance(answer, dict) or not isinstance(answer.get(s.ELEMENTS), list):
            shape()
        rows = answer[s.ELEMENTS]
        paging = answer.get(s.PAGING, {})
        if not isinstance(paging, dict) or not isinstance(paging.get(s.LINKS, []), list):
            shape()
        total = paging.get(s.TOTAL)
        more = any(isinstance(link, dict) and link.get(s.REL) == s.NEXT for link in paging.get(s.LINKS, []))
        if total is not None and (type(total) is not int or total < len(rows)):
            shape()
        complete = not more and (total is None or total == len(rows)) and len(rows) < s.LIST_COUNT
        return rows, complete

    def eigener_post(self, post_urn):
        """K2: the post is the organization's own, published, at most 30 days old; checked at LinkedIn."""
        _, post = self.api('post_get', urn=post_urn)
        if not isinstance(post, dict):
            shape()
        created = post.get(s.CREATED)
        if (post.get(s.ID) != post_urn or post.get(s.AUTHOR) != self.cfg.organization
                or post.get(s.LIFECYCLE) != s.PUBLISHED):
            raise ReportError(422, 'post_fremd')
        if type(created) is not int or created <= 0:
            shape()
        if created < self.now_ms() - k.POST_MAX_TAGE * k.DAY_MS:
            raise ReportError(422, 'post_zu_alt')
        return created

    def now_ms(self):
        return int(self.now().timestamp() * 1000)

    def kommentare(self, post_urn):
        """Verified top-level listing of one post: (comments, unreadable count, complete)."""
        rows, complete = self._listing('comments_list', post_urn, {'start': 0, 'count': s.LIST_COUNT})
        found, unreadable = [], 0
        for row in rows:
            item = k.kommentar_aus(row, post_urn)
            if item is None:
                unreadable += 1
            else:
                found.append(item)
        if len({item.activity for item in found}) > 1:
            shape()  # one post is one thread; two threads in one listing is not a known form
        return found, unreadable, complete

    def antworten(self, kommentar, post_urn=None):
        """Live replies of one comment: (replies, unreadable count, complete)."""
        rows, complete = self._listing('replies_list', kommentar.urn, {'start': 0, 'count': s.LIST_COUNT})
        found, unreadable = [], 0
        for row in rows:
            item = k.kommentar_aus(row, post_urn)
            if item is None or item.activity != kommentar.activity:
                unreadable += 1
            else:
                found.append(item)
        return found, unreadable, complete

    def unsere(self, replies):
        return [reply for reply in replies if self.von_uns(reply.actor) and not reply.geloescht]

    # --- comments ---------------------------------------------------------------------------

    def comments(self, req):
        self.eigener_post(req.post_urn)
        listing, unreadable, complete = self.kommentare(req.post_urn)
        candidates = sorted((c for c in listing if not c.eltern and not c.geloescht and not self.von_uns(c.actor)),
                            key=lambda c: c.schluessel, reverse=True)
        if req.weiter is not None:
            candidates = [c for c in candidates if c.schluessel < req.weiter]
        out, weiter, last = [], None, None
        for kommentar in candidates:
            if len(out) == req.limit:
                weiter = last
                break
            try:
                ours = [] if kommentar.antworten == 0 else self.unsere(self.antworten(kommentar, req.post_urn)[0])
            except Budget:
                weiter = last if last is not None else req.weiter
                break
            last = kommentar.schluessel
            edited = bool(ours) and kommentar.geaendert_ms > kommentar.zeit_ms and \
                kommentar.geaendert_ms > max(reply.zeit_ms for reply in ours)
            if not ours or edited:
                out.append(k.ausgabe(kommentar, bool(ours), edited))
        return {'post_urn': req.post_urn, 'hinweis': k.HINWEIS, 'kommentare': out,
                'weiter': None if weiter is None else '%d_%s' % weiter, 'nicht_lesbar': unreadable,
                'unvollstaendig': not complete}

    # --- reply ------------------------------------------------------------------------------

    def reply(self, req):
        k.pruefen(req)  # again, before the first request: plugin and core both enforce text and anlass
        teile = k.kommentar_teile(req.kommentar_urn)
        if not k.POST_URN.fullmatch(req.post_urn) or teile is None:
            raise ReportError(400, 'invalid_input')
        self.ceiling = HTTP_JE_ANTWORT - 2  # the POST and the read back stay possible
        try:
            parent = self._pruefen(req)
        except Budget:
            raise ReportError(429, 'pruefung_zu_umfangreich') from None
        self.ceiling = HTTP_JE_ANTWORT
        body = {s.ACTOR: self.cfg.organization, s.OBJECT: req.post_urn, s.PARENT_COMMENT: parent.urn,
                s.MESSAGE: {s.MESSAGE_TEXT: req.text}}
        headers, _ = self.api('reply_create', urn=parent.urn, body=body)
        rid = headers.get(s.RESTLI_ID)
        if not isinstance(rid, str) or not k.DIGITS.fullmatch(rid):
            shape()
        antwort = k.kommentar_urn(parent.activity, rid)
        result = {'ausgefuehrt': True, 'state': 'beantwortet', 'post_urn': req.post_urn, 'kommentar_urn': parent.urn,
                  'antwort_urn': antwort, 'anlass': req.anlass, 'doppelt': False, 'nachgelesen': False,
                  'link': k.link_antwort(parent.activity, parent.cid, rid)}
        try:  # read back: a confirmed POST stays a success even if this fails
            replies = self.unsere(self.antworten(parent, req.post_urn)[0])
            result['nachgelesen'] = any(reply.cid == rid for reply in replies)
            result['doppelt'] = len(replies) > 1
        except (ReportError, Budget):
            pass
        return result

    def _pruefen(self, req):
        self.eigener_post(req.post_urn)
        listing, unreadable, complete = self.kommentare(req.post_urn)
        if not complete:
            raise ReportError(422, 'zu_viele_kommentare')
        if unreadable:
            # The author of an unreadable comment is unknown: one reply per author cannot be checked.
            raise ReportError(422, 'kommentare_nicht_lesbar')
        parent = next((c for c in listing if c.urn == req.kommentar_urn), None)
        if parent is None:
            raise ReportError(422, 'kommentar_unbekannt')  # not a top-level comment of exactly this post
        if parent.eltern:
            raise ReportError(422, 'ebene')
        if parent.geloescht:
            raise ReportError(422, 'kommentar_geloescht')
        if self.von_uns(parent.actor):
            raise ReportError(422, 'von_uns')
        if parent.text is None:
            raise ReportError(422, 'erwaehnung_im_kommentar')  # comments withheld the text: no blind reply
        if self._beantwortet(parent, req.post_urn):
            raise ReportError(422, 'wiederholt')
        for sibling in listing:
            if sibling.urn != parent.urn and sibling.actor == parent.actor and self._beantwortet(sibling, req.post_urn):
                raise ReportError(422, 'verfasser_beantwortet')
        return parent

    def _beantwortet(self, kommentar, post_urn):
        """Live, never from the summary: a reply of ours exists. Unreadable or partial lists refuse."""
        replies, unreadable, complete = self.antworten(kommentar, post_urn)
        if unreadable or not complete:
            raise ReportError(422, 'antworten_nicht_lesbar')
        return bool(self.unsere(replies))

    # --- daily list -------------------------------------------------------------------------

    def tagesliste(self, req):
        lists = {key: [] for key in k.MERKMALE}
        state = {'nicht_lesbar': 0, 'unvollstaendig': False}
        now = self.now_ms()
        try:
            self._tagesliste(req, now, lists, state)
        except Budget:
            state['unvollstaendig'] = True
        return {'zeitraum': {'von': k.iso(req.start_ms), 'bis': k.iso(req.ende_ms)}, **lists, **state}

    def _tagesliste(self, req, now, lists, state):
        rows, complete = self._listing('posts_list', params={s.Q: s.AUTHOR, s.AUTHOR: self.cfg.organization,
                                                             s.COUNT: TAGESLISTE_POSTS, s.START: 0, s.SORT: s.CREATED_SORT})
        posts, oldest = [], None
        for row in rows:
            created = row.get(s.CREATED) if isinstance(row, dict) else None
            urn = row.get(s.ID) if isinstance(row, dict) else None
            if (not isinstance(urn, str) or not k.POST_URN.fullmatch(urn) or row.get(s.AUTHOR) != self.cfg.organization
                    or row.get(s.LIFECYCLE) != s.PUBLISHED or type(created) is not int or created <= 0):
                state['nicht_lesbar'] += 1
                continue
            oldest = created if oldest is None else min(oldest, created)
            if created >= req.rueckblick_ms:
                posts.append((created, urn))
        if not complete or (len(rows) >= TAGESLISTE_POSTS and oldest is not None and oldest >= req.rueckblick_ms):
            state['unvollstaendig'] = True  # the oldest post read is still in scope: there may be more
        for created, urn in sorted(posts, reverse=True):
            try:
                listing, unreadable, complete = self.kommentare(urn)
            except ReportError as exc:
                if exc.status != 502:
                    raise
                state['nicht_lesbar'] += 1  # one post LinkedIn answers oddly: counted, the list goes on
                state['unvollstaendig'] = True
                continue
            state['nicht_lesbar'] += unreadable
            state['unvollstaendig'] |= not complete
            open_post = created >= now - k.POST_MAX_TAGE * k.DAY_MS
            for kommentar in sorted(listing, key=lambda c: c.schluessel, reverse=True):
                if kommentar.eltern or kommentar.geloescht or self.von_uns(kommentar.actor):
                    continue
                if kommentar.antworten == 0:
                    ours = []
                else:
                    try:
                        replies, unreadable, complete = self.antworten(kommentar, urn)
                    except ReportError as exc:
                        if exc.status != 502:
                            raise
                        state['nicht_lesbar'] += 1
                        state['unvollstaendig'] = True
                        continue
                    state['nicht_lesbar'] += unreadable
                    state['unvollstaendig'] |= not complete
                    ours = self.unsere(replies)
                if not ours:
                    if open_post:
                        lists['offen'].append(k.link_kommentar(kommentar.activity, kommentar.cid))
                    continue
                for reply in sorted(ours, key=lambda r: r.schluessel, reverse=True):
                    if req.start_ms <= reply.zeit_ms < req.ende_ms:
                        lists['antworten'].append(k.link_antwort(kommentar.activity, kommentar.cid, reply.cid))
                if len(ours) > 1:
                    lists['doppelt'].append(k.link_kommentar(kommentar.activity, kommentar.cid))
                if kommentar.geaendert_ms > kommentar.zeit_ms and kommentar.geaendert_ms > max(r.zeit_ms for r in ours):
                    lists['nach_antwort_bearbeitet'].append(k.link_kommentar(kommentar.activity, kommentar.cid))
