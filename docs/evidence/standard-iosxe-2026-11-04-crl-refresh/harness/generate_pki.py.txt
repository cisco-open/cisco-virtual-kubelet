import pathlib,datetime,uuid,json,os,ipaddress,hashlib
from cryptography import x509
from cryptography.x509.oid import NameOID,ExtendedKeyUsageOID
from cryptography.hazmat.primitives import hashes,serialization
from cryptography.hazmat.primitives.asymmetric import rsa
R=pathlib.Path('/tmp/cvk-crlrefresh-20261104');now=datetime.datetime.now(datetime.timezone.utc);nonce=uuid.uuid4().hex[:12];certs={};keys={}
def write(n,b):p=R/n;p.write_bytes(b);os.chmod(p,0o600)
def key(n):
 k=rsa.generate_private_key(65537,2048);keys[n]=k;write(n+'.key',k.private_bytes(serialization.Encoding.PEM,serialization.PrivateFormat.PKCS8,serialization.NoEncryption()));return k
rn=x509.Name([x509.NameAttribute(NameOID.COMMON_NAME,'CVK CRL refresh root '+nonce)]);rk=key('root')
def cert(n,k,ca):
 b=x509.CertificateBuilder().subject_name(rn if ca else x509.Name([x509.NameAttribute(NameOID.COMMON_NAME,'192.168.129.111')])).issuer_name(rn).public_key(k.public_key()).serial_number(x509.random_serial_number()).not_valid_before(now-datetime.timedelta(days=1)).not_valid_after(now+datetime.timedelta(days=7)).add_extension(x509.BasicConstraints(ca=ca,path_length=0 if ca else None),True).add_extension(x509.SubjectKeyIdentifier.from_public_key(k.public_key()),False).add_extension(x509.AuthorityKeyIdentifier.from_issuer_public_key(rk.public_key()),False)
 if ca:b=b.add_extension(x509.KeyUsage(False,False,False,False,False,True,True,False,False),True)
 else:b=b.add_extension(x509.KeyUsage(True,False,True,False,False,False,False,False,False),True).add_extension(x509.ExtendedKeyUsage([ExtendedKeyUsageOID.SERVER_AUTH]),False).add_extension(x509.SubjectAlternativeName([x509.IPAddress(ipaddress.ip_address('192.168.129.111'))]),False).add_extension(x509.CRLDistributionPoints([x509.DistributionPoint(full_name=[x509.UniformResourceIdentifier('http://192.168.129.111:18470/issuer.crl')],relative_name=None,reasons=None,crl_issuer=None)]),False)
 c=b.sign(rk,hashes.SHA256());certs[n]=c;write(n+'.crt',c.public_bytes(serialization.Encoding.PEM))
cert('root',rk,True)
for n in ['original','replacement']:cert(n,key(n),False)
m={'generatedAtUTC':now.isoformat(),'nonce':nonce,'certificates':{},'ephemeralPrivateKeys':[n+'.key' for n in keys],'revocationTrustpoint':'CVK-CRLREFRESH-20261104','CDP':'http://192.168.129.111:18470/issuer.crl'}
for n,c in certs.items():m['certificates'][n]={'sha256':c.fingerprint(hashes.SHA256()).hex(),'SPKISHA256':hashlib.sha256(c.public_key().public_bytes(serialization.Encoding.DER,serialization.PublicFormat.SubjectPublicKeyInfo)).hexdigest(),'serialHex':format(c.serial_number,'x'),'subject':c.subject.rfc4514_string(),'issuer':c.issuer.rfc4514_string(),'validFromUTC':c.not_valid_before.replace(tzinfo=datetime.timezone.utc).isoformat(),'validUntilUTC':c.not_valid_after.replace(tzinfo=datetime.timezone.utc).isoformat()}
(R/'pki-manifest.json').write_text(json.dumps(m,indent=2)+'\n');(R/'server-mode.json').write_text(json.dumps({'certificate':'original','CRLversion':1}));print('PASS fresh root and two valid leaf certificates')
