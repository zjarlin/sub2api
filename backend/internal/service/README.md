# Application Services

`account_recovery.go` selects temporary recovery candidates only after the normal
pool is exhausted. A closed scheduling switch always excludes an account, and
automatic recovery never enables that switch.

`reasoning_replay.go` preserves exact DeepSeek Responses reasoning content in
API-key-scoped cache references. Only an explicit missing-reasoning rejection
allows a bounded, observable non-thinking retry for unrecoverable old history.
