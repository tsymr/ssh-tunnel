package main

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"sync"
)

// Store 负责隧道定义的 JSON 持久化（密码不落盘）。
type Store struct {
	path    string
	mu      sync.Mutex
	tunnels map[string]*Tunnel
}

func NewStore(path string) *Store {
	return &Store{path: path, tunnels: map[string]*Tunnel{}}
}

func (s *Store) Load() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	data, err := os.ReadFile(s.path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return err
	}
	var list []*Tunnel
	if err := json.Unmarshal(data, &list); err != nil {
		return err
	}
	s.tunnels = map[string]*Tunnel{}
	// 迁移：旧记录没有 created_at，按文件中的顺序补一个较早的时间戳，
	// 保证按创建时间排序时顺序稳定、且位于新数据之前。
	const legacyBase = 1_700_000_000
	for i, t := range list {
		if t.CreatedAt == 0 {
			t.CreatedAt = legacyBase + int64(i)
		}
		if t.Order == 0 {
			t.Order = t.CreatedAt
		}
		s.tunnels[t.ID] = t
	}
	return nil
}

func (s *Store) saveLocked() error {
	list := make([]*Tunnel, 0, len(s.tunnels))
	for _, t := range s.tunnels {
		list = append(list, t)
	}
	sort.SliceStable(list, func(i, j int) bool {
		return lessTunnelOrder(list[i], list[j])
	})
	data, err := json.MarshalIndent(list, "", "  ")
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(s.path), 0o700); err != nil {
		return err
	}
	tmp := s.path + ".tmp"
	if err := os.WriteFile(tmp, data, 0o600); err != nil {
		return err
	}
	return os.Rename(tmp, s.path)
}

func (s *Store) All() []*Tunnel {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make([]*Tunnel, 0, len(s.tunnels))
	for _, t := range s.tunnels {
		out = append(out, t.clone())
	}
	return out
}

func (s *Store) Get(id string) (*Tunnel, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	t, ok := s.tunnels[id]
	if !ok {
		return nil, false
	}
	return t.clone(), true
}

func (s *Store) Put(t *Tunnel) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.tunnels[t.ID] = t.clone()
	return s.saveLocked()
}

func (s *Store) Reorder(ids []string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if len(ids) != len(s.tunnels) {
		return fmt.Errorf("排序列表数量不匹配")
	}
	seen := make(map[string]bool, len(ids))
	for _, id := range ids {
		if id == "" {
			return fmt.Errorf("排序列表包含空 ID")
		}
		if seen[id] {
			return fmt.Errorf("排序列表包含重复 ID: %s", id)
		}
		if _, ok := s.tunnels[id]; !ok {
			return fmt.Errorf("隧道不存在: %s", id)
		}
		seen[id] = true
	}
	for i, id := range ids {
		s.tunnels[id].Order = int64(i + 1)
	}
	return s.saveLocked()
}

func (s *Store) Delete(id string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	delete(s.tunnels, id)
	return s.saveLocked()
}

func (s *Store) Empty() bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return len(s.tunnels) == 0
}
