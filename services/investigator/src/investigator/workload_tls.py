"""Check the current workload identity before HTTP credentials are transmitted."""
import ssl
import time
from pathlib import Path
from datetime import datetime, timezone
from cryptography import x509
from cryptography.x509.oid import ExtensionOID
import httpx

class PeerVerifier:
    def __init__(self, ca_file, crl_file):
        self.ca_file=Path(ca_file)
        self.crl_file=Path(crl_file)

    def current_crl(self):
        now=datetime.now(timezone.utc)
        crl=x509.load_pem_x509_crl(self.crl_file.read_bytes())
        ca=x509.load_pem_x509_certificate(self.ca_file.read_bytes())
        if time.time()-self.crl_file.stat().st_mtime>300 or not crl.is_signature_valid(ca.public_key()) or not(crl.last_update_utc<=now<crl.next_update_utc):
            raise RuntimeError("WORKLOAD_CRL_UNAVAILABLE")
        return crl

    def verify(self, der, identity):
        cert=x509.load_der_x509_certificate(der)
        uris=cert.extensions.get_extension_for_oid(ExtensionOID.SUBJECT_ALTERNATIVE_NAME).value.get_values_for_type(x509.UniformResourceIdentifier)
        if uris != [identity] or self.current_crl().get_revoked_certificate_by_serial_number(cert.serial_number) is not None:
            raise RuntimeError("WORKLOAD_IDENTITY_REJECTED")


def clients(config):
    verifier=PeerVerifier(config["caFile"],config["crlFile"])
    namespace=config["apiServerName"][len("ops-api."):-len(".svc.cluster.local")]
    identity="spiffe://ops.local/ns/"+namespace+"/sa/ops-api"
    tls=ssl.create_default_context(cafile=config["caFile"])
    tls.minimum_version=ssl.TLSVersion.TLSv1_3
    tls.load_cert_chain(config["certificateFile"],config["privateKeyFile"])
    # The pinned httpcore trace runs after TLS authentication, before sending
    # HTTP headers. Disable idle connection reuse so every request also checks
    # a freshly loaded CRL and the exact SPIFFE URI, including Context renewals.
    def trace(event, info):
        if event=="connection.start_tls.complete":
            peer=info["return_value"].get_extra_info("ssl_object")
            verifier.verify(peer.getpeercert(True),identity)
    async def trace_async(event,info):trace(event,info)
    def prepare(request):
        verifier.current_crl()
        request.extensions["sni_hostname"]=config["apiServerName"]
        request.extensions["trace"]=trace
    async def prepare_async(request):
        prepare(request)
        request.extensions["trace"]=trace_async
    settings=dict(verify=tls,timeout=5,follow_redirects=False,trust_env=False,limits=httpx.Limits(max_keepalive_connections=0))
    return (httpx.Client(event_hooks={"request":[prepare]},**settings),
            httpx.AsyncClient(event_hooks={"request":[prepare_async]},**settings))
