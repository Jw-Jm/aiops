"""Build the scoped collector from the existing pinned VictoriaLogs source, outside the repository."""
import argparse, difflib, hashlib, json, os, pathlib, subprocess, tarfile

parser=argparse.ArgumentParser()
parser.add_argument('--archive',type=pathlib.Path,required=True)
parser.add_argument('--output',type=pathlib.Path,required=True)
args=parser.parse_args()
assert hashlib.sha256(args.archive.read_bytes()).hexdigest()=='66671ddad5d614eda7cec045e500cdaf48248cd68d4be2d454f4ba21bd6c6767'
args.output.mkdir(parents=True,exist_ok=False)
with tarfile.open(args.archive) as archive:archive.extractall(args.output,filter='data')
source=args.output/'VictoriaLogs-46a54c976fa3d404396050e8a5ee6c5b0320efc5'
assert hashlib.sha256((source/'LICENSE').read_bytes()).hexdigest()=='778f2412aa6e874bf0e2482e17a119ea53ba508ae6ddd924859a1f32f6e2740f'
base=source/'app/vlagent/kubernetescollector';before={}
for name in ['client.go','collector.go','processor.go','kubernetes.go']:before[name]=(base/name).read_text()
s=before['client.go'];needle='type objectMeta struct {';assert s.count(needle)==1
(base/'client.go').write_text(s.replace(needle,needle+'\n\tUID string `json:"uid"`',1))
s=before['collector.go'];needle='fs.Add("kubernetes.container_name", cs.Name)';assert s.count(needle)==1
(base/'collector.go').write_text(s.replace(needle,'fs.Add("kubernetes.pod_uid", p.Metadata.UID)\n\tfs.Add("kubernetes.namespace_uid", ns.Metadata.UID)\n\t'+needle,1))
s=before['kubernetes.go'];needle='podName, err := os.Hostname()';assert s.count(needle)==1
s=s.replace(needle,'podName := os.Getenv("POD_NAME")\n\tif podName == "" { podName, err = os.Hostname() }',1)
(base/'kubernetes.go').write_text(s)
s=before['processor.go'];needle='lfp.addRow(timestamp, parser.Fields)';assert s.count(needle)==1
s=s.replace(needle,'lfp.addRow(criTimestamp, opsFields(parser.Fields, bytesutil.ToUnsafeString(line)))',1)
s+='''
// Keep native identity and CRI event time authoritative, and preserve the original message.
func opsFields(fields []logstorage.Field, raw string) []logstorage.Field {
 level := "unknown"
 out := fields[:0]
 for _,field := range fields {
  if field.Name=="level" {switch strings.ToLower(field.Value) {case "error","warn","info","debug":level=strings.ToLower(field.Value);case "warning":level="warn"};continue}
  if strings.HasPrefix(field.Name,"kubernetes.") || field.Name=="tenant" || field.Name=="cluster" || field.Name=="collector" || field.Name=="_msg" || field.Name=="_time" || field.Name=="_stream" || field.Name=="_stream_id" {continue}
  out=append(out,field)
 }
 return append(out,logstorage.Field{Name:"_msg",Value:raw},logstorage.Field{Name:"level",Value:level})
}
'''
(base/'processor.go').write_text(s)
(base/'ops_identity_test.go').write_text('''package kubernetescollector
import("testing";"github.com/VictoriaMetrics/VictoriaLogs/lib/logstorage")
func TestOpsIdentityAndOriginalMessage(t *testing.T){
 p:=pod{};p.Metadata.UID="pod-uid";ns:=namespace{};ns.Metadata.UID="namespace-uid"
 fields:=getCommonFields(node{},ns,p,containerStatus{})
 if mustGetFieldValByName(fields,"kubernetes.pod_uid")!="pod-uid" || mustGetFieldValByName(fields,"kubernetes.namespace_uid")!="namespace-uid" {t.Fatal("native UID missing")}
 parsed:=[]logstorage.Field{{Name:"kubernetes.pod_uid",Value:"forged"},{Name:"tenant",Value:"forged"},{Name:"_msg",Value:"changed"},{Name:"level",Value:"WARNING"}}
 out:=opsFields(parsed,"native original")
 if len(out)!=2 || out[0].Name!="_msg" || out[0].Value!="native original" || out[1].Value!="warn" {t.Fatalf("metadata or message changed: %v",out)}
}
''')
subprocess.run(['gofmt','-w',str(base/'client.go'),str(base/'collector.go'),str(base/'processor.go'),str(base/'ops_identity_test.go'),str(base/'kubernetes.go')],check=True)
patch=''
for name,old in before.items():patch+=''.join(difflib.unified_diff(old.splitlines(True),(base/name).read_text().splitlines(True),fromfile='a/app/vlagent/kubernetescollector/'+name,tofile='b/app/vlagent/kubernetescollector/'+name))
(args.output/'ops-native-identity.patch').write_text(patch)
env=os.environ.copy();env['CGO_ENABLED']='0'
with (args.output/'unit.log').open('w') as log:subprocess.run(['go','test','-mod=vendor','./app/vlagent/kubernetescollector','-run','TestOpsIdentityAndOriginalMessage','-timeout','60s'],cwd=source,env=env,stdout=log,stderr=subprocess.STDOUT,check=True)
env.update(GOOS='linux',GOARCH='amd64')
with (args.output/'build.log').open('w') as log:subprocess.run(['go','build','-mod=vendor','-trimpath','-o',str(args.output/'vlagent-ops'), './app/vlagent'],cwd=source,env=env,stdout=log,stderr=subprocess.STDOUT,check=True)
receipt={'sourceCommit':'46a54c976fa3d404396050e8a5ee6c5b0320efc5','version':'v1.52.0-ops1','sourceArchiveSHA256':hashlib.sha256(args.archive.read_bytes()).hexdigest(),'patchSHA256':hashlib.sha256((args.output/'ops-native-identity.patch').read_bytes()).hexdigest(),'binarySHA256':hashlib.sha256((args.output/'vlagent-ops').read_bytes()).hexdigest(),'license':'Apache-2.0','CGO_ENABLED':'0','unit':'TestOpsIdentityAndOriginalMessage passed','scope':'task canary collector only; no core Bundle promotion'}
(args.output/'material.json').write_text(json.dumps(receipt,indent=2));print(json.dumps(receipt))
