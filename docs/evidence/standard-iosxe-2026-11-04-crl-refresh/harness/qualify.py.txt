"""Fresh image and fresh CA chain: observe releases before and after serial replacement."""
import pathlib,json,datetime,time,subprocess,os,ssl,urllib.request,yaml
R=pathlib.Path('/tmp/cvk-crlrefresh-20261104');exec(compile((R/'control_functions.py').read_text(),'cold-control-functions','exec'));P=json.loads((R/'payload-provenance.json').read_text());SEEN=set();IPS={'10.1.1.1','192.168.129.2'}
def events(case=None):
 return [e for l in (R/'server-events.jsonl').read_text().splitlines() if (e:=json.loads(l)).get('client') in IPS and (case is None or e.get('case')==case)]
def lease():return next((x for x in get(['-n',NS,'get','leases','-o','json'])['items'] if x.get('metadata',{}).get('labels',{}).get('cisco.vk/family')=='device-disruptive-mutation'),None)
def expiry(l):
 if not l or not l.get('spec',{}).get('holderIdentity'):return None
 return datetime.datetime.fromisoformat(l['spec']['renewTime'].replace('Z','+00:00'))+datetime.timedelta(seconds=l['spec']['leaseDurationSeconds'])
def observe(label):
 s=snap(label);controls(label);assertBaselineApps(s);SEEN.update(p['metadata']['uid'] for p in s['pods']);return s
def releases(s):
 out=[]
 for p in s['probes']:
  if p.get('status')==200:
   try:out.append({'appID':p['appID'],'release':json.loads(p['body']).get('release'),'ip':p['ip']})
   except ValueError:pass
 return out
def pki(label):run(['python3',str(R/'check_pki.py'),label])
def deploy(case):
 image='https://192.168.129.111:18469/'+case+'.tar';template={'metadata':{'labels':{'app':NAME,'qualification':'20261104-crlrefresh','cert-case':case}},'spec':{'affinity':{'nodeAffinity':{'requiredDuringSchedulingIgnoredDuringExecution':{'nodeSelectorTerms':[{'matchFields':[{'key':'metadata.name','operator':'In','values':[DEV]}]}]}}},'tolerations':[{'key':'cisco.vk/device','operator':'Exists','effect':'NoSchedule'}],'containers':[{'name':'agent','image':image,'imagePullPolicy':'Always','ports':[{'containerPort':8080}],'resources':{'requests':{'memory':'128Mi'},'limits':{'memory':'128Mi'}}}]}};obj={'apiVersion':'apps/v1','kind':'Deployment','metadata':{'name':NAME,'namespace':APPS,'labels':{'qualification':'20261104-crlrefresh'}},'spec':{'replicas':1,'strategy':{'type':'Recreate'},'selector':{'matchLabels':{'app':NAME}},'template':template}};(R/('deployment-'+case+'-input.yaml')).write_text(yaml.safe_dump(obj));apply(obj);return image
def health(label,release):
 s=healthy(label,release);SEEN.update(p['metadata']['uid'] for p in s['pods']);nativeProof(label,s['pods'][0]);leaseReleased(label);return s
def deletion(label):
 run(K+['-n',APPS,'scale','deployment/'+NAME,'--replicas=0']);deadline=time.monotonic()+3000;i=0
 while time.monotonic()<deadline:
  s=observe(label+'-normal-cleanup')
  if not s['pods'] and {a['appID']:a['details'].get('state') for a in s['nativeApps']}=={'iris':'RUNNING','c9ktest':'DEPLOYED'}:leaseReleased(label+'-normal-cleanup');return
  if i%4==0:print(now(),label,'normal deletion pending',str(expiry(lease())),flush=True)
  i+=1;time.sleep(10)
 raise RuntimeError('Normal deletion timeout; no Lease reset allowed')
