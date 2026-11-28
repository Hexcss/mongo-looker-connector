#!/usr/bin/env python3
"""Initialize or restart a private, MongoDB-backed gateway and console."""

import argparse
import base64
import fcntl
import hashlib
from email.errors import HeaderParseError
from email.headerregistry import Address
import os
from pathlib import Path
import secrets
import subprocess
import tempfile
import time
import urllib.error
import urllib.request

HERE = Path(__file__).resolve().parent


def secret_file(path, value):
    with path.open('x', encoding='utf-8') as out:
        out.write(value + '\n')
    path.chmod(0o400)


def initialize(state, owner):
    with tempfile.TemporaryDirectory(dir=state.parent) as tmp:
        prepared = Path(tmp) / 'installation'
        prepared.mkdir(mode=0o700)
        control = secrets.token_urlsafe(32)
        reporter = secrets.token_urlsafe(32)
        values = {
            'root-password': secrets.token_urlsafe(32),
            'control-password': control,
            'reporter-password': reporter,
            'control-uri': f'mongodb://gateway:{control}@mongo:27017/mlc_gateway',
            'reporter-uri': f'mongodb://reporter:{reporter}@mongo:27017/analytics',
            'encryption-key': base64.b64encode(secrets.token_bytes(32)).decode('ascii'),
            'owner-email': owner,
            'initial-owner-password': secrets.token_urlsafe(32),
        }
        for name, value in values.items():
            secret_file(prepared / name, value)
        prepared.rename(state)


def wait_ready(url):
    for _ in range(60):
        try:
            with urllib.request.urlopen(url, timeout=2) as response:
                if response.status == 200:
                    return
        except (OSError, urllib.error.URLError):
            pass
        time.sleep(1)
    raise SystemExit('The service did not become ready. Inspect the Compose service status.')


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument('--owner', help='Email address for the first console owner')
    parser.add_argument('--state-dir', type=Path, default=Path.home() / '.local/share/mongo-looker-connector')
    parser.add_argument('--gateway-port', type=int, default=8092)
    parser.add_argument('--console-port', type=int, default=8091)
    args = parser.parse_args()
    state = args.state_dir.expanduser().absolute()
    if state.resolve().is_relative_to(HERE.parents[1]):
        parser.error('choose a private state directory outside the checkout')
    if args.gateway_port == args.console_port or not all(1 <= p <= 65535 for p in [args.console_port, args.gateway_port]):
        parser.error('choose two different TCP ports')
    os.umask(0o077)
    state.parent.mkdir(mode=0o700, parents=True, exist_ok=True)
    project = 'mlc-' + hashlib.sha256(os.fsencode(state)).hexdigest()[:12]
    env = dict(os.environ, MLC_STATE_DIR=str(state), MLC_INSTALL_UID=str(os.getuid()),
               MLC_INSTALL_GID=str(os.getgid()), MLC_GATEWAY_PORT=str(args.gateway_port),
               MLC_CONSOLE_PORT=str(args.console_port))

    def compose(*arguments):
        cmd = ['docker', 'compose', '-p', project, '-f', str(HERE / 'compose.yaml'), *arguments]
        subprocess.run(cmd, env=env, check=True)

    with (state.parent / ('.' + project + '.lock')).open('a') as lock:
        fcntl.flock(lock, fcntl.LOCK_EX)
        if not state.exists():
            try:
                owner = Address(addr_spec=args.owner or '')
                if not owner.username or not owner.domain or owner.addr_spec != args.owner or len(args.owner) > 254:
                    raise ValueError()
            except (HeaderParseError, ValueError):
                parser.error('--owner requires an email address on first initialization')
            initialize(state, args.owner)
        if state.is_symlink() or state.stat().st_uid != os.getuid() or state.stat().st_mode & 0o077:
            parser.error('state directory must be private and owned by the current user')
        if args.owner and (state / 'owner-email').read_text().strip().lower() != args.owner.strip().lower():
            parser.error('the installation already belongs to a different owner')

        compose('build', 'gateway')
        if not (state / 'initialized').exists():
            compose('up', '--wait', '-d', 'mongo')
            compose('run', '--rm', 'provision')
            compose('run', '--rm', 'init-owner')
            (state / 'initialized').touch(mode=0o600)
        compose('up', '-d', 'console', 'gateway')
        wait_ready(f'http://127.0.0.1:{args.gateway_port}/health')
        wait_ready(f'http://localhost:{args.console_port}/')
    print(f'Console http://localhost:{args.console_port}/')
    print(f'Compose project {project}')
    password_file = state / 'initial-owner-password'
    if password_file.exists():
        print(f'Initial password file {password_file}. Change it at first sign-in, then remove the file.')


if __name__ == '__main__':
    try:
        main()
    except subprocess.CalledProcessError as exc:
        raise SystemExit(exc.returncode) from None
