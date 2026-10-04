import httpx
import threading

class JobAPI:
    """mTLS, audience-bound Go Job API; no database or fact source client."""
    def __init__(self, base_url, job_id, token, client):
        self.base_url = base_url.rstrip("/")+"/internal/v1/investigations/"+job_id
        self._token = token
        self._client = client
        self._stop=threading.Event()
        self._renew_error=False
        self.mcp_token=""

    def request(self, suffix, body=None):
        if self._renew_error: raise RuntimeError("CONTEXT_RENEWAL_FAILED")
        response = self._client.request("GET" if body is None else "POST",self.base_url+suffix,
                                        headers={"Authorization":"Bearer "+self._token},json=body)
        if response.status_code != 200:
            # Never include response body/request headers in exception logs.
            raise RuntimeError("JOB_API_REJECTED_"+str(response.status_code))
        return response.json()["data"]

    def allocate(self, name, args, model=False, reserve=None):
        body={"name":name,"arguments":args,"model":model}
        if reserve is not None: body["reserve"]=reserve
        return self.request("/calls:allocate",body)

    def settle(self, step_id, result, consumed=None, error_code=""):
        return self.request("/steps/"+step_id+":complete",{"result":result,"consumed":consumed,"errorCode":error_code})

    def start_renewal(self,mcp_token):
        self.mcp_token=mcp_token
        def loop():
            while not self._stop.wait(20):
                try:
                    contexts=self.request("/contexts:renew",{})
                    self._token=contexts["jobContext"]
                    self.mcp_token=contexts["mcpContext"]
                except Exception:
                    self._renew_error=True
                    return
        self._renew_thread=threading.Thread(target=loop,daemon=True)
        self._renew_thread.start()

    def close(self):
        self._stop.set()
        if hasattr(self,"_renew_thread"):self._renew_thread.join(timeout=5)
        self._client.close()
