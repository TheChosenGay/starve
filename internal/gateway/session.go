package gateway

import (
	"sync"

	"starve/internal/ecs"
)

// Session 一个登录玩家的会话：UID ↔ 连接 ↔ 世界实体。
type Session struct {
	UID        string
	ConnID     string
	EntityID   ecs.Entity
	InputEpoch uint64
}

// Sessions 会话表（线程安全）：connID → session，uid → session（踢旧用）。
type Sessions struct {
	mu     sync.RWMutex
	byConn map[string]*Session
	byUID  map[string]*Session
}

func NewSessions() *Sessions {
	return &Sessions{
		byConn: make(map[string]*Session),
		byUID:  make(map[string]*Session),
	}
}

// Bind 绑定会话；同 UID 已有连接时返回旧会话（调用方负责踢线）。
func (s *Sessions) Bind(uid, connID string, entity ecs.Entity) *Session {
	return s.BindWithEpoch(uid, connID, entity, 0)
}

// BindWithEpoch 绑定带输入世代的会话；同 UID 旧连接立即从查询表移除。
func (s *Sessions) BindWithEpoch(uid, connID string, entity ecs.Entity, inputEpoch uint64) *Session {
	s.mu.Lock()
	defer s.mu.Unlock()
	sess := &Session{UID: uid, ConnID: connID, EntityID: entity, InputEpoch: inputEpoch}
	old := s.byUID[uid]
	if old != nil {
		delete(s.byConn, old.ConnID)
	}
	s.byUID[uid] = sess
	s.byConn[connID] = sess
	return old
}

// GetByConn 按连接查会话。
func (s *Sessions) GetByConn(connID string) (*Session, bool) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	sess, ok := s.byConn[connID]
	return sess, ok
}

// RemoveByConn 连接断开时移除会话。
func (s *Sessions) RemoveByConn(connID string) *Session {
	s.mu.Lock()
	defer s.mu.Unlock()
	sess, ok := s.byConn[connID]
	if !ok {
		return nil
	}
	delete(s.byConn, connID)
	if s.byUID[sess.UID] == sess {
		delete(s.byUID, sess.UID)
	}
	return sess
}

// Count 当前在线会话数。
func (s *Sessions) Count() int {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return len(s.byUID)
}

// All 返回全部在线会话（广播用；顺序无保证）。
func (s *Sessions) All() []*Session {
	s.mu.RLock()
	defer s.mu.RUnlock()
	out := make([]*Session, 0, len(s.byUID))
	for _, sess := range s.byUID {
		out = append(out, sess)
	}
	return out
}
