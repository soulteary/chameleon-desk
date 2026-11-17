#!/usr/bin/env python3
"""Build native recovery companions; no downloads, device access or helper execution."""
import argparse
import hashlib
import json
import os
from pathlib import Path
import platform
import shutil
import shlex
import subprocess
import sys

TOOLS = ('nested', 'staticnested', 'darkside', 'mfkey32', 'mfkey32v2', 'mfkey64',
         'staticnested_1nt', 'staticnested_2x1nt_rf08s', 'staticnested_2x1nt_rf08s_1key',
         'mfulc_des_brute', 'hardnested')


def linux_static_links(cc, pkg_flags):
    extra, archives = [], []
    for flag in shlex.split(pkg_flags):
        if flag == '-lcrypto': continue
        if flag.startswith('-l') and flag[2:] not in ('dl','pthread','m','rt','c'):
            archive = Path(subprocess.check_output([cc,'-print-file-name='+(flag[3:] if flag.startswith('-l:') else 'lib'+flag[2:]+'.a')],text=True).strip()).resolve()
            if not archive.is_file(): raise ValueError('missing static dependency: '+flag)
            archives.append(archive)
            extra.append(str(archive))
        else: extra.append(flag)
    return extra, archives


def build(root, output, revision, cc):
    system = {'Linux': 'linux', 'Darwin': 'darwin', 'Windows': 'windows'}[platform.system()]
    arch = {'x86_64': 'amd64', 'AMD64': 'amd64', 'arm64': 'arm64', 'aarch64': 'arm64'}[platform.machine()]
    output.mkdir(parents=True, exist_ok=False)
    folder = output/'bin'; folder.mkdir()
    src = Path('third_party/chameleon/src')
    base = [str(src/name) for name in ('common.c','crapto1.c','crypto1.c','bucketsort.c','parity.c','mfkey.c','nested_util.c')]
    flags = ['-O2', '-D_GNU_SOURCE', '-I'+str(src), '-pthread']
    if system == 'windows': flags += ['-static', '-Wl,--no-insert-timestamp']
    def compile_tool(name, sources, extra=()):
        target = folder/(name+('.exe' if system == 'windows' else ''))
        subprocess.run([cc, *flags, *sources, *extra, '-o', str(target)], cwd=root, check=True)
        target.chmod(0o755)
    for name in TOOLS[:9]: compile_tool(name, [str(src/(name+'.c')), *base])
    # Explicit static archives avoid introducing OpenSSL/liblzma shared-loader
    # dependencies. OS C libraries remain part of the target platform baseline.
    linked_archives = []
    if system == 'darwin':
        crypto = Path(subprocess.check_output(['brew','--prefix','openssl@3'],text=True).strip())
        lzma = Path(subprocess.check_output(['brew','--prefix','xz'],text=True).strip())
        crypto_lib, lzma_lib = crypto/'lib/libcrypto.a', lzma/'lib/liblzma.a'
        includes = ['-I'+str(crypto/'include'), '-I'+str(lzma/'include')]
        notices = [crypto/'LICENSE.txt', *sorted(lzma.glob('COPYING*'))]
        crypto_extra = []
    else:
        crypto_lib = Path(subprocess.check_output([cc,'-print-file-name=libcrypto.a'],text=True).strip())
        lzma_lib = Path(subprocess.check_output([cc,'-print-file-name=liblzma.a'],text=True).strip())
        includes = []
        if system == 'windows':
            prefix = Path(shutil.which(cc) or cc).resolve().parent.parent
            notices = [p for package in ('openssl','xz','winpthreads','gcc-libs') for p in (prefix/'share/licenses'/package).rglob('*') if p.is_file()]
            crypto_extra = ['-lws2_32','-lcrypt32','-lbcrypt']
        else:
            notices = [Path('/usr/share/doc/libssl-dev/copyright'),Path('/usr/share/doc/liblzma-dev/copyright')]
            # Follow the installed OpenSSL static dependency closure instead
            # of assuming that libcrypto only needs libdl. Distro builds can
            # additionally require zlib, zstd or jitterentropy.
            crypto_extra, linked_archives = linux_static_links(cc, subprocess.check_output(['pkg-config','--static','--libs','libcrypto'],text=True))
            for archive in linked_archives:
                owner = subprocess.check_output(['dpkg-query','-S',str(archive)],text=True).split(': ')[0].split(':')[0]
                notice = Path('/usr/share/doc')/owner/'copyright'
                if not notice.is_file(): raise ValueError('missing dependency notice: '+owner)
                notices.append(notice)
            apache = Path('/usr/share/common-licenses/Apache-2.0')
            if apache.is_file(): notices.append(apache)
    if not crypto_lib.is_file() or not lzma_lib.is_file() or len(notices)<2:
        raise ValueError('static recovery libraries and their license notices are required')
    compile_tool('mfulc_des_brute', [str(src/'mfulc_des_brute.c')], [*includes,'-Wno-deprecated-declarations',str(crypto_lib),*crypto_extra])
    h = src/'HardnestedRecovery'
    hard = ['hardnested_main.c','pm3/ui.c','pm3/util.c','cmdhfmfhard.c','pm3/commonutil.c','pm3/util_posix.c',
            'hardnested/hardnested_bf_core.c','hardnested/hardnested_bruteforce.c','hardnested/hardnested_bitarray_core.c','hardnested/tables.c']
    hard_sources = base[:5]+[str(h/name) for name in hard]
    if system == 'windows': hard_sources.append(str(src/'compat/fmemopen/libfmemopen.c'))
    compile_tool('hardnested', hard_sources, [*includes,'-I'+str(h),'-I'+str(h/'pm3'),'-I'+str(h/'hardnested'),str(lzma_lib),'-lm'])
    # Include the exact patched worker sources and their build recipe.
    shutil.copytree(root/'third_party/chameleon',output/'source/third_party/chameleon')
    (output/'source/scripts').mkdir()
    (output/'source/tools/ble_bridge').mkdir(parents=True)
    shutil.copyfile(root/'scripts/build_recovery.py',output/'source/scripts/build_recovery.py')
    shutil.copyfile(root/'tools/ble_bridge/version.json',output/'source/tools/ble_bridge/version.json')
    licenses = output/'library-notices';licenses.mkdir()
    for i,path in enumerate(notices): shutil.copyfile(path,licenses/(str(i)+'-'+path.name))
    version = json.loads((root/'tools/ble_bridge/version.json').read_text())
    files = [{'name': name, 'sha256':hashlib.sha256((folder/(name+('.exe' if system=='windows' else ''))).read_bytes()).hexdigest()} for name in TOOLS]
    manifest = {'schema_version':1,'core_version':dict(version,revision=revision),'os':system,'arch':arch,'files':files}
    (folder/'workers.json').write_text(json.dumps(manifest,sort_keys=True,indent=2)+'\n',encoding='utf-8',newline='\n')
    metadata = {'compiler':subprocess.check_output([cc,'--version'],text=True).splitlines()[0],
                'static_libraries':{p.name:hashlib.sha256(p.read_bytes()).hexdigest() for p in (crypto_lib,lzma_lib,*linked_archives)}}
    (output/'build.json').write_text(json.dumps(metadata,sort_keys=True,indent=2)+'\n',encoding='utf-8',newline='\n')


if __name__ == '__main__':
    if sys.version_info < (3, 11): raise SystemExit('recovery builder requires Python 3.11 or later')
    parser=argparse.ArgumentParser(description=__doc__)
    parser.add_argument('--output',required=True)
    parser.add_argument('--revision',required=True)
    parser.add_argument('--cc',default=os.environ.get('CC','cc'))
    args=parser.parse_args()
    build(Path(__file__).resolve().parents[1],Path(args.output).resolve(),args.revision,args.cc)
