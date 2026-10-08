"""OpenBao workload CSR lifecycle in the existing investigator process."""
import os
import re
import ssl
import threading
import time
from datetime import datetime, timedelta, timezone
from pathlib import Path
from urllib.parse import urlsplit

import httpx
from cryptography import x509
from cryptography.hazmat.primitives import hashes, serialization
from cryptography.hazmat.primitives.asymmetric import ec
from cryptography.x509.oid import ExtendedKeyUsageOID

from .workload_tls import PeerVerifier


class WorkloadIdentity:
    def __init__(self, config):
        if not hasattr(os, "memfd_create"):
            raise RuntimeError("WORKLOAD_PKI_REQUIRES_LINUX_MEMORY_FILES")
        self.namespace=config["apiServerName"][len("ops-api."):-len(".svc.cluster.local")]
        if not re.fullmatch(r"[a-z0-9](?:[-a-z0-9]{0,61}[a-z0-9])?", self.namespace):
            raise RuntimeError("WORKLOAD_NAMESPACE_INVALID")
        self.identity="spiffe://ops.local/ns/"+self.namespace+"/sa/ops-investigator"
        self.dns="ops-investigator."+self.namespace+".svc.cluster.local"
        self.ca=Path(config["caFile"]).read_bytes()
        self.authorities=x509.load_pem_x509_certificates(self.ca)
        if not self.authorities:
            raise RuntimeError("PINNED_WORKLOAD_CA_UNAVAILABLE")
        self.token_file=Path(os.environ["OPENBAO_PROJECTED_TOKEN_FILE"])
        address=os.environ["OPENBAO_ADDR"]
        parsed=urlsplit(address)
        if parsed.scheme!="https" or not parsed.hostname or parsed.username or parsed.password or parsed.query or parsed.fragment or parsed.path not in ("", "/"):
            raise RuntimeError("OPENBAO_WORKLOAD_ROUTE_INVALID")
        tls=ssl.create_default_context(cafile=os.environ["OPENBAO_CA_FILE"])
        tls.minimum_version=ssl.TLSVersion.TLSv1_3
        name=os.environ.get("OPENBAO_SERVER_NAME", parsed.hostname)
        def prepare(request):
            request.extensions["sni_hostname"]=name
        self.client=httpx.Client(base_url=address,verify=tls,timeout=5,follow_redirects=False,trust_env=False,event_hooks={"request":[prepare]})
        self.lock=threading.RLock()
        self.stop_event=threading.Event()
        self.state=None
        self.crl=None
        self.fetched=0
        try:
            self.refresh()
        except Exception:
            self.client.close()
            raise RuntimeError("WORKLOAD_PKI_INITIALIZATION_FAILED") from None
        self.thread=threading.Thread(target=self._run,name="workload-pki",daemon=True)
        self.thread.start()

    def _request(self, method, path, body=None, token=None):
        headers={"X-Vault-Token":token} if token else {}
        with self.client.stream(method,path,json=body,headers=headers) as response:
            if response.status_code!=200:
                raise RuntimeError("OPENBAO_WORKLOAD_REQUEST_REJECTED")
            raw=bytearray()
            for block in response.iter_bytes():
                raw.extend(block)
                if len(raw)>1<<20:
                    raise RuntimeError("OPENBAO_WORKLOAD_RESPONSE_OVERSIZED")
        return bytes(raw)

    def _read_crl(self):
        crl=x509.load_pem_x509_crl(self._request("GET","/v1/pki/crl/pem"))
        now=datetime.now(timezone.utc)
        issuer=next((ca for ca in self.authorities if ca.subject==crl.issuer and ca.not_valid_before_utc<=now<ca.not_valid_after_utc and crl.is_signature_valid(ca.public_key())),None)
        if issuer is None or not(crl.last_update_utc<=now<crl.next_update_utc):
            raise RuntimeError("WORKLOAD_CRL_UNAVAILABLE")
        return crl,issuer

    def refresh(self):
        import json
        # Only a public CSR is transmitted; the private key stays in memory.
        with self.token_file.open("rb") as f:
            jwt=f.read((1<<20)+1).decode().strip()
        if not jwt or len(jwt)>1<<20:
            raise RuntimeError("WORKLOAD_PROJECTED_TOKEN_UNAVAILABLE")
        login=json.loads(self._request("POST","/v1/auth/kubernetes/login",{"role":"ops-investigator-workload","jwt":jwt}))["auth"]
        if not login["client_token"] or not 0<login["lease_duration"]<=3600:
            raise RuntimeError("WORKLOAD_LOGIN_RESPONSE_INVALID")
        key=ec.generate_private_key(ec.SECP256R1())
        csr=x509.CertificateSigningRequestBuilder().subject_name(x509.Name([])).add_extension(x509.SubjectAlternativeName([x509.DNSName(self.dns),x509.UniformResourceIdentifier(self.identity)]),critical=False).sign(key,hashes.SHA256())
        data=json.loads(self._request("POST","/v1/pki/sign/platform-workload-ops-investigator",{"csr":csr.public_bytes(serialization.Encoding.PEM).decode(),"ttl":"1h","use_csr_values":True},login["client_token"]))["data"]
        cert_pem=data["certificate"].encode()
        cert=x509.load_pem_x509_certificate(cert_pem)
        crl,issuer=self._read_crl()
        verifier=PeerVerifier("unused","unused",lambda:(crl,issuer))
        verifier.verify(cert.public_bytes(serialization.Encoding.DER),self.identity)
        now=datetime.now(timezone.utc)
        usages=cert.extensions.get_extension_for_class(x509.ExtendedKeyUsage).value
        if cert.public_key().public_numbers()!=key.public_key().public_numbers() or cert.not_valid_after_utc-now>timedelta(hours=1,seconds=30) or not all(usage in usages for usage in (ExtendedKeyUsageOID.CLIENT_AUTH,ExtendedKeyUsageOID.SERVER_AUTH)):
            raise RuntimeError("WORKLOAD_ISSUED_CERTIFICATE_INVALID")
        key_pem=key.private_bytes(serialization.Encoding.PEM,serialization.PrivateFormat.PKCS8,serialization.NoEncryption())
        # Build both contexts before publishing an immutable certificate state.
        client=self._context(cert_pem,key_pem,False)
        server=self._context(cert_pem,key_pem,True)
        with self.lock:
            self.state=(cert,client,server)
            self.crl=(crl,issuer)
            self.fetched=time.monotonic()

    def _context(self, certificate, key, server):
        context=ssl.create_default_context(ssl.Purpose.CLIENT_AUTH if server else ssl.Purpose.SERVER_AUTH,cadata=self.ca.decode())
        context.minimum_version=ssl.TLSVersion.TLSv1_3
        if server:
            context.verify_mode=ssl.CERT_REQUIRED
        cert_fd=os.memfd_create("workload-certificate",os.MFD_CLOEXEC)
        key_fd=os.memfd_create("workload-key",os.MFD_CLOEXEC)
        try:
            os.fchmod(cert_fd,0o600);os.fchmod(key_fd,0o600)
            os.write(cert_fd,certificate);os.write(key_fd,key)
            context.load_cert_chain("/proc/self/fd/"+str(cert_fd),"/proc/self/fd/"+str(key_fd))
        finally:
            os.close(cert_fd);os.close(key_fd)
        return context

    def current_trust(self):
        with self.lock:
            now=datetime.now(timezone.utc)
            if self.crl is None or time.monotonic()-self.fetched>300 or not self.crl[0].last_update_utc<=now<self.crl[0].next_update_utc:
                raise RuntimeError("WORKLOAD_CRL_UNAVAILABLE")
            return self.crl

    def _current_context(self,index):
        with self.lock:
            crl,_=self.current_trust()
            cert=self.state[0]
            if datetime.now(timezone.utc)>=cert.not_valid_after_utc or crl.get_revoked_certificate_by_serial_number(cert.serial_number) is not None:
                raise RuntimeError("WORKLOAD_IDENTITY_REJECTED")
            return self.state[index]

    def client_context(self):
        return self._current_context(1)

    def server_context(self):
        context=self._current_context(2)
        def current(connection,name,initial):
            connection.context=self._current_context(2)
        context.sni_callback=current
        return context

    def _run(self):
        while not self.stop_event.wait(60):
            with self.lock:
                cert=self.state[0]
                renewal=cert.not_valid_before_utc+(cert.not_valid_after_utc-cert.not_valid_before_utc)*2/3
            try:
                if datetime.now(timezone.utc)>=renewal:
                    self.refresh()
                else:
                    crl=self._read_crl()
                    with self.lock:
                        self.crl=crl;self.fetched=time.monotonic()
            except Exception:
                # Failed renewals must still attempt revocation propagation.
                try:
                    crl=self._read_crl()
                    with self.lock:
                        self.crl=crl;self.fetched=time.monotonic()
                except Exception:
                    pass  # Existing state expires at the unchanged five-minute bound.

    def close(self):
        self.stop_event.set()
        self.thread.join(timeout=10)
        self.client.close()
