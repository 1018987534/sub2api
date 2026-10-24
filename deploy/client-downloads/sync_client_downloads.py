#!/usr/bin/env python3
"""Mirror original official desktop installers, then atomically publish a manifest.

Python 3.9+ standard library only. Never installs or executes downloaded software.
"""
import argparse
import contextlib
import datetime as dt
import fcntl
import hashlib
import json
import os
from pathlib import Path
import re
import shutil
import sys
import tempfile
import urllib.parse
import urllib.request
import uuid
import zipfile

RELEASE_API = 'https://api.github.com/repos/farion1231/cc-switch/releases/latest'
CHATGPT = {
    'chatgpt-macos': 'https://persistent.oaistatic.com/codex-app-prod/Codex.dmg',
    'chatgpt-windows-x64': 'https://persistent.oaistatic.com/codex-app-prod/ChatGPT-x64.msix',
    'chatgpt-windows-arm64': 'https://persistent.oaistatic.com/codex-app-prod/ChatGPT-arm64.msix',
}
HEADERS = {'User-Agent': 'xiaohondou-client-mirror/1.0', 'Accept': 'application/vnd.github+json'}


def request(url, method='GET'):
    parsed = urllib.parse.urlparse(url)
    if parsed.scheme != 'https' or parsed.hostname not in {'api.github.com', 'github.com', 'persistent.oaistatic.com'}:
        raise ValueError('Unexpected upstream URL')
    if parsed.hostname == 'github.com' and not parsed.path.startswith('/farion1231/cc-switch/releases/download/'):
        raise ValueError('Unexpected GitHub asset path')
    return urllib.request.urlopen(urllib.request.Request(url, method=method, headers=HEADERS), timeout=90)


def sha256(file):
    digest = hashlib.sha256()
    with open(file, 'rb') as stream:
        for chunk in iter(lambda: stream.read(1024 * 1024), b''):
            digest.update(chunk)
    return digest.hexdigest()


def validate_package(file, suffix):
    with open(file, 'rb') as stream:
        magic = stream.read(8)
        if suffix == '.msi' and magic != bytes.fromhex('d0cf11e0a1b11ae1'):
            raise ValueError('Not a Windows MSI installer')
        if suffix == '.dmg':
            stream.seek(-512, os.SEEK_END)
            if stream.read(4) != b'koly':
                raise ValueError('Not a UDIF DMG installer')
    if suffix == '.msix':
        with zipfile.ZipFile(file) as package:
            names = set(package.namelist())
            if not {'AppxManifest.xml', 'AppxSignature.p7x', '[Content_Types].xml'} <= names:
                raise ValueError('Missing signed MSIX package structures')


def make_plan(ccswitch_only=False):
    with request(RELEASE_API) as response:
        release = json.load(response)
    version = release.get('tag_name', '')
    if release.get('draft') or release.get('prerelease') or not re.fullmatch(r'v\d+\.\d+\.\d+[\w.-]*', version):
        raise ValueError('Latest stable CC Switch release is invalid')
    wanted = {
        'ccswitch-macos': f'CC-Switch-{version}-macOS.dmg',
        'ccswitch-windows-x64': f'CC-Switch-{version}-Windows.msi',
        'ccswitch-windows-arm64': f'CC-Switch-{version}-Windows-arm64.msi',
    }
    assets = {asset['name']: asset for asset in release['assets']}
    plan = {}
    for key, name in wanted.items():
        asset = assets.get(name)
        if not asset or not re.fullmatch(r'sha256:[a-f0-9]{64}', asset.get('digest') or ''):
            raise ValueError(f'Missing installer or upstream SHA-256: {name}')
        plan[key] = {'name': name, 'source': asset['browser_download_url'], 'bytes': asset['size'], 'sha256': asset['digest'][7:], 'integrity': 'github-release-digest'}
    if not ccswitch_only:
        for key, url in CHATGPT.items():
            with request(url, 'HEAD') as response:
                size = int(response.headers.get('Content-Length', '0'))
                if not 1024 * 1024 <= size <= 4 * 1024 ** 3:
                    raise ValueError(f'Unexpected upstream size for {key}: {size}')
                plan[key] = {'name': url.rsplit('/', 1)[1], 'source': url, 'bytes': size, 'etag': response.headers.get('ETag', ''), 'integrity': 'official-https-and-package-structure'}
    return version, plan


def reuse_file(root, previous, spec):
    if not previous or previous.get('source') != spec['source'] or previous.get('bytes') != spec['bytes']:
        return None
    if spec.get('sha256') and spec['sha256'] != previous.get('sha256'):
        return None
    if spec.get('etag') and spec['etag'] != previous.get('etag'):
        return None
    url = previous.get('url', '')
    if not re.fullmatch(r'/downloads/clients/releases/[A-Za-z0-9._-]+/[A-Za-z0-9._-]+', url) or '..' in url:
        return None
    file = root / url.removeprefix('/downloads/clients/')
    if file.is_symlink() or not file.is_file() or file.stat().st_size != spec['bytes']:
        return None
    if sha256(file) != previous.get('sha256'):
        return None
    return file


