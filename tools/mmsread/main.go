// 诊断工具：读离散告警点 stVal、转储 DO 的 FC=ST 目录顺序，定位结构体定位错位。
package main

import (
	"fmt"
	"os"
	"strconv"

	"github.com/go-bindings/iec61850"
)

func main() {
	if len(os.Args) < 3 {
		fmt.Println("usage: mmsread <host:port> <objectRef|dir:DOref> [...]")
		os.Exit(2)
	}
	host := os.Args[1]
	port := 102
	if h, p, ok := splitHostPort(host); ok {
		host, port = h, p
	}
	st := iec61850.NewSettings()
	st.Host = host
	st.Port = port
	c, err := iec61850.NewClient(st)
	if err != nil {
		fmt.Printf("connect %s:%d failed: %v\n", host, port, err)
		os.Exit(1)
	}
	defer c.Close()
	for _, ref := range os.Args[2:] {
		if len(ref) > 4 && ref[:4] == "dir:" {
			do := ref[4:]
			names, err := c.GetDataDirectoryByFC(do, iec61850.ST)
			if err != nil {
				fmt.Printf("dir %-40s ERR %v\n", do, err)
				continue
			}
			fmt.Printf("dir %-40s ST-order=%v\n", do, names)
			continue
		}
		v, err := c.ReadBool(ref, iec61850.ST)
		if err != nil {
			fmt.Printf("%-45s ERR %v\n", ref, err)
			continue
		}
		fmt.Printf("%-45s stVal=%v\n", ref, v)
	}
}

func splitHostPort(s string) (string, int, bool) {
	for i := len(s) - 1; i >= 0; i-- {
		if s[i] == ':' {
			p, err := strconv.Atoi(s[i+1:])
			if err != nil {
				return "", 0, false
			}
			return s[:i], p, true
		}
	}
	return "", 0, false
}