def waitGuard():
 deadline=time.monotonic()+3300;i=0
 while time.monotonic()<deadline:
  l=lease();end=expiry(l)
  if not end or datetime.datetime.now(datetime.timezone.utc)>=end:return
  s=observe('refresh-guard-'+str(i));row={'atUTC':now(),'leaseUID':l['metadata']['uid'],'holder':l['spec'].get('holderIdentity'),'renewTime':l['spec'].get('renewTime'),'expiryUTC':end.isoformat(),'podUIDs':[p['metadata']['uid'] for p in s['pods']],'ready':any(ready(p) for p in s['pods']),'actualReleases':releases(s),'artifactJournals':len(s['journals'])}
  with (R/'guarded-observations.jsonl').open('a') as f:f.write(json.dumps(row)+'\n')
  if i%4==0:print(now(),'refresh waiting for normal fence',end.isoformat(),flush=True)
  i+=1;time.sleep(min(15,max(1,(end-datetime.datetime.now(datetime.timezone.utc)).total_seconds())))
 raise RuntimeError('Fence timeout; never clear Lease')
def pub(version):run(['python3',str(R/'publish_crl.py'),str(version)])
def probe(label,accepted):
 ctx=ssl.create_default_context(cafile=str(R/'root-current-crl.pem'));ctx.verify_flags|=ssl.VERIFY_CRL_CHECK_CHAIN
 try:
  with urllib.request.urlopen('https://192.168.129.111:18469/_probe',context=ctx,timeout=10) as response:assert response.status==200
  assert accepted;result={'accepted':True}
 except urllib.error.URLError as e:
  if not isinstance(e.reason,ssl.SSLCertVerificationError):raise
  assert not accepted and isinstance(e.reason,ssl.SSLCertVerificationError) and e.reason.verify_code==23;result={'accepted':False,'verificationCode':23}
 save('tls-control-'+label,{'atUTC':now(),'currentPublishedCRLUsed':True,'hostOnly':True,**result})
def append(n,row):
 with (R/(n+'.jsonl')).open('a') as f:f.write(json.dumps(row)+'\n')
started=now();s=observe('baseline');assert not s['pods'] and {a['appID']:a['details'].get('state') for a in s['nativeApps']}=={'iris':'RUNNING','c9ktest':'DEPLOYED'}
pub(1)
with (R/'servers.log').open('wb') as out:server=subprocess.Popen(['python3',str(R/'servers.py')],stdout=out,stderr=subprocess.STDOUT,start_new_session=True)
(R/'servers.pid').write_text(str(server.pid))
for i in range(30):
 try:probe('valid',True);break
 except OSError:time.sleep(1)
pki('baseline');deploy('valid');valid=health('valid',P['phases']['valid']['release']);pki('valid');assert any(e['kind']=='crl-served' and e.get('version')==1 for e in events());assert any(e['kind']=='package-sent' and e.get('phase')=='valid' for e in events())
# Only the signed CRL at the same CDP changes now; the TLS certificate stays original.
pub(2);published=json.loads((R/'crl-v2.json').read_text());oldCRL=json.loads((R/'crl-v1.json').read_text());expires=datetime.datetime.fromisoformat(oldCRL['nextUpdateUTC']);probe('revoked',False);still=observe('existing-runtime-after-publication');assert any(v['release']==P['phases']['valid']['release'] for v in releases(still));save('release-after-publication',{'atUTC':now(),'actualReleases':releases(still),'autoEvictionObserved':False});pki('just-published');deletion('valid')
assert datetime.datetime.now(datetime.timezone.utc)<expires-datetime.timedelta(seconds=120),'Initial CRL expired too early for the cached-window control'
assert not any(e['kind']=='package-sent' and e.get('phase')=='cached' for e in events());save('cached-image-unprimed',{'atUTC':now(),'phase':'cached','packageSentToC9K':False,**P['phases']['cached']});deploy('cached')
# Characterize either cache-window acceptance or earlier refresh without forcing a result.
deadline=time.monotonic()+180;cacheHealthy=False;cacheOutcome=None;i=0
while time.monotonic()<deadline:
 s=observe('cached-window-'+str(i));newCRL=[e for e in events() if e['kind']=='crl-served' and e.get('version')==2];actual=releases(s);row={'atUTC':now(),'originalCRLNextUpdateUTC':expires.isoformat(),'newCRLRetrieved':bool(newCRL),'ready':any(ready(p) for p in s['pods']),'actualReleases':actual};append('cache-window-observations',row)
 if any(v['release']==P['phases']['cached']['release'] for v in actual) and row['ready']:
  cached=health('cached',P['phases']['cached']['release']);cacheHealthy=True;cacheOutcome='fresh-image-accepted-using-cached-unrevoked-CRL';break
 if newCRL and any(e['kind']=='tls-rejected' and e['atUTC']>published['publishedAtUTC'] for e in events()):cacheOutcome='revocation-discovered-before-original-CRL-nextUpdate';cached=s;break
 i+=1;time.sleep(10)
