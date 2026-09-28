module sub2api/deepseek2api

go 1.26

replace sub2api/builtinlogin => ../builtinlogin

require (
	github.com/tetratelabs/wazero v1.9.0
	sub2api/builtinlogin v0.0.0-00010101000000-000000000000
)

require (
	github.com/dlclark/regexp2/v2 v2.1.0 // indirect
	github.com/tiktoken-go/tokenizer v0.8.0 // indirect
)
