"""The resident Contract wrapper around the locked HolmesGPT runtime."""
import json
import os
import ssl
import threading
import time
import sys
import traceback
from pathlib import Path
from http.server import BaseHTTPRequestHandler, ThreadingHTTPServer
from cryptography import x509
from cryptography.x509.oid import ExtensionOID
from datetime import datetime, timezone
from .model_config import ModelContract
from .runtime_config import RuntimeConfig


def serve(config):
    config=RuntimeConfig.model_validate(config).checked()
    model=ModelContract.model_validate(config["model"])
    # Locked upstream official environment setting, before importing its LLM.
    os.environ["LLM_REQUEST_TIMEOUT"]=str(model.timeout)
    os.environ["OVERRIDE_MAX_OUTPUT_TOKEN"]=str(model.token_budget)
    os.environ["LITELLM_LOCAL_MODEL_COST_MAP"]="True"
    from .holmes_adapter import MCPBridge, investigate
    from .job_api import JobAPI
    import httpx
    gate=threading.BoundedSemaphore(10)
    tls=ssl.create_default_context(ssl.Purpose.CLIENT_AUTH,cafile=config["caFile"])
    tls.minimum_version=ssl.TLSVersion.TLSv1_3
    tls.load_cert_chain(config["certificateFile"],config["privateKeyFile"])
    tls.verify_mode=ssl.CERT_REQUIRED

    from .workload_tls import PeerVerifier, clients
    verifier=PeerVerifier(config["caFile"],config["crlFile"])
    def trusted_peer(connection):
        try:
            verifier.verify(connection.getpeercert(binary_form=True),config["workerIdentity"])
            return True
        except Exception:
            return False

    class Handler(BaseHTTPRequestHandler):
        def log_message(self,*args):pass # Credentials and untrusted evidence never enter access logs.
        def respond(self,status,value):
            b=json.dumps(value).encode();self.send_response(status);self.send_header("Content-Type","application/json");self.send_header("Content-Length",str(len(b)));self.end_headers();self.wfile.write(b)
        def setup(self):
            self.request.settimeout(10)
            super().setup()
        def do_POST(self):
            if self.path!="/internal/v1/investigate" or not trusted_peer(self.connection):self.respond(403,{"errorCode":"FORBIDDEN"});return
            try: length=int(self.headers.get("Content-Length","0"))
            except ValueError: self.respond(400,{"errorCode":"INVALID_REQUEST"});return
            if not 0<length<=65536:self.respond(400,{"errorCode":"INVALID_REQUEST"});return
            if not gate.acquire(blocking=False):self.respond(429,{"errorCode":"INVESTIGATOR_BUSY"});return
            job_api=None;bridge=None
            try:
                payload=json.loads(self.rfile.read(length))
                if set(payload)!={"job","jobContext","mcpContext"}:raise ValueError()
                sync, asynchronous=clients(config)
                job_api=JobAPI(config["jobAPI"],payload["job"]["jobId"],payload["jobContext"],sync)
                job_api.start_renewal(payload["mcpContext"])
                bridge=MCPBridge(config["mcpEndpoint"],payload["mcpContext"],asynchronous)
                bridge.token_provider=lambda:job_api.mcp_token
                result=investigate(payload["job"],model,job_api,bridge,config["modelAPIKeyFile"])
                result=job_api.request("/result:complete",result)
                self.respond(200,{"data":result})
            except Exception as error:
                for frame in traceback.extract_tb(error.__traceback__):print("investigator-failure",type(error).__name__,frame.filename,frame.lineno,file=sys.stderr)
                self.respond(503,{"errorCode":"INVESTIGATION_FAILED"})
            finally:
                if job_api is not None:job_api.close()
                if bridge is not None:bridge.close()
                gate.release()
    host,port=config["listenAddress"].rsplit(":",1)
    server=ThreadingHTTPServer((host,int(port)),Handler)
    server.socket.settimeout(5)
    server.socket=tls.wrap_socket(server.socket,server_side=True)
    server.serve_forever(poll_interval=0.2)

if __name__=="__main__":
    config=json.loads(Path(os.environ["SP06_INVESTIGATOR_FILE"]).read_text())
    serve(config)
