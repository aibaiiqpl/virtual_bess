// Package buildinfo 提供构建期版本信息，供 details 子命令确认现场部署的是哪一版。
//
// Version / Commit / BuildTime 由 Makefile 通过 -ldflags -X 注入；
// 直接 go build 时这些变量为空，退回读取 Go 自带的 VCS 戳。
package buildinfo

import (
	"runtime"
	"runtime/debug"
)

var (
	Version   = ""
	Commit    = ""
	BuildTime = ""
)

const unknown = "unknown"

// Info 是一次构建的完整标识。
type Info struct {
	Version   string // 语义版本或 git describe 结果
	Commit    string // git 提交号
	Dirty     bool   // 构建时工作区是否有未提交改动
	BuildTime string // 构建时间（UTC）
	GoVersion string
	Platform  string // GOOS/GOARCH
}

// Get 汇总构建信息；未经 ldflags 注入时整体回落到 Go 自带的 VCS 戳。
func Get() Info {
	info := Info{
		Version:   Version,
		Commit:    Commit,
		BuildTime: BuildTime,
		GoVersion: runtime.Version(),
		Platform:  runtime.GOOS + "/" + runtime.GOARCH,
	}
	// 只有完全没注入版本信息时才读 VCS：本仓库以 submodule 形式嵌在工作区里时，
	// Go 会向上取到父仓库的 revision 和 dirty 状态，用它覆盖注入值会把干净构建误标为脏。
	if info.Commit == "" {
		fillFromVCS(&info)
	}
	if info.Version == "" {
		info.Version = "dev"
	}
	if info.Commit == "" {
		info.Commit = unknown
	}
	if info.BuildTime == "" {
		info.BuildTime = unknown
	}
	return info
}

// fillFromVCS 用 go build 自动嵌入的 VCS 信息填充构建标识，仅在未注入版本时调用。
// 注意：仓库作为 submodule 使用时，Go 记录的可能是父仓库的提交，只能作为兜底参考。
func fillFromVCS(info *Info) {
	bi, ok := debug.ReadBuildInfo()
	if !ok {
		return
	}
	for _, s := range bi.Settings {
		switch s.Key {
		case "vcs.revision":
			if info.Commit == "" {
				info.Commit = s.Value
			}
		case "vcs.time":
			if info.BuildTime == "" {
				info.BuildTime = s.Value
			}
		case "vcs.modified":
			if s.Value == "true" {
				info.Dirty = true
			}
		}
	}
}
