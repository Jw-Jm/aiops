import asyncio
from concurrent.futures import ThreadPoolExecutor
from threading import Barrier
from types import SimpleNamespace
from unittest.mock import AsyncMock
import unittest
from investigator.mcp_bridge import MCPBridge

class ConcurrentMCPCalls(unittest.TestCase):
    def test_one_official_runner_handles_parallel_holmes_calls(self):
        client = SimpleNamespace(aclose=AsyncMock())
        bridge = MCPBridge("https://127.0.0.1/mcp", "unit-token", client)
        async def request(name=None, params=None, allocation=None):
            await asyncio.sleep(0.03)
            return params["index"] if params else -1
        bridge._request = request
        self.assertEqual(bridge.catalog(), -1)
        barrier = Barrier(4)
        def call(index):
            barrier.wait()
            return bridge.call("resource_history", {"index": index}, {})
        try:
            with ThreadPoolExecutor(max_workers=4) as executor:
                self.assertEqual(list(executor.map(call, range(4))), list(range(4)))
        finally:
            bridge.close()
        client.aclose.assert_awaited_once()
