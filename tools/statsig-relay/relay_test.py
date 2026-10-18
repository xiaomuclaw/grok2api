import json
import os
import sys
import threading
import unittest
from http.server import BaseHTTPRequestHandler, ThreadingHTTPServer
from urllib.request import Request, urlopen

sys.path.insert(0, os.path.dirname(__file__))
import relay  # noqa: E402


class Fake(BaseHTTPRequestHandler):
    calls = []

    def log_message(self, *_args):
        pass

    def do_POST(self):
        length = int(self.headers.get("Content-Length", "0"))
        payload = json.loads(self.rfile.read(length))
        self.__class__.calls.append((self.path, self.headers, payload))
        if self.path == "/v1":
            result = {"status": "ok", "solution": {"userAgent": "test-agent", "cookies": [{"name": "cf_clearance", "value": "abc"}]}}
        else:
            result = {"x-statsig-id": "s" * 94}
        encoded = json.dumps(result).encode()
        self.send_response(200)
        self.send_header("Content-Length", str(len(encoded)))
        self.end_headers()
        self.wfile.write(encoded)


class RelayTests(unittest.TestCase):
    @classmethod
    def setUpClass(cls):
        cls.server = ThreadingHTTPServer(("127.0.0.1", 0), Fake)
        threading.Thread(target=cls.server.serve_forever, daemon=True).start()
        base = f"http://127.0.0.1:{cls.server.server_port}"
        relay.FLARESOLVERR_URL, relay.UPSTREAM_URL, relay._clearance = base + "/v1", base + "/sign", None

    @classmethod
    def tearDownClass(cls):
        cls.server.shutdown()

    def test_refresh_and_forward(self):
        value = relay.sign("POST", "/rest/test", "meta")
        self.assertEqual(len(value), 94)
        self.assertEqual(Fake.calls[0][0], "/v1")
        self.assertEqual(Fake.calls[1][1]["Cookie"], "cf_clearance=abc")
        self.assertEqual(Fake.calls[1][2]["environment"]["metaContent"], "meta")

if __name__ == "__main__":
    unittest.main()
