// Command foya 是内核入口:默认启动常驻内核 daemon,exec 子命令用于
// 无头一次性执行(供 CLI / 外部 harness 调用)。
//
// 脚手架阶段:daemon 启动 HTTP server,本地默认监听 Unix domain socket
// (私有目录 + 0600,靠 OS 权限做单用户信任);exec 为占位。
package main

import (
	"fmt"
	"net"
	"net/http"
	"os"
	"path/filepath"

	"github.com/freesoulcode/foya/internal/config"
	"github.com/freesoulcode/foya/internal/kernel"
	"github.com/freesoulcode/foya/internal/server"
)

func main() {
	if len(os.Args) > 1 && os.Args[1] == "exec" {
		runExec(os.Args[2:])
		return
	}
	runDaemon()
}

// runDaemon 启动常驻内核。
func runDaemon() {
	cfg := config.Default()
	app := kernel.New(cfg)
	srv := server.New(cfg, app.Backend())

	ln, desc, err := listen(cfg)
	if err != nil {
		fmt.Fprintln(os.Stderr, "kernel listen failed:", err)
		os.Exit(1)
	}
	fmt.Printf("foya kernel listening on %s\n", desc)

	if err := http.Serve(ln, srv.Handler()); err != nil {
		fmt.Fprintln(os.Stderr, "kernel exited:", err)
		os.Exit(1)
	}
}

// listen 按配置创建监听器。本地默认 Unix domain socket。
func listen(cfg config.Config) (net.Listener, string, error) {
	switch cfg.Transport {
	case config.TransportTCP:
		ln, err := net.Listen("tcp", cfg.Addr)
		return ln, cfg.Addr, err
	default: // TransportUnixSocket
		return listenUnix(cfg.SocketPath)
	}
}

// listenUnix 在私有目录下创建 Unix domain socket。
// 目录 0700、socket 0600,构成单用户信任边界。
func listenUnix(path string) (net.Listener, string, error) {
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return nil, "", fmt.Errorf("create socket dir: %w", err)
	}
	// 清理上次残留的 socket 文件(否则 bind 报 address already in use)。
	if err := os.Remove(path); err != nil && !os.IsNotExist(err) {
		return nil, "", fmt.Errorf("remove stale socket: %w", err)
	}
	ln, err := net.Listen("unix", path)
	if err != nil {
		return nil, "", fmt.Errorf("listen unix: %w", err)
	}
	if err := os.Chmod(path, 0o600); err != nil {
		_ = ln.Close()
		return nil, "", fmt.Errorf("chmod socket: %w", err)
	}
	return ln, "unix:" + path, nil
}

// runExec 无头执行一次性任务(占位)。
func runExec(args []string) {
	fmt.Println("foya exec: 尚未实现 (脚手架阶段)")
}
