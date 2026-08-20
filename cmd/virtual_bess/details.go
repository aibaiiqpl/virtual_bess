package main

import (
	"fmt"
	"io"
	"os"

	"virtual_bess/internal/buildinfo"
	iec61850sim "virtual_bess/internal/protocol/iec61850"
)

// runDetails 打印当前二进制的构建标识，用于确认现场部署的是哪一次构建。
// 不接受参数、不读配置、不启动任何监听。
func runDetails(args []string) int {
	if len(args) > 0 {
		fmt.Fprintf(os.Stderr, "details takes no arguments, got %q\n", args[0])
		return 2
	}
	printDetails(os.Stdout)
	return 0
}

func printDetails(w io.Writer) {
	info := buildinfo.Get()
	// Variant 区分是否编译进 IEC 61850：两种变体外观相同，现场只能靠这行分辨。
	variant := "simple"
	if iec61850sim.Supported {
		variant = "iec61850"
	}
	fmt.Fprintf(w, "Version:\t%s\nBuildTime:\t%s\nGitCommitSha:\t%s\nGitBranch:\t%s\nGitAuthor:\t%s\nBuildBy:\t%s\nGoVersion:\t%s\nPlatform:\t%s\nVariant:\t%s\n",
		info.Version, info.BuildTime, info.GitCommitSha, info.GitBranch,
		info.GitCommitAuthor, info.BuildBy, info.GoVersion, info.Platform, variant)
}
