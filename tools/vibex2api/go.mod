module sub2api/vibex2api

go 1.26

require (
	github.com/coder/websocket v1.8.14
	github.com/santhosh-tekuri/jsonschema/v6 v6.0.2
	golang.org/x/image v0.37.0
	sub2api/builtinlogin v0.0.0
)

require golang.org/x/text v0.35.0 // indirect

replace sub2api/builtinlogin => ../builtinlogin
