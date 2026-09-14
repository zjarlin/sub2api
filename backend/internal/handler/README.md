# HTTP Request Handlers

`model_fallback.go` owns the bounded GPT text-model downgrade policy shared by
Responses and Chat Completions. Account retries run before model downgrades;
stateful response IDs and responses with semantic output cannot be replayed.

`concurrency_retry.go` waits for busy upstreams only after other candidates are
exhausted, reopening only capacity exclusions for at most three two-second rounds.
