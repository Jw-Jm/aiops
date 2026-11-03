from pathlib import Path
import os,socket,subprocess,tarfile,urllib.parse
assert socket.gethostname()=='mssc'
base=Path('/data/ops-v12-native-visual-r24-20261026');work=base/'r48'
with tarfile.open(work/'ops-v12-r48-migrations.tgz') as archive:archive.extractall(work/'source',filter='data')
env=os.environ.copy();env['SP03_TEST_DATABASE_URL']='postgresql://postgres:'+urllib.parse.quote((base/'private/pg-password').read_text().strip(),safe='')+'@127.0.0.1:15454/postgres?sslmode=disable'
with (work/'integration-passed.log').open('x') as log:
 result=subprocess.run([str(work/'ops-v12-r48-integration.test'),'-test.run','^(TestNativeObservationSourceAuthorization|TestNativeObservationQueryLifecycle)$','-test.v','-test.timeout','180s'],cwd=work/'source/test/integration',env=env,stdout=log,stderr=subprocess.STDOUT)
print('disposable PG native-source authorization/lifecycle exit',result.returncode);raise SystemExit(result.returncode)
