#!/usr/bin/env python3
"""Task-owned HTTPS gateway for real static UI, API and real OIDC. No route mocks."""
import argparse
import http.client
from http.server import BaseHTTPRequestHandler, ThreadingHTTPServer
import json
from pathlib import Path
import ssl
import urllib.parse


def main():
    p=argparse.ArgumentParser(description=__doc__)
    p.add_argument('--base',type=Path,default=Path('/opt/ops-assistant-dev-20261012'))
    p.add_argument('--dist',type=Path,required=True)
    args=p.parse_args()
    public='https://192.168.0.63:18643'
    api_file=args.base/'runtime-private/api-address.json'
    class Handler(BaseHTTPRequestHandler):
        protocol_version='HTTP/1.1'
        def log_message(self,*values):pass
        def do_GET(self):self.handle_request()
        def do_POST(self):self.handle_request()
        def do_DELETE(self):self.handle_request()
        def do_PATCH(self):self.handle_request()
        def do_OPTIONS(self):self.handle_request()
        def handle_request(self):
            path=urllib.parse.urlsplit(self.path).path
            if path.startswith('/oidc/') or path.startswith('/api/'):
                if path.startswith('/oidc/'):
                    target=('127.0.0.1',18680)
                    upstream=self.path[len('/oidc'):]
                else:
                    if not api_file.exists():self.send_error(503);return
                    url=urllib.parse.urlsplit(json.loads(api_file.read_text())['address']);target=(url.hostname,url.port);upstream=self.path
                headers={k:v for k,v in self.headers.items() if k.lower() not in ('host','connection','accept-encoding','content-length','transfer-encoding')}
                headers.update({'Host':'192.168.0.63:18643','X-Forwarded-Proto':'https','X-Forwarded-Host':'192.168.0.63','X-Forwarded-Port':'18643'})
                length=int(self.headers.get('Content-Length','0'))
                if length>65536:self.send_error(413);return
                body=self.rfile.read(length) if length else None
                connection=http.client.HTTPConnection(*target,timeout=650)
                try:
                    connection.request(self.command,upstream,body=body,headers=headers)
                    response=connection.getresponse();self.send_response(response.status)
                    stream=response.getheader('Content-Type','').startswith('text/event-stream')
                    for key,value in response.getheaders():
                        if key.lower() not in ('connection','transfer-encoding','content-length'):
                            self.send_header(key,value)
                    if stream:self.send_header('Connection','close');self.close_connection=True
                    else:
                        data=response.read();self.send_header('Content-Length',str(len(data)))
                    self.end_headers()
                    if stream:
                        while chunk:=response.read1(8192):self.wfile.write(chunk);self.wfile.flush()
                    else:self.wfile.write(data)
                except (BrokenPipeError,ConnectionResetError):pass
                finally:connection.close()
                return
            if path=='/runtime-config.json':
                data=json.dumps({'oidc':{'authority':public+'/oidc/realms/ops-assistant-20261013','clientId':'ops-assistant-web'}}).encode();mime='application/json'
            else:
                candidate=(args.dist/path.lstrip('/')).resolve()
                if not candidate.is_relative_to(args.dist.resolve()):self.send_error(403);return
                if not candidate.is_file():candidate=args.dist/'index.html'
                data=candidate.read_bytes()
                import mimetypes
                mime=mimetypes.guess_type(str(candidate))[0] or 'application/octet-stream'
            self.send_response(200);self.send_header('Content-Type',mime);self.send_header('Cache-Control','no-store');self.send_header('Content-Length',str(len(data)));self.end_headers();self.wfile.write(data)
    server=ThreadingHTTPServer(('0.0.0.0',18643),Handler)
    context=ssl.SSLContext(ssl.PROTOCOL_TLS_SERVER);context.minimum_version=ssl.TLSVersion.TLSv1_2
    context.load_cert_chain('/data/ops-sp08-20261011/private/web.crt','/data/ops-sp08-20261011/private/web.key')
    server.socket=context.wrap_socket(server.socket,server_side=True)
    server.serve_forever()


if __name__=='__main__':main()
