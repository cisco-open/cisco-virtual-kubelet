from pathlib import Path
import json
r=Path('/private/tmp/cvk-crlrefresh-20261104');old=Path('/private/tmp/cvk-stdrevoke-20261104');cold=old/'cold'
subs=[('/tmp/cvk-stdrevoke-20261104/cold','/tmp/cvk-crlrefresh-20261104'),('/tmp/cvk-stdrevoke-20261104','/tmp/cvk-crlrefresh-20261104'),('cvk-stdrevoke-k3s-20261103','cvk-crlrefresh-k3s-20261104'),('cvk-stdrevoke-k3s','cvk-crlrefresh-k3s'),('20261104-stdrevoke','20261104-crlrefresh'),('cvk-stdrevoke-20261104','cvk-crlrefresh-20261104'),('cvk-stdrevoke-apps-20261104','cvk-crlrefresh-apps-20261104'),('c9k-stdrevoke-20261104','c9k-crlrefresh-20261104'),('cvk-stdrevoke-fixture','cvk-crlrefresh-fixture'),('cvk-stdrevoke','cvk-crlrefresh'),('16455','16456'),('10.72.0.0','10.74.0.0'),('10.73.0.0','10.75.0.0')]
for n,p in [('bootstrap',old/'bootstrap.py'),('deploy',old/'deploy.py'),('device_cli',old/'device_cli.py'),('control_functions',cold/'control_functions.py'),('snapshot',cold/'snapshot.py')]:
 s=p.read_text()
 for a,b in subs:s=s.replace(a,b)
 (r/(n+'.py')).write_text(s)
x=json.loads((old/'candidate.json').read_text());x['physicalControl']='Standard natural CRL cache refresh after new leaf revocation publication';x['priorCandidateEvidence']='../standard-iosxe-2026-11-04-certificate-revocation/candidate.json';(r/'candidate.json').write_text(json.dumps(x,indent=2)+'\n');(r/'main.go').write_text((cold/'main.go').read_text().replace('cvk-revocation-cold-qualification','cvk-crl-refresh-qualification'))
