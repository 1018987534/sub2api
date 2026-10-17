#!/usr/bin/env python3
"""Only the two monitoring A records. All other DNS and Worker bindings stay intact."""
import json
import os
import subprocess
import sys
import urllib.parse
import urllib.request
from pathlib import Path

DOMAINS = {'check.nideyiyi.com': 'nideyiyi.com', 'check.xiaofanqie.org': 'xiaofanqie.org'}
SOURCE = '95.169.18.157'
TARGET = '179.255.105.184'
REMOTE_STATE = '/run/bwg-dmit-migration/monitor-dns.before.json'
SCRIPT = Path(__file__).with_name('remote.py')


def ssh(*command, data=None, check=True):
    args = ['ssh', '-i', os.environ.get('MIGRATION_SSH_KEY', str(Path.home()/'.ssh/id_ed25519')),
            '-o', 'BatchMode=yes', '-o', 'IdentitiesOnly=yes', '-o', 'ConnectTimeout=12', 'dmit-us-01'] + list(command)
    p = subprocess.run(args, input=data, stdout=subprocess.PIPE, stderr=subprocess.PIPE)
    if check and p.returncode:
        raise RuntimeError('remote DNS state unavailable (exit %d)' % p.returncode)
    return p


def token():
    value = os.environ.get('CLOUDFLARE_API_TOKEN')
    if not value:
        # This is the declared production credential path; no browser stores used.
        value = Path(os.environ.get('CF_TOKEN_FILE', '~/.codex/secrets/cloudflare_nideyiyi_api_token')).expanduser().read_text().strip()
    if not value:
        raise RuntimeError('Cloudflare token missing')
    return value


def api(path, body=None, method='GET'):
    request = urllib.request.Request('https://api.cloudflare.com/client/v4'+path,
        data=None if body is None else json.dumps(body).encode(), method=method,
        headers={'Authorization': 'Bearer '+token(), 'Content-Type': 'application/json'})
    with urllib.request.urlopen(request, timeout=25) as response:
        result = json.load(response)
    if not result.get('success'):
        raise RuntimeError('Cloudflare API failed; error codes='+','.join(str(x.get('code')) for x in result.get('errors', [])))
    return result['result']


def records():
    result=[]
    for domain,zone in DOMAINS.items():
        zones=api('/zones?'+urllib.parse.urlencode({'name':zone}))
        if len(zones)!=1: raise RuntimeError('zone missing/ambiguous: '+zone)
        zoneid=zones[0]['id']
        values=api('/zones/'+zoneid+'/dns_records?'+urllib.parse.urlencode({'name':domain}))
        if len(values)!=1 or values[0]['type']!='A':
            raise RuntimeError('expected one A record, no AAAA/CNAME for '+domain)
        value=values[0]
        result.append({'zone_id':zoneid,'record':{k:value[k] for k in ('id','type','name','content','ttl','proxied','comment','tags') if k in value}})
    return result


def patch(item, content):
    old=item['record']
    # PATCH preserves comments, tags, TTL, proxy and other record metadata.
    result=api('/zones/'+item['zone_id']+'/dns_records/'+old['id'], {'content':content}, 'PATCH')
    if result['content']!=content: raise RuntimeError('DNS readback mismatch')


def main():
    action=sys.argv[1] if len(sys.argv)>1 else 'plan'
    if action=='plan':
        values=records()
        if any(x['record']['content'] not in (SOURCE,TARGET) for x in values):
            raise RuntimeError('monitor DNS points at an unrecognized origin; review before writes')
        print(json.dumps(values,sort_keys=True,indent=2))
    elif action=='apply':
        values=records()
        if any(x['record']['content']!=SOURCE for x in values):
            raise RuntimeError('monitor DNS must still match BWG immediately before apply')
        # Persist BEFORE the first PATCH, so partial publish can be recovered.
        ssh('test ! -e '+REMOTE_STATE)
        ssh('umask 077; cat > '+REMOTE_STATE,data=json.dumps(values).encode())
        for item in values: patch(item,TARGET)
        print('monitor-DNS=DMIT')
    elif action=='verify':
        values=records()
        if any(x['record']['content']!=TARGET for x in values): raise RuntimeError('monitor DNS not fully on DMIT')
        print(json.dumps(values,sort_keys=True,indent=2))
    elif action=='rollback':
        saved=ssh('cat '+REMOTE_STATE,check=False)
        if saved.returncode:
            print('monitor-DNS=never-published; unchanged')
            return
        current={x['record']['id']:x for x in records()}
        for item in json.loads(saved.stdout):
            live=current.get(item['record']['id'])
            if live is None or live['record']['content'] not in (SOURCE,TARGET):
                raise RuntimeError('DNS changed externally after publish; refusing to overwrite it')
            patch(item,item['record']['content'])
        print('monitor-DNS=restored')
    else: raise RuntimeError('unknown DNS action')


if __name__=='__main__':
    try: main()
    except Exception as exc:
        print('ERROR: '+str(exc),file=sys.stderr)
        sys.exit(1)
