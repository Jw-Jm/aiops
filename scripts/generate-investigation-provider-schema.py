#!/usr/bin/env python3
"""Generate the provider-format view of the formal closed result Contract."""
from pathlib import Path
import json
root=Path(__file__).resolve().parents[1]
common=json.loads((root/'api/schemas/common.schema.json').read_text())
result=json.loads((root/'api/schemas/investigation-result-v1.schema.json').read_text())
action=json.loads((root/'api/schemas/action-plan-v2.schema.json').read_text())
for document in (result,action):
 document.pop('$schema',None);document.pop('$id',None)
result['properties']['actionPlans']['items']=action
result['$defs']=common['$defs']
def localize(v):
 if isinstance(v,dict):
  return {k:x.replace('https://ops.local/schemas/common/v1#/$defs/','#/$defs/') if k=='$ref' and isinstance(x,str) else localize(x) for k,x in v.items()}
 if isinstance(v,list):return [localize(x) for x in v]
 return v
(root/'services/investigator/src/investigator/output.schema.json').write_text(json.dumps(localize(result),indent=2)+'\n')
