module github.com/fsociety/todo/services/gateway

go 1.23

require (
	github.com/fsociety/todo/pkg v0.0.0
	github.com/fsociety/todo/proto/gen v0.0.0
	github.com/go-chi/chi/v5 v5.2.1
	google.golang.org/grpc v1.81.0
	google.golang.org/protobuf v1.36.11
)

require (
	golang.org/x/net v0.51.0 // indirect
	golang.org/x/sys v0.42.0 // indirect
	golang.org/x/text v0.34.0 // indirect
	google.golang.org/genproto/googleapis/rpc v0.0.0-20260226221140-a57be14db171 // indirect
)

replace (
	github.com/fsociety/todo/pkg => ../../pkg
	github.com/fsociety/todo/proto/gen => ../../proto/gen
)
