// Package buildinfo 保存构建期注入的版本信息，供 details 子命令确认现场部署的是哪一次构建。
//
// 所有字段由 Makefile 通过 -ldflags -X 注入，字段命名与 emu-go 保持一致；
// 直接 go build 时为空，显示为 dev / unknown。
package buildinfo

import "runtime"

var (
	Version         string
	BuildTime       string
	GitCommitSha    string
	GitBranch       string
	GitCommitAuthor string
	BuildBy         string
)

// Info 是一次构建的完整标识。
type Info struct {
	Version         string
	BuildTime       string
	GitCommitSha    string
	GitBranch       string
	GitCommitAuthor string
	BuildBy         string
	GoVersion       string
	Platform        string
}

// Get 返回构建标识，未注入的字段填占位值，避免现场看到空行误以为输出截断。
func Get() Info {
	return Info{
		Version:         orDefault(Version, "dev"),
		BuildTime:       orDefault(BuildTime, "unknown"),
		GitCommitSha:    orDefault(GitCommitSha, "unknown"),
		GitBranch:       orDefault(GitBranch, "unknown"),
		GitCommitAuthor: orDefault(GitCommitAuthor, "unknown"),
		BuildBy:         orDefault(BuildBy, "unknown"),
		GoVersion:       runtime.Version(),
		Platform:        runtime.GOOS + "/" + runtime.GOARCH,
	}
}

func orDefault(value, fallback string) string {
	if value == "" {
		return fallback
	}
	return value
}
