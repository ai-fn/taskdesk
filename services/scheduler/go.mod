module github.com/fsociety/todo/services/scheduler

go 1.25.0

require (
	github.com/fsociety/todo/pkg v0.0.0
	github.com/fsociety/todo/proto/gen v0.0.0
	google.golang.org/grpc v1.81.0
)

require (
	golang.org/x/net v0.52.0 // indirect
	golang.org/x/sys v0.43.0 // indirect
	golang.org/x/text v0.36.0 // indirect
	google.golang.org/genproto/googleapis/rpc v0.0.0-20260226221140-a57be14db171 // indirect
	google.golang.org/protobuf v1.36.11 // indirect
)

replace (
	github.com/fsociety/todo/pkg => ../../pkg
	github.com/fsociety/todo/proto/gen => ../../proto/gen
)
