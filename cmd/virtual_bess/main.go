// virtual_bess 命令行入口：默认启动仿真服务，另提供 details 子命令查看部署版本与生效配置。
package main

import (
	"fmt"
	"os"
	"strings"
)

func main() {
	args := os.Args[1:]
	// 第一个参数不以 '-' 开头时按子命令解析，其余情况保持原有「直接带 flag 启动」的行为。
	if len(args) > 0 && !strings.HasPrefix(args[0], "-") {
		switch args[0] {
		case "details":
			os.Exit(runDetails(args[1:]))
		case "run":
			os.Exit(run(args[1:]))
		case "help":
			usage(os.Stdout)
			os.Exit(0)
		default:
			fmt.Fprintf(os.Stderr, "unknown command %q\n\n", args[0])
			usage(os.Stderr)
			os.Exit(2)
		}
	}
	os.Exit(run(args))
}

func usage(w *os.File) {
	fmt.Fprint(w, `usage: virtual_bess [command] [flags]

commands:
  run       start the simulator (default when no command is given)
  details   show build details, then exit
  help      show this message

flags (run):
  -config   path to config file (optional)
  -port     modbus TCP port, overrides config
`)
}