if cacheOutcome is None:cacheOutcome='cached-window-inconclusive';cached=s
pki('cached-boundary');save('cached-window-result',{'atUTC':now(),'classification':cacheOutcome,'actualReleases':releases(cached),'ready':any(ready(p) for p in cached['pods']),'originalCRLNextUpdateUTC':expires.isoformat(),'beforeOriginalNextUpdate':datetime.datetime.now(datetime.timezone.utc)<expires});print(now(),'CACHE WINDOW',cacheOutcome,releases(cached),flush=True)
# Observe whether refresh is autonomous while an already-running app remains idle.
i=0
while cacheHealthy and datetime.datetime.now(datetime.timezone.utc)<expires-datetime.timedelta(seconds=100):
 pki('idle-cache-'+str(i));append('idle-observations',{'atUTC':now(),'version2NativeRetrievals':sum(e['kind']=='crl-served' and e.get('version')==2 for e in events()),'originalCRLNextUpdateUTC':expires.isoformat()});i+=1;time.sleep(20)
deletion('cached')
i=0
while datetime.datetime.now(datetime.timezone.utc)<expires+datetime.timedelta(seconds=5):
 pki('expiry-wait-'+str(i));i+=1;time.sleep(min(15,max(1,(expires+datetime.timedelta(seconds=5)-datetime.datetime.now(datetime.timezone.utc)).total_seconds())))
pki('before-refresh-demand');assert not any(e['kind']=='package-sent' and e.get('phase')=='refresh' for e in events());save('refresh-image-unprimed',{'atUTC':now(),'phase':'refresh','packageSentToC9K':False,**P['phases']['refresh']});url=deploy('refresh');start=time.monotonic();i=0;classification=None
while time.monotonic()-start<720:
 s=observe('refresh-invalid-'+str(i));native=events();v2=[e for e in native if e['kind']=='crl-served' and e.get('version')==2];alerts=[e for e in native if e['kind']=='tls-rejected' and e['atUTC']>published['publishedAtUTC']];gets=[e for e in native if e['kind']=='http-request' and e.get('phase')=='refresh' and e.get('package')];l=lease();end=expiry(l);age=(datetime.datetime.now(datetime.timezone.utc)-datetime.datetime.fromisoformat(l['spec']['renewTime'].replace('Z','+00:00'))).total_seconds() if end else None;actual=releases(s);running=any(a['appID'].startswith('cvk0000_') and a['details'].get('state')=='RUNNING' for a in s['nativeApps']);row={'atUTC':now(),'podUIDs':[p['metadata']['uid'] for p in s['pods']],'ready':any(ready(p) for p in s['pods']),'nativeRunning':running,'actualReleases':actual,'newCRLNativeRetrievals':len(v2),'TLSRejections':len(alerts),'packageRequests':len(gets),'leaseRenewAgeSeconds':age,'leaseExpiresUTC':end.isoformat() if end else None,'artifactJournals':len(s['journals'])};append('invalid-observations',row)
 if i%4==0:print(json.dumps(row),flush=True)
 if gets:classification='fresh-image-delivery-accepted-after-CRL-expiry';bad=s;break
 if actual or running:classification='runtime-observed-without-fresh-image-transfer';bad=s;break
 if time.monotonic()-start>=300 and v2 and len(alerts)>=2 and not row['ready'] and not running and (end is None or age>=90):classification='new-revocation-discovered-and-fresh-delivery-rejected';bad=s;break
 i+=1;time.sleep(10)
