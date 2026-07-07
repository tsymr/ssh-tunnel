package main

import (
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"sync"
	"syscall"
	"time"
)

// Tunnel 是一个隧道定义。密码不在此结构中，仅由 Manager 在内存中持有。
type Tunnel struct {
	ID          string `json:"id"`
	Label       string `json:"label"`
	CreatedAt   int64  `json:"created_at"`
	Order       int64  `json:"order"`
	User        string `json:"user"`
	Host        string `json:"host"`
	LocalPort   int    `json:"local_port"`
	RemotePort  int    `json:"remote_port"`
	BindAddr    string `json:"bind_addr"`
	ForwardMode string `json:"forward_mode"` // local | remote
	KeyFile     string `json:"key_file"`
	AuthMethod  string `json:"auth_method"` // key | password
}

func (t *Tunnel) clone() *Tunnel { c := *t; return &c }

func tunnelOrder(t *Tunnel) int64 {
	if t.Order != 0 {
		return t.Order
	}
	return t.CreatedAt
}

func lessTunnelOrder(a, b *Tunnel) bool {
	if tunnelOrder(a) != tunnelOrder(b) {
		return tunnelOrder(a) < tunnelOrder(b)
	}
	if a.CreatedAt != b.CreatedAt {
		return a.CreatedAt < b.CreatedAt
	}
	return a.ID < b.ID
}

func (t *Tunnel) LabelOrID() string {
	if t.Label != "" {
		return t.Label
	}
	return t.ID
}

func validPort(p int) bool { return p >= 1 && p <= 65535 }

func (t *Tunnel) validate() error {
	if t.User == "" {
		return fmt.Errorf("缺少用户名")
	}
	if t.Host == "" {
		return fmt.Errorf("缺少服务器地址")
	}
	if !validPort(t.LocalPort) {
		return fmt.Errorf("无效本地端口: %d", t.LocalPort)
	}
	if !validPort(t.RemotePort) {
		return fmt.Errorf("无效远端端口: %d", t.RemotePort)
	}
	if t.ForwardMode != "local" && t.ForwardMode != "remote" {
		return fmt.Errorf("无效转发模式: %s", t.ForwardMode)
	}
	if t.AuthMethod != "key" && t.AuthMethod != "password" {
		t.AuthMethod = "key"
	}
	return nil
}

// summary 返回人类可读的转发描述，用于日志与界面。
func (t *Tunnel) summary() string {
	mode := "本地"
	spec := ""
	if t.ForwardMode == "remote" {
		mode = "远程"
		if t.BindAddr != "" {
			spec = fmt.Sprintf("%s:%s:%d -> 127.0.0.1:%d", t.Host, t.BindAddr, t.RemotePort, t.LocalPort)
		} else {
			spec = fmt.Sprintf("%s:%d -> 127.0.0.1:%d", t.Host, t.RemotePort, t.LocalPort)
		}
	} else {
		bind := t.BindAddr
		if bind == "" {
			bind = "127.0.0.1"
		}
		spec = fmt.Sprintf("%s:%d -> %s:127.0.0.1:%d", bind, t.LocalPort, t.Host, t.RemotePort)
	}
	label := t.LabelOrID()
	return fmt.Sprintf("[%s] %s转发 %s", label, mode, spec)
}

// buildArgs 构造 ssh 参数，返回是否需要 sshpass 包装。
func (t *Tunnel) buildArgs() (args []string, useSshpass bool, err error) {
	var forward string
	if t.ForwardMode == "remote" {
		if t.BindAddr != "" {
			forward = fmt.Sprintf("%s:%d:127.0.0.1:%d", t.BindAddr, t.RemotePort, t.LocalPort)
		} else {
			forward = fmt.Sprintf("%d:127.0.0.1:%d", t.RemotePort, t.LocalPort)
		}
	} else {
		bind := t.BindAddr
		if bind == "" {
			bind = "127.0.0.1"
		}
		forward = fmt.Sprintf("%s:%d:127.0.0.1:%d", bind, t.LocalPort, t.RemotePort)
	}

	args = []string{
		"-N",
		"-o", "ExitOnForwardFailure=yes",
		"-o", "ServerAliveInterval=30",
		"-o", "ServerAliveCountMax=3",
		"-o", "StrictHostKeyChecking=accept-new",
	}
	if t.ForwardMode == "remote" {
		args = append(args, "-R", forward)
	} else {
		args = append(args, "-L", forward)
	}
	if t.KeyFile != "" {
		if _, e := os.Stat(t.KeyFile); e == nil {
			args = append(args, "-i", t.KeyFile)
		}
	}
	args = append(args, fmt.Sprintf("%s@%s", t.User, t.Host))
	useSshpass = t.AuthMethod == "password"
	return
}

