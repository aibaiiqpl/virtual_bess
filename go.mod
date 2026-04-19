module virtual_bess

go 1.24.0

require (
	aiwatt.net/ems/go-common v0.0.0
	gopkg.in/yaml.v3 v3.0.1
)

require (
	github.com/goburrow/serial v0.1.0 // indirect
	go.uber.org/multierr v1.10.0 // indirect
	go.uber.org/zap v1.27.1 // indirect
	gopkg.in/natefinch/lumberjack.v2 v2.2.1 // indirect
)

replace aiwatt.net/ems/go-common => /tmp/go-common