if classification is None:classification='inconclusive-refresh';bad=s
pki('revoked-boundary');save('snapshot-invalid-boundary',bad);save('invalid-lease',lease());save('invalid-server-events',events());save('release-before-replacement',{'atUTC':now(),'classification':classification,'actualReleases':releases(bad),'podUIDs':[p['metadata']['uid'] for p in bad['pods']]});lines=[]
for w in bad['workers']:
 text=subprocess.check_output(K+['-n',NS,'logs',w['name'],'--tail=2000'],text=True);needles=[p['metadata']['uid'].replace('-','') for p in bad['pods']]+['refresh.tar'];lines.extend(v for v in text.splitlines() if any(n in v for n in needles))
save('worker-diagnostics',{'atUTC':now(),'lines':lines});print(now(),'SEALED REFRESH OUTCOME',classification,flush=True)
mode=json.loads((R/'server-mode.json').read_text());assert mode=={'certificate':'original','CRLversion':2};mode['certificate']='replacement';tmp=R/'mode-next.json';tmp.write_text(json.dumps(mode));os.replace(tmp,R/'server-mode.json');probe('replacement',True);save('certificate-replaced',{'atUTC':now(),'onlyLeafCertificateAndKeyChanged':True,'CRLOrPolicyChanged':False,'URLOrPayloadChanged':False,'revokedSerialRemoved':False});pki('after-replacement');waitGuard();recovery=health('recovery',P['phases']['refresh']['release']);same={p['metadata']['uid'] for p in bad['pods']}=={p['metadata']['uid'] for p in recovery['pods']};save('release-after-replacement',{'atUTC':now(),'actualReleases':releases(recovery),'samePodRetained':same});deletion('recovery');before=sum(e['kind']=='package-sent' and e.get('phase')=='refresh' for e in events());deploy('refresh');fresh=health('fresh',P['phases']['refresh']['release']);assert sum(e['kind']=='package-sent' and e.get('phase')=='refresh' for e in events())>before;save('release-after-fresh',{'atUTC':now(),'actualReleases':releases(fresh),'passed':True});deletion('fresh');pki('complete');v2=[e for e in events() if e['kind']=='crl-served' and e.get('version')==2];alerts=[e for e in events() if e['kind']=='tls-rejected' and e['atUTC']>published['publishedAtUTC']];save('qualification-result',{'executionComplete':True,'startedAtUTC':started,'finishedAtUTC':now(),'classification':classification,'cacheWindowOutcome':cacheOutcome,'revocationDiscoveryQualified':classification=='new-revocation-discovered-and-fresh-delivery-rejected','publicationUTC':published['publishedAtUTC'],'originalCRLNextUpdateUTC':expires.isoformat(),'firstVersion2NativeFetchUTC':v2[0]['atUTC'] if v2 else None,'firstRejectionUTC':alerts[0]['atUTC'] if alerts else None,'samePodRecoveryPassed':same,'freshDeploymentPassed':True,'normalDeletionPassed':True,'podUIDs':sorted(SEEN),'defaultMode':'Standard','governed':'deferred','metadataAndJournalOmitted':True,'manualLeaseOrJournalChanges':False,'forcedCRLRefreshOrFlushDuringQualification':False,'cachePolicyChanged':False,'clockChanged':False});print('COMPLETE CRL REFRESH QUALIFICATION',flush=True)