def download(spec, destination):
    digest = hashlib.sha256()
    total = 0
    with request(spec['source']) as response, open(destination, 'xb') as output:
        for chunk in iter(lambda: response.read(1024 * 1024), b''):
            total += len(chunk)
            if total > spec['bytes']:
                raise ValueError('Download exceeds expected Content-Length')
            output.write(chunk)
            digest.update(chunk)
        output.flush()
        os.fsync(output.fileno())
    if total != spec['bytes']:
        raise ValueError('Incomplete download')
    observed = digest.hexdigest()
    if spec.get('sha256') and observed != spec['sha256']:
        raise ValueError('Upstream SHA-256 mismatch')
    validate_package(destination, destination.suffix)
    return observed


def publish(root, version, plan):
    root.mkdir(parents=True, exist_ok=True, mode=0o755)
    (root / 'releases').mkdir(exist_ok=True, mode=0o755)
    with open(root / '.mirror.lock', 'a') as lock:
        try:
            fcntl.flock(lock, fcntl.LOCK_EX | fcntl.LOCK_NB)
        except BlockingIOError:
            print('Another mirror sync is active; keeping current manifest.')
            return
        try:
            previous = json.loads((root / 'manifest.json').read_text())
        except (FileNotFoundError, ValueError):
            previous = {}
        reusable = {key: reuse_file(root, previous.get('files', {}).get(key), spec) for key, spec in plan.items()}
        needed = sum(spec['bytes'] for key, spec in plan.items() if reusable[key] is None)
        if shutil.disk_usage(root).free < needed + 256 * 1024 * 1024:
            raise OSError('Insufficient free space for atomic installer sync')
        if set(previous.get('files', {})) == set(plan) and all(reusable.values()) and previous.get('ccswitch_version') == version:
            previous['checked_at'] = dt.datetime.now(dt.timezone.utc).isoformat()
            atomic_json(root / 'manifest.json', previous)
            print(f'UNCHANGED {version}; {len(plan)} verified installers retained')
            return
        release_id = dt.datetime.now(dt.timezone.utc).strftime('%Y%m%dT%H%M%SZ-') + uuid.uuid4().hex[:10]
        staging = Path(tempfile.mkdtemp(prefix='.pending-', dir=root))
        try:
            files = {}
            for key, spec in plan.items():
                destination = staging / spec['name']
                if reusable[key]:
                    os.link(reusable[key], destination)
                    observed = previous['files'][key]['sha256']
                else:
                    print(f'DOWNLOAD {key} ({spec["bytes"]} bytes)', flush=True)
                    observed = download(spec, destination)
                destination.chmod(0o644)
                files[key] = {**spec, 'sha256': observed, 'url': f'/downloads/clients/releases/{release_id}/{spec["name"]}'}
            manifest = {'schema': 1, 'updated_at': dt.datetime.now(dt.timezone.utc).isoformat(), 'ccswitch_version': version, 'files': files}
            # A crash before this point leaves the last published manifest untouched.
            atomic_json(staging / 'manifest.json', manifest)
            staging.chmod(0o755)
            os.replace(staging, root / 'releases' / release_id)
            atomic_json(root / 'manifest.json', manifest)
            print(f'PUBLISHED {version}; {len(files)} installers; manifest={root / "manifest.json"}')
            releases = sorted((folder for folder in (root / 'releases').iterdir()
                               if folder.is_dir() and not folder.is_symlink() and
                               re.fullmatch(r'\d{8}T\d{6}Z-[a-f0-9]{10}', folder.name)),
                              key=lambda folder: folder.name, reverse=True)
            for old in releases[3:]:
                if old.name != release_id:
                    shutil.rmtree(old)
        finally:
            if staging.exists():
                shutil.rmtree(staging)


def atomic_json(destination, data):
    temporary = destination.with_name('.manifest-' + uuid.uuid4().hex + '.json')
    try:
        with open(temporary, 'x', encoding='utf-8') as output:
            json.dump(data, output, indent=2, ensure_ascii=False)
            output.write('\n')
            output.flush()
            os.fsync(output.fileno())
        temporary.chmod(0o644)
        os.replace(temporary, destination)
    finally:
        with contextlib.suppress(FileNotFoundError):
            temporary.unlink()


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument('--root', type=Path, default=Path('/srv/sub2api-downloads/clients'))
    parser.add_argument('--plan', action='store_true', help='Resolve official URLs, size and digest without downloading or writing')
    parser.add_argument('--ccswitch-only', action='store_true', help='Sync only the three smaller CC Switch installers')
    args = parser.parse_args()
    version, plan = make_plan(args.ccswitch_only)
    if args.plan:
        print(json.dumps({'ccswitch_version': version, 'total_bytes': sum(item['bytes'] for item in plan.values()), 'files': plan}, indent=2))
    else:
        publish(args.root.resolve(), version, plan)


if __name__ == '__main__':
    try:
        main()
    except Exception as error:
        print(f'SYNC FAILED; previous published manifest retained: {type(error).__name__}: {error}', file=sys.stderr)
        sys.exit(1)
