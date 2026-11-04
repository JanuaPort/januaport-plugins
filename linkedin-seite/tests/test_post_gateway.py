"""Stufe B1 (#1237) W2: the Beisteller's own MCP client against a local gateway fake (SEC Bed. 3)."""
import base64
import json
import unittest
from linkedin_seite.domain import ReportError
from linkedin_seite.gateway import GatewayClient
from support_post import (FakeGateway, GATEWAY_TOKEN, TOOL_GET, TOOL_FILE, PNG, config, record,
                          new_id)


class Gateway(unittest.TestCase):
    sse = False

    def setUp(self):
        self.gw = FakeGateway(sse=self.sse)
        self.addCleanup(self.gw.close)
        self.cfg = config(self.gw.port)
        self.client = GatewayClient(self.cfg.schreiben, GATEWAY_TOKEN)

    def code(self, fn, *args):
        with self.assertRaises(ReportError) as cm:
            fn(*args)
        return cm.exception.code

    def test_handshake_then_exactly_the_two_closed_tools(self):
        rid = new_id()
        self.gw.plan[rid] = record(rid, {'post_text': 'Hallo'})
        self.gw.files['a.png'] = PNG
        self.assertEqual(self.client.eintrag(rid)['fields'], {'post_text': 'Hallo'})
        medium = self.client.datei('a.png', 1024)
        self.assertEqual((bytes(medium.daten), medium.groesse, medium.art), (PNG, len(PNG), 'bild'))
        methods = [c[0] for c in self.gw.calls]
        self.assertEqual(methods[:2], ['initialize', 'notifications/initialized'])
        self.assertEqual(methods.count('initialize'), 1)
        self.assertEqual([(c[1], c[2]) for c in self.gw.tool_calls()],
                         [(TOOL_GET, {'id': rid}),
                          (TOOL_FILE, {'file': 'a.png'})])
        for call in self.gw.calls:
            self.assertEqual(call[3]['Authorization'], 'Bearer ' + GATEWAY_TOKEN)
            self.assertIn('text/event-stream', call[3]['Accept'])
        self.assertTrue(all(path == ('POST', '/mcp') for path in self.gw.paths))
        self.assertEqual(self.client.tools, frozenset({TOOL_GET, TOOL_FILE}))

    def test_any_other_tool_is_refused_before_http(self):
        for tool in ('redaktion_freigabe_search', 'redaktionsplan_add', 'redaktionsplan_update', 'redaktion_freigabe_add', 'redaktionsplan_search',
                     'redaktion_medien_read_file', 'redaktion_medien_write_file', 'team_token_create', 'catalog'):
            with self.subTest(tool=tool):
                self.assertEqual(self.code(self.client.call, tool, {}, 1024), 'gateway_werkzeug_nicht_erlaubt')
        self.assertEqual(self.gw.calls, [])

    def test_paths_rejected_before_any_call(self):
        for name in ('', '../a.png', '/a.png', 'x/a.png', 'x\\a.png', '..', 'C:\\a.png'):
            with self.subTest(name=name):
                self.assertEqual(self.code(self.client.datei, name, 1024), 'medium_name_ungueltig')
        self.assertEqual(self.gw.calls, [])

    def test_size_limit_before_reading_more_and_after_decoding(self):
        self.gw.files['gross.png'] = PNG + b'x' * 4000
        self.assertEqual(self.code(self.client.datei, 'gross.png', 1024), 'medium_zu_gross')
        self.gw.files['grenze.png'] = PNG + b'x' * (1024 - len(PNG))
        self.assertEqual(self.client.datei('grenze.png', 1024).groesse, 1024)
        self.gw.files['eins.png'] = PNG + b'x' * (1025 - len(PNG))
        self.assertEqual(self.code(self.client.datei, 'eins.png', 1024), 'medium_zu_gross')

    def test_file_answer_shape_fail_closed(self):
        self.gw.files['a.png'] = PNG
        hooks = {
            'wrong-name': lambda n, a, res: {**res, 'structuredContent': {**res['structuredContent'], 'name': 'b.png'}},
            'wrong-size': lambda n, a, res: {**res, 'structuredContent': {**res['structuredContent'], 'bytes': 3}},
            'no-blob': lambda n, a, res: {**res, 'content': res['content'][:1]},
            'two-blobs': lambda n, a, res: {**res, 'content': res['content'] + res['content'][1:]},
            'bad-base64': lambda n, a, res: {**res, 'content': [res['content'][0], {'type': 'resource', 'resource': {'blob': '***'}}]},
        }
        for kind, hook in hooks.items():
            self.gw.result_hook = lambda n, a, res, hook=hook: hook(n, a, res) if n == TOOL_FILE else res
            with self.subTest(kind=kind):
                self.assertEqual(self.code(self.client.datei, 'a.png', 1024), 'gateway_antwort_ungueltig')

    def test_errors_map_to_fixed_codes_without_gateway_text(self):
        rid = new_id()
        self.assertEqual(self.code(self.client.eintrag, rid), 'eintrag_nicht_gefunden')
        self.gw.tool_errors.add(TOOL_FILE)
        with self.assertRaises(ReportError) as cm:
            self.client.datei('a.png', 1024)
        self.assertEqual(cm.exception.code, 'gateway_werkzeug_fehler')
        self.assertNotIn('TOOLERRORCANARY', str(cm.exception))
        self.gw.status = 401
        self.assertEqual(self.code(GatewayClient(self.cfg.schreiben, GATEWAY_TOKEN).eintrag, rid), 'gateway_zugang_verweigert')
        self.gw.status = 500
        self.assertEqual(self.code(GatewayClient(self.cfg.schreiben, GATEWAY_TOKEN).eintrag, rid), 'gateway_abgewiesen')
        wrong = GatewayClient(config(self.gw.port).schreiben, GATEWAY_TOKEN.replace('a1b2', 'ffff'))
        self.gw.status = 200
        self.assertEqual(self.code(wrong.eintrag, rid), 'gateway_zugang_verweigert')

    def test_unknown_tool_on_the_gateway_is_a_refusal(self):
        self.gw.result_hook = None
        original = self.gw.tool
        self.gw.tool = lambda name, args: None
        self.assertEqual(self.code(self.client.eintrag, new_id()), 'gateway_abgewiesen')
        self.gw.tool = original

    def test_unreachable_gateway(self):
        gone = FakeGateway()
        gone.close()
        client = GatewayClient(config(gone.port).schreiben, GATEWAY_TOKEN)
        with self.assertRaises(ReportError) as cm:
            client.eintrag(new_id())
        self.assertEqual(cm.exception.code, 'gateway_nicht_erreichbar')


class GatewaySSE(Gateway):
    """The same contract when the gateway answers as text/event-stream."""
    sse = True


if __name__ == '__main__':
    unittest.main()
