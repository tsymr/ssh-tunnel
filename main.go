package main

import (
	"flag"
	"fmt"
	"log"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"time"
)

var version = "dev"

// builtinPresets 是首次运行时自动导入的隧道模板（仅作默认值，用户可自由修改/删除）。
var builtinPresets = []Tunnel{
	{Label: "8088 正向转发", User: "ts", Host: "10.0.0.23", LocalPort: 8088, RemotePort: 8088, BindAddr: "127.0.0.1", ForwardMode: "local", AuthMethod: "key"},
	{Label: "18765 正向转发", User: "ts", Host: "10.0.0.23", LocalPort: 18765, RemotePort: 18765, BindAddr: "", ForwardMode: "local", AuthMethod: "key"},
	{Label: "9222 反向转发", User: "ts", Host: "10.0.0.23", LocalPort: 9222, RemotePort: 9222, BindAddr: "127.0.0.1", ForwardMode: "remote", AuthMethod: "key"},
}

func main() {
	port := flag.Int("port", 18787, "Web 监听端口")
	dataDir := flag.String("data-dir", defaultDataDir(), "数据目录")
	serve := flag.Bool("serve", false, "服务模式（不打开浏览器）")
	install := flag.Bool("install", false, "安装为 launchd 系统服务并启动")
	uninstall := flag.Bool("uninstall", false, "卸载 launchd 系统服务")
	showVersion := flag.Bool("version", false, "打印版本号")
	flag.Parse()

	if *showVersion {
		fmt.Println(version)
		return
	}
	if *uninstall {
		must(uninstallService())
		fmt.Println("已卸载系统服务")
		return
	}
	if *install {
		must(installService(*port, *dataDir))
		fmt.Printf("已安装并启动系统服务，Web 地址: http://127.0.0.1:%d\n", *port)
		return
	}

	if err := run(*port, *dataDir, *serve); err != nil {
		log.Fatal(err)
	}
}

func run(port int, dataDir string, serve bool) error {
	if err := os.MkdirAll(dataDir, 0o700); err != nil {
		return err
	}
	storePath := filepath.Join(dataDir, "tunnels.json")
	logDir := filepath.Join(dataDir, "logs")

	store := NewStore(storePath)
	if err := store.Load(); err != nil {
		return fmt.Errorf("加载配置失败: %w", err)
	}
	// 首次运行：导入预设隧道，开箱即用。
	if !fileExists(storePath) {
		base := time.Now().UnixNano()
		for i, p := range builtinPresets {
			t := p
			t.ID = genID()
			t.CreatedAt = base + int64(i) // 保持预设导入顺序
			if err := store.Put(&t); err != nil {
				return err
			}
		}
	}

	mgr := NewManager(store, logDir)
	srv := newServer(mgr, dataDir, port)

	addr := fmt.Sprintf("127.0.0.1:%d", port)
	ln, err := net.Listen("tcp", addr)
	if err != nil {
		return fmt.Errorf("监听 %s 失败: %w", addr, err)
	}
	url := "http://" + addr
	log.Printf("SSH Tunnel %s 正在监听 %s", version, url)
	if !serve {
		go openBrowser(url)
	}
	return http.Serve(ln, srv.router())
}

func defaultDataDir() string {
	home, err := os.UserHomeDir()
	if err != nil {
		return ".ssh-tunnel-data"
	}
	return filepath.Join(home, ".ssh-tunnel")
}

func fileExists(p string) bool {
	_, err := os.Stat(p)
	return err == nil
}

func openBrowser(url string) {
	var c *exec.Cmd
	switch runtime.GOOS {
	case "darwin":
		c = exec.Command("open", url)
	case "linux":
		c = exec.Command("xdg-open", url)
	default:
		return
	}
	_ = c.Start()
}

func must(err error) {
	if err != nil {
		fmt.Fprintln(os.Stderr, "错误:", err)
		os.Exit(1)
	}
}
