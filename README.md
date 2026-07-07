# SSH Tunnel

一个用 Go 写的单二进制 **SSH 隧道管理器**,带 Web 界面,可配置/开启/关闭本地(`-L`)与远程(`-R`)端口转发。仅监听本机 `127.0.0.1`,适合个人在本机使用。

## 特性

- 单二进制,前端通过 `//go:embed` 打包,无外部依赖
- Web 界面管理多个隧道:新增 / 编辑 / 删除 / 启停 / 查看日志
- 支持私钥(`~/.ssh/id_rsa`)与密码(需系统装 `sshpass`)两种认证
- 密码仅存内存,**绝不落盘**;服务重启后需重新输入
- 可一键安装为 macOS `launchd` 服务:开机自启、崩溃重启
- 首次运行自动导入三个预设隧道(`10.0.0.23` 的 8088 / 18765 / 9222)

## 编译

```bash
go build -o ssh-tunnel-web .
```

## 运行

```bash
# 前台运行,自动打开浏览器
./ssh-tunnel-web

# 指定端口 / 数据目录
./ssh-tunnel-web --port 18787 --data-dir ~/.ssh-tunnel

# 服务模式(由 launchd 托管时不打开浏览器)
./ssh-tunnel-web --serve
```

打开 http://127.0.0.1:18787 即可使用。

## 安装为系统服务(macOS)

```bash
./ssh-tunnel-web --install            # 安装并启动
./ssh-tunnel-web --uninstall          # 卸载
```

> ⚠️ 安装后**不要移动** `ssh-tunnel-web` 的位置——plist 中写死了它的绝对路径。
> 服务日志:`~/.ssh-tunnel/service.log`。各隧道日志:`~/.ssh-tunnel/logs/`。

## 数据目录

| 路径 | 说明 |
|------|------|
| `~/.ssh-tunnel/tunnels.json` | 隧道定义(不含密码) |
| `~/.ssh-tunnel/logs/tunnel-<id>.log` | 每条隧道的 ssh 输出日志 |
| `~/.ssh-tunnel/service.log` | 服务自身的 stdout/stderr |

## 关于 sshpass

若要用**密码**认证,需安装 `sshpass`:

```bash
brew install hudochenkov/sshpass/sshpass
```

私钥认证无需 sshpass。状态栏会显示 sshpass 是否可用。

## 启停原理

- 启动:以 `ssh -N -L/-R ... -o ExitOnForwardFailure=yes -o ServerAliveInterval=30 ...` 拉起进程,设为独立进程组,日志重定向到文件
- 停止:向整个进程组发 `SIGTERM`,5 秒后仍未退出则 `SIGKILL`
- 退出码与错误实时写入隧道日志,Web 上可随时查看
