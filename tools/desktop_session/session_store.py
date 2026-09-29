"""Bounded, private persistence shared by desktop protocol sessions."""
import json
import os
import time
from collections import OrderedDict
from pathlib import Path
import hashlib


def digest(value):
    raw = json.dumps(value, ensure_ascii=False, sort_keys=True, separators=(',', ':'))
    return hashlib.sha256(raw.encode()).hexdigest()


class SessionStore:
    def __init__(self, path=None, *, ttl=21600, max_entries=64, max_bytes=8 * 1024 * 1024, clock=time.time):
        self.path = Path(path) if path else None
        self.ttl, self.max_entries, self.max_bytes, self.clock = ttl, max_entries, max_bytes, clock
        self.entries, self.session = OrderedDict(), ''
        if self.path and self.path.exists():
            if self.path.stat().st_size > max_bytes:
                raise ValueError('Conversation state exceeded limit')
            saved = json.loads(self.path.read_text())
            if saved.get('version') != 1:
                raise ValueError('Unsupported conversation state version')
            self.session = saved['session']
            self.entries.update(saved['entries'])
        self.prune()

    def bind_session(self, session):
        identity = digest(session)
        if identity != self.session:
            self.entries.clear()
            self.session = identity
            self.persist()

    def prune(self):
        expired = [key for key, entry in self.entries.items() if self.clock() - entry['updated'] >= self.ttl]
        for key in expired:
            del self.entries[key]
        while len(self.entries) > self.max_entries:
            self.entries.popitem(last=False)

    def serialized(self):
        return json.dumps({'version': 1, 'session': self.session, 'entries': list(self.entries.items())},
                          ensure_ascii=False, separators=(',', ':')).encode()

    def persist(self):
        self.prune()
        raw = self.serialized()
        while len(raw) > self.max_bytes and self.entries:
            self.entries.popitem(last=False)
            raw = self.serialized()
        if self.path is None:
            return
        self.path.parent.mkdir(parents=True, exist_ok=True, mode=0o700)
        temporary = self.path.with_suffix('.tmp')
        descriptor = os.open(temporary, os.O_WRONLY | os.O_CREAT | os.O_TRUNC, 0o600)
        with os.fdopen(descriptor, 'wb') as output:
            output.write(raw)
            output.flush()
            os.fsync(output.fileno())
        os.replace(temporary, self.path)

