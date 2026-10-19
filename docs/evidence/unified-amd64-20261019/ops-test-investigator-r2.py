from pathlib import Path
import ast,json,subprocess
base=Path('/opt/ops-verification-20261019-1a7c7da');out=base/'distribution';repo=base/'repo';revision='1a7c7da4af7d74d80df0df0aec72868cbe2785cc'
# Keep the repository-relative fixture paths used by the native tests.
command=['docker','run','--rm','--pull=never','--network=none','--read-only','--cap-drop=ALL','--security-opt=no-new-privileges','--tmpfs=/tmp:rw,uid=65532,gid=65532,mode=0700,size=128m','--mount=type=bind,src='+str(out/'holmesgpt-overlay-context/wrapper')+',dst=/native-project/services/investigator,readonly','--mount=type=bind,src='+str(repo/'test/fixtures/assistant')+',dst=/native-project/test/fixtures/assistant,readonly','--entrypoint','python','ops.local/verification-20261019/holmesgpt:1a7c7da','-m','unittest','discover','-s','/native-project/services/investigator/tests','-p','*unittest.py','-v']
with (out/'holmesgpt-native-tests-r2.log').open('w')as log:subprocess.run(command,stdout=log,stderr=subprocess.STDOUT,check=True,timeout=90)
import hashlib,tarfile,copy,io
module=ast.parse((out/'ops-build-native-candidate.py').read_text());functions=ast.Module(body=[n for n in module.body if isinstance(n,ast.FunctionDef)],type_ignores=[]);exec(compile(functions,'reviewed-native-helpers','exec'))
r=export('holmesgpt','ops.local/verification-20261019/holmesgpt:1a7c7da');r.update(json.loads((out/'holmesgpt-overlay-context/SOURCE-DISTRIBUTION.json').read_text()));receipts=json.loads((out/'native-build-receipts.json').read_text());receipts.append(r);(out/'native-build-receipts.json').write_text(json.dumps(receipts,indent=2)+'\n');print(json.dumps(r))
