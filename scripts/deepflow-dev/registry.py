#!/usr/bin/env python3
"""Read-only task registry for kubelet's actual image credential verification.

Bound to loopback, only the explicitly prepared MySQL/ClickHouse repositories.
No synthetic kubelet pull records or global credential-policy changes.
"""
import argparse
import hashlib
from http.server import BaseHTTPRequestHandler, ThreadingHTTPServer
import json
from pathlib import Path
import re
import ssl


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument('--root', type=Path, required=True)
    parser.add_argument('--certificate', required=True)
    parser.add_argument('--private-key', required=True)
    args = parser.parse_args()
    class Handler(BaseHTTPRequestHandler):
        def log_message(self, *args):
            pass
        def do_HEAD(self):
            self.serve(False)
        def do_GET(self):
            self.serve(True)
        def serve(self, body):
            if self.path.rstrip('/') == '/v2':
                self.send_response(200)
                self.send_header('Docker-Distribution-API-Version', 'registry/2.0')
                self.send_header('Content-Length', '0')
                self.end_headers()
                return
            match = re.fullmatch(r'/v2/(mysql|clickhouse-server)/(manifests|blobs)/(sha256:[a-f0-9]{64}|assistant-20261013)', self.path)
            if not match:
                self.send_error(404)
                return
            name, kind, ref = match.groups()
            root = args.root / name
            file = root / ('manifest.json' if kind == 'manifests' else ref.removeprefix('sha256:'))
            if not file.is_file():
                self.send_error(404)
                return
            if kind == 'manifests':
                data = file.read_bytes()
                digest = 'sha256:' + hashlib.sha256(data).hexdigest()
                if ref.startswith('sha256:') and ref != digest:
                    self.send_error(404)
                    return
                mime = json.loads(data)['mediaType']
            else:
                digest = ref
                mime = 'application/octet-stream'
            self.send_response(200)
            self.send_header('Content-Type', mime)
            self.send_header('Docker-Content-Digest', digest)
            self.send_header('Content-Length', str(file.stat().st_size))
            self.end_headers()
            if body:
                with file.open('rb') as stream:
                    while chunk := stream.read(1024 * 1024):
                        self.wfile.write(chunk)
    server = ThreadingHTTPServer(('127.0.0.1', 18501), Handler)
    tls = ssl.SSLContext(ssl.PROTOCOL_TLS_SERVER)
    tls.minimum_version = ssl.TLSVersion.TLSv1_2
    tls.load_cert_chain(args.certificate, args.private_key)
    server.socket = tls.wrap_socket(server.socket, server_side=True)
    server.serve_forever()


if __name__ == '__main__':
    main()
