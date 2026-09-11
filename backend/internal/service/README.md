# Application Services

`account_recovery.go` selects temporary recovery candidates only after the normal
pool is exhausted. A closed scheduling switch always excludes an account, and
automatic recovery never enables that switch.
