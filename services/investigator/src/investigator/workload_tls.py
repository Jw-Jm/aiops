"""Check the current workload identity before HTTP credentials are transmitted."""
import ssl
import time
import re
from pathlib import Path
from datetime import datetime, timezone
from cryptography import x509
from cryptography.x509.oid import ExtensionOID
import httpx
from cryptography.exceptions import InvalidSignature

class PeerVerifier:
    def __init__(self, ca_file, crl_file, current=None):
        self.ca_file=Path(ca_file)
        self.crl_file=Path(crl_file)
        self.current=current

    def current_trust(self):
        if self.current is not None:
            return self.current()
        now=datetime.now(timezone.utc)
        crl=x509.load_pem_x509_crl(self.crl_file.read_bytes())
        authorities=x509.load_pem_x509_certificates(self.ca_file.read_bytes())
        issuer=next((ca for ca in authorities if ca.subject==crl.issuer and crl.is_signature_valid(ca.public_key()) and ca.not_valid_before_utc<=now<ca.not_valid_after_utc),None)
        age=time.time()-self.crl_file.stat().st_mtime
        if not -30<=age<=300 or issuer is None or not(crl.last_update_utc<=now<crl.next_update_utc):
            raise RuntimeError("WORKLOAD_CRL_UNAVAILABLE")
        return crl,issuer

    def current_crl(self):
        return self.current_trust()[0]

    def verify(self, der, identity):
        cert=x509.load_der_x509_certificate(der)
        san=cert.extensions.get_extension_for_oid(ExtensionOID.SUBJECT_ALTERNATIVE_NAME).value
        uris=san.get_values_for_type(x509.UniformResourceIdentifier)
        matched=re.fullmatch(r"spiffe://ops.local/ns/([a-z0-9](?:[-a-z0-9]{0,61}[a-z0-9])?)/sa/([a-z0-9](?:[-a-z0-9]{0,61}[a-z0-9])?)",identity)
        crl,issuer=self.current_trust()
        now=datetime.now(timezone.utc)
        if not matched or uris != [identity] or san.get_values_for_type(x509.DNSName) != [matched[2]+'.'+matched[1]+'.svc.cluster.local'] or san.get_values_for_type(x509.IPAddress) or san.get_values_for_type(x509.RFC822Name) or not(cert.not_valid_before_utc<=now<cert.not_valid_after_utc) or crl.get_revoked_certificate_by_serial_number(cert.serial_number) is not None:
            raise RuntimeError("WORKLOAD_IDENTITY_REJECTED")
        # Current PKI roles issue directly from the pinned workload CA. A CRL
        # signed by another trusted CA cannot describe this leaf's revocation.
        try:
            cert.verify_directly_issued_by(issuer)
        except (ValueError, TypeError, InvalidSignature):
            raise RuntimeError("WORKLOAD_IDENTITY_REJECTED") from None


def clients(config, identity=None):
    verifier=PeerVerifier(config["caFile"],config["crlFile"],identity.current_trust if identity else None)
    namespace=config["apiServerName"][len("ops-api."):-len(".svc.cluster.local")]
    peer_identity="spiffe://ops.local/ns/"+namespace+"/sa/ops-api"
    tls=None
    if identity is None:
        tls=ssl.create_default_context(cafile=config["caFile"])
        tls.minimum_version=ssl.TLSVersion.TLSv1_3
        tls.load_cert_chain(config["certificateFile"],config["privateKeyFile"])
    # The pinned httpcore trace runs after TLS authentication, before sending
    # HTTP headers. Disable idle connection reuse so every request also checks
    # a freshly loaded CRL and the exact SPIFFE URI, including Context renewals.
    def trace(event, info):
        if event=="connection.start_tls.complete":
            peer=info["return_value"].get_extra_info("ssl_object")
            verifier.verify(peer.getpeercert(True),peer_identity)
    async def trace_async(event,info):trace(event,info)
    def prepare(request):
        verifier.current_crl()
        request.extensions["sni_hostname"]=config["apiServerName"]
        request.extensions["trace"]=trace
    async def prepare_async(request):
        prepare(request)
        request.extensions["trace"]=trace_async
    settings=dict(timeout=5,follow_redirects=False,trust_env=False,limits=httpx.Limits(max_keepalive_connections=0))
    if identity:
        # A new transport captures the latest immutable certificate for each
        # request, including requests made by an already running Job.
        class OwnedStream(httpx.SyncByteStream):
            def __init__(self,stream,transport):self.stream=stream;self.transport=transport
            def __iter__(self):yield from self.stream
            def close(self):
                try:self.stream.close()
                finally:self.transport.close()
        class OwnedAsyncStream(httpx.AsyncByteStream):
            def __init__(self,stream,transport):self.stream=stream;self.transport=transport
            async def __aiter__(self):
                async for block in self.stream:yield block
            async def aclose(self):
                try:await self.stream.aclose()
                finally:await self.transport.aclose()
        class CurrentTransport(httpx.BaseTransport):
            def handle_request(self,request):
                transport=httpx.HTTPTransport(verify=identity.client_context(),trust_env=False)
                try:
                    response=transport.handle_request(request)
                    response.stream=OwnedStream(response.stream,transport)
                    return response
                except BaseException:
                    transport.close()
                    raise
        class CurrentAsyncTransport(httpx.AsyncBaseTransport):
            async def handle_async_request(self,request):
                transport=httpx.AsyncHTTPTransport(verify=identity.client_context(),trust_env=False)
                try:
                    response=await transport.handle_async_request(request)
                    response.stream=OwnedAsyncStream(response.stream,transport)
                    return response
                except BaseException:
                    await transport.aclose()
                    raise
        return (httpx.Client(transport=CurrentTransport(),event_hooks={"request":[prepare]},**settings),
                httpx.AsyncClient(transport=CurrentAsyncTransport(),event_hooks={"request":[prepare_async]},**settings))
    settings["verify"]=tls
    return (httpx.Client(event_hooks={"request":[prepare]},**settings),
            httpx.AsyncClient(event_hooks={"request":[prepare_async]},**settings))