// runningProc 跟踪一个运行中的 ssh 进程。
type runningProc struct {
	cmd      *exec.Cmd
	logFile  *os.File
	done     chan struct{}
	finished bool
	exitErr  error
	started  time.Time
	pid      int
}

// TunnelStatus 是面向 API 的隧道视图，附带运行态。
type TunnelStatus struct {
	*Tunnel
	Running   bool  `json:"running"`
	PID       int   `json:"pid"`
	StartedAt int64 `json:"started_at"`
	AuthReady bool  `json:"auth_ready"` // password 模式下密码是否已就绪
}

// Manager 管理隧道定义、运行中的进程以及内存中的密码。
type Manager struct {
	store   *Store
	logDir  string
	mu      sync.Mutex
	running map[string]*runningProc
	pwds    map[string]string
}

func NewManager(store *Store, logDir string) *Manager {
	_ = os.MkdirAll(logDir, 0o700)
	return &Manager{
		store:   store,
		logDir:  logDir,
		running: map[string]*runningProc{},
		pwds:    map[string]string{},
	}
}

func genID() string {
	b := make([]byte, 4)
	_, _ = rand.Read(b)
	return hex.EncodeToString(b)
}

func commandExists(name string) bool {
	_, err := exec.LookPath(name)
	return err == nil
}

func (m *Manager) List() []*TunnelStatus {
	tunnels := m.store.All()
	// 按用户排序字段排序；旧数据没有 order 时回退到 created_at。
	sort.SliceStable(tunnels, func(i, j int) bool {
		return lessTunnelOrder(tunnels[i], tunnels[j])
	})
	m.mu.Lock()
	defer m.mu.Unlock()
	out := make([]*TunnelStatus, 0, len(tunnels))
	for _, t := range tunnels {
		out = append(out, m.statusLocked(t))
	}
	return out
}

func (m *Manager) StatusOf(id string) (*TunnelStatus, error) {
	t, ok := m.store.Get(id)
	if !ok {
		return nil, fmt.Errorf("隧道不存在")
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.statusLocked(t), nil
}

func (m *Manager) statusLocked(t *Tunnel) *TunnelStatus {
	st := &TunnelStatus{Tunnel: t}
	if rp, ok := m.running[t.ID]; ok && !rp.finished {
		st.Running = true
		st.PID = rp.pid
		st.StartedAt = rp.started.Unix()
	}
	if t.AuthMethod == "password" {
		st.AuthReady = m.pwds[t.ID] != ""
	} else {
		st.AuthReady = true
	}
	return st
}

// Create 校验并创建隧道定义。
func (m *Manager) Create(t *Tunnel, password string) error {
	if err := t.validate(); err != nil {
		return err
	}
	if t.ID == "" {
		t.ID = genID()
	}
	if t.CreatedAt == 0 {
		t.CreatedAt = time.Now().UnixNano()
	}
	if t.Order == 0 {
		t.Order = t.CreatedAt
	}
	for _, ex := range m.store.All() {
		if ex.LocalPort == t.LocalPort {
			return fmt.Errorf("本地端口 %d 已被隧道「%s」占用", t.LocalPort, ex.LabelOrID())
		}
	}
	if err := m.store.Put(t); err != nil {
		return err
	}
	if t.AuthMethod == "password" && password != "" {
		m.mu.Lock()
		m.pwds[t.ID] = password
		m.mu.Unlock()
	}
	return nil
}

// Update 更新隧道定义。password 为空时保留原密码（password 模式下）。
func (m *Manager) Update(id string, t *Tunnel, password string) error {
	old, ok := m.store.Get(id)
	if !ok {
		return fmt.Errorf("隧道不存在")
	}
	t.ID = id
	t.CreatedAt = old.CreatedAt // 编辑不重置创建时间
	t.Order = old.Order         // 编辑不重置手动排序
	if err := t.validate(); err != nil {
		return err
	}
	for _, ex := range m.store.All() {
		if ex.ID != id && ex.LocalPort == t.LocalPort {
			return fmt.Errorf("本地端口 %d 已被隧道「%s」占用", t.LocalPort, ex.LabelOrID())
		}
	}
	if err := m.store.Put(t); err != nil {
		return err
	}
	m.mu.Lock()
	if t.AuthMethod == "password" {
		if password != "" {
			m.pwds[id] = password
		}
	} else {
		delete(m.pwds, id)
	}
	m.mu.Unlock()
	return nil
}

func (m *Manager) Reorder(ids []string) error {
	return m.store.Reorder(ids)
}

// Delete 删除隧道定义；运行中则拒绝。
func (m *Manager) Delete(id string) error {
	m.mu.Lock()
	if rp, ok := m.running[id]; ok && !rp.finished {
		m.mu.Unlock()
		return fmt.Errorf("隧道正在运行，请先停止")
	}
	m.mu.Unlock()
	if err := m.store.Delete(id); err != nil {
		return err
	}
	m.mu.Lock()
	delete(m.pwds, id)
	m.mu.Unlock()
	return nil
}

// SetPassword 设置某个隧道的密码（仅 password 模式有效）。
func (m *Manager) SetPassword(id, password string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if password == "" {
		delete(m.pwds, id)
	} else {
		m.pwds[id] = password
	}
}

// Start 启动隧道对应的 ssh 进程。
func (m *Manager) Start(id string) error {
	t, ok := m.store.Get(id)
	if !ok {
		return fmt.Errorf("隧道不存在")
	}
	if err := t.validate(); err != nil {
		return err
	}

	m.mu.Lock()
	if rp, ok := m.running[id]; ok && !rp.finished {
		m.mu.Unlock()
		return fmt.Errorf("隧道已在运行，PID %d", rp.pid)
	}
	// 清理已退出的旧记录
	if rp, ok := m.running[id]; ok && rp.finished {
		if rp.logFile != nil {
			rp.logFile.Close()
		}
		delete(m.running, id)
	}
	// 本地端口冲突检查
	for rid, rp := range m.running {
		if rp.finished {
			continue
		}
		if ot, ok := m.store.Get(rid); ok && ot.LocalPort == t.LocalPort {
			m.mu.Unlock()
			return fmt.Errorf("本地端口 %d 已被隧道「%s」占用", t.LocalPort, ot.LabelOrID())
		}
	}

	args, useSshpass, err := t.buildArgs()
	if err != nil {
		m.mu.Unlock()
		return err
	}
	if useSshpass {
		if !commandExists("sshpass") {
			m.mu.Unlock()
			return fmt.Errorf("未安装 sshpass，无法使用密码认证（可用 brew install hudochenkov/sshpass/sshpass 安装）")
		}
		if m.pwds[id] == "" {
			m.mu.Unlock()
			return fmt.Errorf("缺少密码，请编辑隧道并填写密码")
		}
	}

	logPath := filepath.Join(m.logDir, fmt.Sprintf("tunnel-%s.log", id))
	lf, err := os.OpenFile(logPath, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o600)
	if err != nil {
		m.mu.Unlock()
		return err
	}

	var cmd *exec.Cmd
	if useSshpass {
		cmd = exec.Command("sshpass", append([]string{"-e"}, args...)...)
		cmd.Env = append(os.Environ(), "SSHPASS="+m.pwds[id])
	} else {
		cmd = exec.Command("ssh", args...)
	}
	cmd.Stdout = lf
	cmd.Stderr = lf
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}

	if err := cmd.Start(); err != nil {
		lf.Close()
		m.mu.Unlock()
		return err
	}
	pid := cmd.Process.Pid
	fmt.Fprintf(lf, "\n==== 启动 %s PID=%d %s ====\n", time.Now().Format("2006-01-02 15:04:05"), pid, t.summary())

	rp := &runningProc{
		cmd:     cmd,
		logFile: lf,
		done:    make(chan struct{}),
		pid:     pid,
		started: time.Now(),
	}
	m.running[id] = rp
	m.mu.Unlock()

	go m.wait(id, rp)
	return nil
}

