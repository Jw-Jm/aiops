import datetime
import ssl
import threading
from http.server import BaseHTTPRequestHandler, ThreadingHTTPServer
import pytest
from cryptography import x509
from cryptography.hazmat.primitives import hashes, serialization
from cryptography.hazmat.primitives.asymmetric import rsa
from cryptography.x509.oid import NameOID
from investigator.workload_tls import clients

def test_wrong_workload_and_live_revocation_fail_before_http_headers(tmp_path):
    now=datetime.datetime.now(datetime.timezone.utc)
    key=rsa.generate_private_key(public_exponent=65537,key_size=2048)
    name=x509.Name([x509.NameAttribute(NameOID.COMMON_NAME,"SP06 owned test CA")])
    ca=x509.CertificateBuilder().subject_name(name).issuer_name(name).public_key(key.public_key()).serial_number(1).not_valid_before(now-datetime.timedelta(minutes=1)).not_valid_after(now+datetime.timedelta(hours=1)).add_extension(x509.BasicConstraints(ca=True,path_length=None),critical=True).sign(key,hashes.SHA256())
    (tmp_path/'ca.pem').write_bytes(ca.public_bytes(serialization.Encoding.PEM))
    calls=[]
    class Handler(BaseHTTPRequestHandler):
        def log_message(self,*args):pass
        def do_GET(self):
            calls.append(self.headers.get('Authorization'))
            self.send_response(200);self.send_header('Content-Length','2');self.end_headers();self.wfile.write(b'{}')
    def certificate(uri):
        return x509.CertificateBuilder().subject_name(x509.Name([x509.NameAttribute(NameOID.COMMON_NAME,"owned-leaf")])).issuer_name(name).public_key(key.public_key()).serial_number(2).not_valid_before(now-datetime.timedelta(minutes=1)).not_valid_after(now+datetime.timedelta(hours=1)).add_extension(x509.SubjectAlternativeName([x509.DNSName('ops-api.test.svc.cluster.local'),x509.UniformResourceIdentifier(uri)]),critical=False).sign(key,hashes.SHA256())
    def crl(revoked=False):
        builder=x509.CertificateRevocationListBuilder().issuer_name(name).last_update(now-datetime.timedelta(minutes=1)).next_update(now+datetime.timedelta(minutes=10))
        if revoked:builder=builder.add_revoked_certificate(x509.RevokedCertificateBuilder().serial_number(2).revocation_date(now).build())
        (tmp_path/'crl.pem').write_bytes(builder.sign(key,hashes.SHA256()).public_bytes(serialization.Encoding.PEM))
    (tmp_path/'key.pem').write_bytes(key.private_bytes(serialization.Encoding.PEM,serialization.PrivateFormat.PKCS8,serialization.NoEncryption()))
    config=dict(caFile=str(tmp_path/'ca.pem'),crlFile=str(tmp_path/'crl.pem'),certificateFile=str(tmp_path/'cert.pem'),privateKeyFile=str(tmp_path/'key.pem'),apiServerName='ops-api.test.svc.cluster.local')
    for uri,revoked,allowed in [('spiffe://ops.local/ns/test/sa/other',False,False),('spiffe://ops.local/ns/test/sa/ops-api',False,True),('spiffe://ops.local/ns/test/sa/ops-api',True,False)]:
        (tmp_path/'cert.pem').write_bytes(certificate(uri).public_bytes(serialization.Encoding.PEM));crl(revoked)
        server=ThreadingHTTPServer(('127.0.0.1',0),Handler)
        tls=ssl.SSLContext(ssl.PROTOCOL_TLS_SERVER);tls.load_cert_chain(config['certificateFile'],config['privateKeyFile']);server.socket=tls.wrap_socket(server.socket,server_side=True)
        thread=threading.Thread(target=server.serve_forever,daemon=True);thread.start()
        client,asynchronous=clients(config)
        try:
            if allowed:assert client.get('https://127.0.0.1:'+str(server.server_port),headers={'Authorization':'Bearer owned-context'}).status_code==200
            else:
                with pytest.raises(RuntimeError,match='WORKLOAD_IDENTITY_REJECTED'):client.get('https://127.0.0.1:'+str(server.server_port),headers={'Authorization':'Bearer must-not-leak'})
        finally:
            client.close();server.shutdown();server.server_close();thread.join()
    assert calls==['Bearer owned-context']
