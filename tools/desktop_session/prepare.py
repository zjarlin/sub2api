"""Export this user's current desktop session to a private bind mount for Docker."""
import json
import os
import secrets
from pathlib import Path
from native_context import snapshot


def private_write(path, text):
    temporary = path.with_suffix(path.suffix + '.tmp')
    with os.fdopen(os.open(temporary, os.O_WRONLY | os.O_CREAT | os.O_TRUNC, 0o600), 'w') as output:
        output.write(text)
    os.replace(temporary, path)


if __name__ == '__main__':
    base = Path(__file__).resolve().parent
    directory = base / 'runtime'
    directory.mkdir(mode=0o700, exist_ok=True)
    os.chmod(directory, 0o700)
    state_directory = base / 'state'
    state_directory.mkdir(mode=0o700, exist_ok=True)
    os.chmod(state_directory, 0o700)
    context = snapshot()
    private_write(directory / 'session.json', json.dumps(context))
    key_file = directory / 'api_key'
    if not key_file.exists():
        private_write(key_file, secrets.token_urlsafe(36))
    private_write(base / '.env', '\n'.join([
        'DESKTOP_UID=' + str(os.getuid()), 'DESKTOP_GID=' + str(os.getgid()),
        'DESKTOP_SECRET_DIR=' + str(directory), 'DESKTOP_PORT=18089', '',
        'DESKTOP_STATE_DIR=' + str(state_directory), '',
    ]))
    print('Private session snapshot refreshed. Credentials are excluded from Git and Docker build context.')
