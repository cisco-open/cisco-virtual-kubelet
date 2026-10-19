# One-shot lab recovery, not a production remediation executor.
import base64,datetime,hashlib,json,os,re,subprocess,pexpect
from pathlib import Path
root=Path('/tmp/cvk-swim-live-20261008')
path='flash:gNOI_iosxe_17.18.02.0.4112.1766116039.bin'
leaf='catc-handoff-20261008-101-r7-cat9k-lab-101-2a89361f'
def get(kind,name):
 return json.loads(subprocess.check_output(['kubectl','get',kind,name,'-n','cvk-live','-o','json']))
def fence():
 roll=get('iosxesoftwarerollout','catc-handoff-20261008-101-r7')
 up=get('iosxesoftwareupgrade',leaf)
 hand=get('catalystcenterswimhandoff','swim-97c267f5d40df669026ee57a1f0b5e52')
 lease=get('lease','cvk-device-18bf8e21debc4863-device-disruptive-mutation-27ab075b')
 assert roll['metadata']['uid']=='b3091df5-c00f-48c0-a1cd-6fb8f0baba49'
 assert roll['spec']['control']['pause'] and up['status']['managerControl']['pause']
 assert up['metadata']['uid']=='4960d563-e07f-4b5e-a6e3-ffcd013d454d'
 assert hand['status']['phase']=='CheckingReadiness' and not hand['status'].get('activationTask')
 assert not up['status'].get('primarySupervisorActivationRequested')
 assert lease['spec']['holderIdentity']=='software-upgrade/'+up['metadata']['uid']
 renewed=datetime.datetime.fromisoformat(lease['spec']['renewTime'].replace('Z','+00:00'))
 assert (datetime.datetime.now(datetime.timezone.utc)-renewed).total_seconds()<120
 receipt=get('iosxesoftwareupgrade','roadmap-drain-bed27185-101-cat9k-lab-101-ed048c9e')
 assert receipt['status']['phase']=='PreparedInvalidated'
 assert receipt['status']['preparedReceipt']['sourceDigest']=='sha256:c210d89b0bcbdeea4962b87b5f159c331988fe5a85d07a5a30da0438b2d99355'
 return lease['metadata']['uid']
backup=root/'retired-17.18.02.bin'
h256=hashlib.sha256();h512=hashlib.sha512()
with backup.open('rb') as f:
 for b in iter(lambda:f.read(1048576),b''):h256.update(b);h512.update(b)
assert backup.stat().st_size==1247897709
assert h256.hexdigest()=='c210d89b0bcbdeea4962b87b5f159c331988fe5a85d07a5a30da0438b2d99355'
assert not (root/'cleanup-dispatch.json').exists(), 'existing dispatch must be investigated, never replayed'
fence()
d=get('ciscodevice','cat9k-lab-101');assert d['metadata']['uid']=='20f3e68e-b127-4f2e-9a36-b66945135051'
s=get('secret',d['spec']['credentialSecretRef']['name'])['data']
username=base64.b64decode(s['username']).decode().strip() if 'username' in s else d['spec']['username']
password=base64.b64decode(s['password']).decode().strip()
p=pexpect.spawn('ssh',['-o','StrictHostKeyChecking=accept-new','-o','ConnectTimeout=15',username+'@'+d['spec']['address']],encoding='utf-8',timeout=300)
p.logfile=None
prompt=r'(?m)^[A-Za-z0-9_.-]+#\s*$'
def command(c):
 p.sendline(c);p.expect(prompt)
 out=p.before.replace('\r','')
 with (root/'cleanup-transcript.jsonl').open('a') as f:f.write(json.dumps({'command':c,'output':out})+'\n')
 return out
try:
 i=p.expect([r'(?i)password:',prompt,pexpect.EOF])
 if i==0:p.sendline(password);p.expect(prompt)
 else:assert i==1
 command('terminal length 0');command('terminal width 200')
 assert 'FOC2520L6H1' in command('show inventory')
 summary=command('show install summary')
 assert re.findall(r'^IMG\s+(\S+)\s+(\S+)',summary,re.M)==[('C','17.18.03.0.5496'),('I','17.18.04.0.759')]
 assert 'Auto abort timer: inactive' in summary
 boot=command('show boot');assert boot.count('BOOT variable = flash:packages.conf;')==2
 conf=command('more flash:packages.conf');assert '17.18.02' not in conf and '17.18.03' in conf
 assert 'No App found' in command('show app-hosting list')
 listing=command('dir '+path);assert '1247897709' in listing
 hashed=command('verify /sha512 '+path)
 assert h512.hexdigest() in hashed.lower(), 'fresh device hash differs from retained backup'
 leaseUID=fence()
 receipt={'operationUID':'4960d563-e07f-4b5e-a6e3-ffcd013d454d','leaseUID':leaseUID,'path':path,'size':1247897709,'sha256':h256.hexdigest(),'sha512':h512.hexdigest(),'backup':str(backup),'submittedAt':datetime.datetime.now(datetime.timezone.utc).isoformat(),'mode':'SupervisedLabCleanup'}
 with (root/'cleanup-dispatch.json').open('x') as f:json.dump(receipt,f);f.flush();os.fsync(f.fileno())
 out=command('delete /force '+path)
 assert '%Error' not in out and 'Invalid input' not in out
 absent=command('dir '+path);assert 'No such file' in absent
 after=command('dir flash:')
 m=re.search(r'\((\d+) bytes free\)',after);assert m and int(m.group(1))>=2620*1024*1024
 assert re.findall(r'^IMG\s+(\S+)\s+(\S+)',command('show install summary'),re.M)==[('C','17.18.03.0.5496'),('I','17.18.04.0.759')]
 receipt.update({'verifiedAt':datetime.datetime.now(datetime.timezone.utc).isoformat(),'freeBytes':int(m.group(1)),'result':'VerifiedRemoved'})
 (root/'cleanup-result.json').write_text(json.dumps(receipt,indent=2))
 print(json.dumps(receipt))
finally:p.close(force=True)
