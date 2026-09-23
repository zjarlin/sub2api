"""Read the current user's Doubao session without exporting its credentials."""
import ctypes
import hashlib
import sqlite3
import subprocess
from pathlib import Path


def read_cookies(include_browser_state=False):
    password = subprocess.run(
        ['/usr/bin/security', 'find-generic-password', '-w', '-s', 'Doubao Safe Storage'],
        capture_output=True, timeout=15, check=True,
    ).stdout.rstrip(b'\n')
    key = hashlib.pbkdf2_hmac('sha1', password, b'saltysalt', 1003, 16)
    crypt = ctypes.CDLL('/usr/lib/system/libcommonCrypto.dylib').CCCrypt
    crypt.argtypes = [ctypes.c_uint, ctypes.c_uint, ctypes.c_uint,
                      ctypes.c_void_p, ctypes.c_size_t, ctypes.c_void_p,
                      ctypes.c_void_p, ctypes.c_size_t, ctypes.c_void_p,
                      ctypes.c_size_t, ctypes.POINTER(ctypes.c_size_t)]
    crypt.restype = ctypes.c_int
    database = Path.home() / 'Library/Application Support/Doubao/Default/Cookies'
    selected = {'sessionid', 'sessionid_ss', 'sid_tt', 'passport_csrf_token'}
    if include_browser_state:
        selected |= {'msToken', 's_v_web_id', 'tt_scid', 'ttwid', 'sid_guard',
                     'passport_csrf_token_default'}
    cookies = {}
    connection = sqlite3.connect(database.as_uri() + '?mode=ro', uri=True)
    try:
        for host, name, encrypted in connection.execute(
            "SELECT host_key, name, encrypted_value FROM cookies "
            "WHERE host_key IN ('.doubao.com', 'www.doubao.com') ORDER BY host_key"
        ):
            if name not in selected or not encrypted.startswith(b'v10'):
                continue
            encrypted = encrypted[3:]
            output = ctypes.create_string_buffer(len(encrypted))
            count = ctypes.c_size_t()
            status = crypt(1, 0, 1, key, len(key), b' ' * 16, encrypted,
                           len(encrypted), output, len(output), ctypes.byref(count))
            if status:
                raise RuntimeError('Cookie decryption failed')
            plaintext = output.raw[:count.value]
            if not plaintext.startswith(hashlib.sha256(host.encode()).digest()):
                raise RuntimeError('Cookie host binding did not match')
            cookies[name] = plaintext[32:].decode()
    finally:
        connection.close()
    if not cookies.get('sessionid'):
        raise RuntimeError('No usable Doubao desktop session')
    return cookies