func (m *Manager) wait(id string, rp *runningProc) {
	err := rp.cmd.Wait()
	exitCode := -1
	if rp.cmd.ProcessState != nil {
		exitCode = rp.cmd.ProcessState.ExitCode()
	}
	rp.exitErr = err
	fmt.Fprintf(rp.logFile, "==== 退出 code=%d err=%v %s ====\n", exitCode, err, time.Now().Format("2006-01-02 15:04:05"))
	_ = rp.logFile.Close()
	close(rp.done)
	m.mu.Lock()
	rp.finished = true
	m.mu.Unlock()
}

// Stop 向 ssh 进程组发送 SIGTERM，超时后 SIGKILL。
func (m *Manager) Stop(id string) error {
	m.mu.Lock()
	rp, ok := m.running[id]
	if !ok || rp.finished {
		m.mu.Unlock()
		return fmt.Errorf("隧道未运行")
	}
	pid := rp.pid
	done := rp.done
	m.mu.Unlock()

	_ = syscall.Kill(-pid, syscall.SIGTERM)
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		_ = syscall.Kill(-pid, syscall.SIGKILL)
		<-done
	}
	return nil
}

// Log 读取隧道日志，返回末尾最多 tailBytes 字节。
func (m *Manager) Log(id string, tailBytes int64) (string, error) {
	if _, ok := m.store.Get(id); !ok {
		return "", fmt.Errorf("隧道不存在")
	}
	logPath := filepath.Join(m.logDir, fmt.Sprintf("tunnel-%s.log", id))
	data, err := os.ReadFile(logPath)
	if err != nil {
		if os.IsNotExist(err) {
			return "", nil
		}
		return "", err
	}
	if tailBytes > 0 && int64(len(data)) > tailBytes {
		data = data[len(data)-int(tailBytes):]
	}
	return string(data), nil
}
