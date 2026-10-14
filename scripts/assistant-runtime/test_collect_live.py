import importlib.util
from pathlib import Path
import unittest

spec=importlib.util.spec_from_file_location('collector',Path(__file__).with_name('collect_live.py'))
collector=importlib.util.module_from_spec(spec);spec.loader.exec_module(collector)

class CollectionTest(unittest.TestCase):
    def test_cadvisor_maps_only_current_selected_pod_and_container(self):
        pod={'metadata':{'uid':'actual-uid'},'spec':{'containers':[{'name':'app'}]}}
        data='\n'.join([
            'container_cpu_usage_seconds_total{namespace="owned",pod="a",container="app",cpu="total"} 12.5 1700000000000',
            'container_cpu_usage_seconds_total{namespace="other",pod="a",container="app",cpu="total"} 900 1700000000000',
            'container_cpu_usage_seconds_total{namespace="owned",pod="a",container="POD",cpu="total"} 800 1700000000000',
            'container_cpu_usage_seconds_total{namespace="owned",pod="a",container="app",cpu="cpu00"} 700 1700000000000',
            'container_memory_working_set_bytes{namespace="owned",pod="a",container="app"} 4096 1700000000000'])
        result=collector.cadvisor(data,{('owned','a'):pod})
        self.assertEqual(len(result),2)
        self.assertEqual(result[0][1:],('actual-uid','owned','app',12.5,1700000000000))
        self.assertEqual(result[1][4],4096)
    def test_sink_cannot_be_changed_to_external_endpoint(self):
        with self.assertRaises(ValueError):collector.send('https://outside.example','/write','','text/plain')

if __name__=='__main__':unittest.main()
