"""Official MCP SDK bridge shared by fixed collection and HolmesGPT."""
import asyncio
from mcp import ClientSession
from mcp.client.streamable_http import streamable_http_client

class MCPBridge:
    def __init__(self, endpoint, token, http_client):
        self.endpoint=endpoint
        self._token=token
        self._client=http_client
        self._runner=asyncio.Runner()
        self.token_provider=lambda: self._token

    async def _request(self, name=None, params=None, allocation=None):
        self._client.headers["Authorization"]="Bearer "+self.token_provider()
        async with streamable_http_client(self.endpoint, http_client=self._client) as (read,write,_):
            async with ClientSession(read,write) as session:
                initialized=await session.initialize()
                if initialized.protocolVersion != "2025-06-18":
                    raise RuntimeError("MCP_PROTOCOL_UNVERIFIED")
                if name is None:
                    return (await session.list_tools()).tools
                result=await session.call_tool(name,params,meta={"ops/stepId":allocation["stepId"],"ops/jti":allocation["jti"]})
                if result.structuredContent is None:
                    raise RuntimeError("MCP_STRUCTURED_RESULT_REQUIRED")
                return result.structuredContent

    def close(self):
        self._runner.run(self._client.aclose())
        self._runner.close()

    def catalog(self):return self._runner.run(self._request())
    def call(self,name,params,allocation):return self._runner.run(self._request(name,params,allocation))
