module virtual_bess

go 1.26

require (
	aiwatt.net/ems/go-common v1.1.0
	github.com/go-bindings/iec61850 v1.0.0
	gopkg.in/yaml.v3 v3.0.1
)

require (
	github.com/goburrow/serial v0.1.0 // indirect
	github.com/spf13/cast v1.10.0 // indirect
	go.uber.org/multierr v1.10.0 // indirect
	go.uber.org/zap v1.27.1 // indirect
	gopkg.in/natefinch/lumberjack.v2 v2.2.1 // indirect
)

replace aiwatt.net/ems/go-common v1.1.0 => cnb.cool/aiwatt/ems/go-common v1.1.0

// 本地副本补上 UpdateBooleanAttributeValue（上游 v1.0.0 未包装布尔属性更新），
// 用于在 61850 服务端置位离散告警点 AlmN.stVal。详见 third_party/go-bindings-iec61850/server_bool_patch.go
replace github.com/go-bindings/iec61850 v1.0.0 => ./third_party/go-bindings-iec61850
