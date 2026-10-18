#!/usr/bin/env python3
"""One standalone 120s retention probe on this run's committed evidence only."""
import json
import time
import urllib.request
from urllib.parse import urlsplit
from datetime import datetime
from pathlib import Path
from verify_long_context import select_observations, verify

base = Path('/opt/ops-five-source-reverify-20261018/round5')
runtime = json.loads((base/'joint-runtime-report.json').read_text())
assert runtime['passed'] is True
context = runtime['context']
results = runtime['committedQueryResults']
receipt = json.loads((base/'live-source/receipt.json').read_text())
cfg = receipt['config']
target = urlsplit(context['request']['targetCanonicalId'])
assert target.netloc == cfg['tenantId'] and target.path.split('/')[1] == cfg['clusterUid'], 'committed context and collection scope differ'
assert datetime.fromisoformat(receipt['startedAt']) <= datetime.fromisoformat(context['request']['baseline']['from'].replace('Z','+00:00'))
assert datetime.fromisoformat(receipt['lastObservedAt']) >= datetime.fromisoformat(context['request']['recovery']['to'].replace('Z','+00:00'))
with (base/'live-source/observations.jsonl').open() as source:
    rows = select_observations((json.loads(line) for line in source), context, cfg, 1500)
assert rows, 'no same-scope raw rows inside the committed windows'
payload = {'committedContext':context, 'committedQueryResults':results, 'rawObservationIndex':rows}
question = ('只输出 JSON，不确认根因，不编造数字。键为 windows、httpFault、tcpFault、httpRecovery、missingEvidence、sourceRole。'
    'windows 按 baseline/fault/recovery 列出 fromUnix/toUnix；httpFault 为已提交 HTTP 故障错误率 current/baseline；'
    'tcpFault 为已提交 TCP 故障 current/baseline/delta；httpRecovery 为已提交 HTTP 恢复错误率 current/baseline/delta。'
    'delta 必须是 current-baseline 的有符号差值，不得把 decrease 错写为 increase。'
    'missingEvidence 按 edges 原顺序逐条列出，保留重复项，不改写。'
    'sourceRole 为 {"committedContext":"已提交证据","rawObservationIndex":"原始观测"}。')
body = {'model':'deepseek-flash','max_tokens':8192,'messages':[{'role':'user','content':json.dumps(payload,ensure_ascii=False)+'\n'+question}]}
with (base/'retention-request.json').open('x') as out:
    out.write(json.dumps(body,ensure_ascii=False)+'\n')
key = Path('/opt/ops-assistant-dev-20261012/runtime-private/deepseek-acceptance.key').read_text().strip()
request = urllib.request.Request('https://api.deepseek.com/anthropic/v1/messages',json.dumps(body).encode(),
    {'content-type':'application/json','x-api-key':key,'anthropic-version':'2023-06-01'})
started = time.monotonic()
with urllib.request.urlopen(request,timeout=120) as reply:
    response = json.load(reply)
answer = {'usage':response['usage'],'stopReason':response['stop_reason'],
    'answer':''.join(block.get('text','') for block in response.get('content',[])),
    'elapsedSeconds':round(time.monotonic()-started,2),'observationRows':len(rows)}
with (base/'retention-response.json').open('x') as out:
    out.write(json.dumps(answer,ensure_ascii=False,indent=2)+'\n')
result = verify(context,results,receipt,answer)
result.update({'timeoutSeconds':120,'outputTokenLimit':8192,'observationRows':len(rows),'elapsedSeconds':answer['elapsedSeconds']})
with (base/'retention-verification.json').open('x') as out:
    out.write(json.dumps(result,ensure_ascii=False,indent=2)+'\n')
print(json.dumps(result,ensure_ascii=False))
raise SystemExit(0 if result['passed'] else 1)
