import argparse
import json
from pathlib import Path
import struct
import subprocess
import tempfile

ELF_HEADER_SIZE = 64
ELF_MACHINE_OFFSET = 18
ELF_PROGRAM_OFFSET = 32
ELF_PROGRAM_ENTRY_SIZE_OFFSET = 54
ELF_PROGRAM_COUNT_OFFSET = 56
ELF_INTERPRETER = 3
MACHINES = {'amd64': 62, 'arm64': 183}


def validate_binary(data, architecture):
    if len(data) < ELF_HEADER_SIZE or data[:6] != b'\x7fELF\x02\x01':
        raise ValueError('entrypoint must be a little-endian ELF64 binary')
    machine, = struct.unpack_from('<H', data, ELF_MACHINE_OFFSET)
    if machine != MACHINES[architecture]:
        raise ValueError(f'entrypoint machine {machine} does not match {architecture}')
    offset, = struct.unpack_from('<Q', data, ELF_PROGRAM_OFFSET)
    size, count = struct.unpack_from('<HH', data, ELF_PROGRAM_ENTRY_SIZE_OFFSET)
    if not size or offset + size * count > len(data):
        raise ValueError('invalid ELF program headers')
    for index in range(count):
        kind, = struct.unpack_from('<I', data, offset + index * size)
        if kind == ELF_INTERPRETER:
            raise ValueError('platform entrypoint must be statically linked')


def resolve(image, platform, manifest):
    operating_system, architecture = platform.split('/')
    matches = [item['digest'] for item in manifest.get('manifests', [])
               if item.get('platform', {}).get('os') == operating_system
               and item.get('platform', {}).get('architecture') == architecture]
    if len(matches) != 1:
        raise ValueError(f'image index must contain exactly one {platform} image')
    return image.split('@')[0] + '@' + matches[0]


def run(*args):
    return subprocess.check_output(args, text=True).strip()


def verify_image(image, platform):
    operating_system, architecture = platform.split('/')
    if run('uname', '-m') not in {'amd64': ('x86_64',), 'arm64': ('aarch64', 'arm64')}[architecture]:
        raise ValueError('image verification requires a native architecture runner')
    info, = json.loads(run('docker', 'image', 'inspect', image))
    if (info['Os'], info['Architecture']) != (operating_system, architecture):
        raise ValueError('image configuration does not match requested platform')
    entrypoint = info['Config']['Entrypoint'][0]
    container = run('docker', 'create', image)
    try:
        with tempfile.TemporaryDirectory(prefix='loco-image-') as directory:
            binary = Path(directory) / 'entrypoint'
            run('docker', 'cp', container + ':' + entrypoint, str(binary))
            validate_binary(binary.read_bytes(), architecture)
    finally:
        run('docker', 'rm', container)
    print(f'{platform} image and static ELF entrypoint verified')


def main():
    parser = argparse.ArgumentParser()
    parser.add_argument('operation', choices=('resolve', 'image', 'binary'))
    parser.add_argument('image')
    parser.add_argument('platform', choices=('linux/amd64', 'linux/arm64'))
    parser.add_argument('index', nargs='?')
    args = parser.parse_args()
    if args.operation == 'resolve':
        print(resolve(args.image, args.platform, json.loads(Path(args.index).read_text())))
    elif args.operation == 'binary':
        validate_binary(Path(args.image).read_bytes(), args.platform.split('/')[1])
        print(f'{args.platform} static ELF binary verified')
    else:
        verify_image(args.image, args.platform)


if __name__ == '__main__':
    main()
