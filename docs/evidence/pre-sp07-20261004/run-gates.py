import subprocess,pathlib,os,json,datetime
r=pathlib.Path(__file__).resolve().parent
root=r.parents[2]
env=dict(os.environ,OPS_PERFORMANCE_EXEMPTION='pre-sp07-user-20261004',GOPROXY='off',GOSUMDB='off')
commands=[['make','check-generated'],['make','check-runtime-source'],['make','check'],['make','test-security'],['make','test-replay']]
records=[]
for args in commands:
 name=args[-1]+'-baseline-r1'; started=datetime.datetime.now(datetime.timezone.utc).isoformat()
 with (r/(name+'.log')).open('w') as f: p=subprocess.run(args,cwd=root,env=env,stdout=f,stderr=subprocess.STDOUT)
 (r/(name+'.exit')).write_text(str(p.returncode)+'\n')
 record={'command':args,'exitCode':p.returncode,'startedAt':started,'completedAt':datetime.datetime.now(datetime.timezone.utc).isoformat(),'log':name+'.log','scope':'baseline code checks; skipped live tests do not count as runtime PASS'};records.append(record)
 (r/'baseline-gates.json').write_text(json.dumps(records,indent=2)+'\n');print(json.dumps(record),flush=True)
