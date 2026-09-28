module sub2api/vibex2api

go 1.26

require (
	github.com/coder/websocket v1.8.14
	sub2api/builtinlogin v0.0.0
)

replace sub2api/builtinlogin => ../builtinlogin
