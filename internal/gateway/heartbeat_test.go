package gateway

import (
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/TheChosenGay/combet/ws"
	"github.com/gorilla/websocket"
)

// 心跳超时踢线（连接层）：静默连接超过读超时后自动关闭。
// 服务端 ReadLoop 返回超时错误，网关 sweeper 随后会把它转成离线通知。
func TestHeartbeatTimeoutClosesConnection(t *testing.T) {
	done := make(chan error, 1)
	upgrader := websocket.Upgrader{CheckOrigin: func(*http.Request) bool { return true }}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		raw, err := upgrader.Upgrade(w, r, nil)
		if err != nil {
			done <- err
			return
		}
		conn := ws.New(raw, func([]byte) {}, 150*time.Millisecond)
		done <- conn.ReadLoop() // 静默 → 超时 → 返回错误
	}))
	defer srv.Close()

	c, _, err := websocket.DefaultDialer.Dial("ws"+strings.TrimPrefix(srv.URL, "http"), nil)
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	// 客户端不发任何帧

	select {
	case err := <-done:
		ne, ok := err.(net.Error)
		if !ok || !ne.Timeout() {
			t.Fatalf("ReadLoop 应返回超时错误, got %v", err)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("静默连接未在心跳超时后关闭")
	}
}

// 心跳保活：客户端持续发帧（心跳/数据），读超时不断重置，连接不关闭。
func TestHeartbeatKeepsAlive(t *testing.T) {
	var frames atomicCounter
	readLoopDone := make(chan error, 1)
	upgrader := websocket.Upgrader{CheckOrigin: func(*http.Request) bool { return true }}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		raw, err := upgrader.Upgrade(w, r, nil)
		if err != nil {
			readLoopDone <- err
			return
		}
		conn := ws.New(raw, func([]byte) { frames.add() }, 150*time.Millisecond)
		readLoopDone <- conn.ReadLoop()
	}))
	defer srv.Close()

	c, _, err := websocket.DefaultDialer.Dial("ws"+strings.TrimPrefix(srv.URL, "http"), nil)
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()

	// 每 40ms 发一帧，持续 400ms（远超 150ms 超时阈值）
	stopped := make(chan struct{})
	go func() {
		tick := time.NewTicker(40 * time.Millisecond)
		defer tick.Stop()
		for {
			select {
			case <-tick.C:
				if err := c.WriteMessage(websocket.BinaryMessage, []byte("hb")); err != nil {
					return
				}
			case <-stopped:
				return
			}
		}
	}()

	// 发送窗口内：持续有帧，连接不应关闭
	select {
	case err := <-readLoopDone:
		t.Fatalf("持续心跳期间连接不应关闭: %v", err)
	case <-time.After(400 * time.Millisecond):
	}
	close(stopped)
	if frames.get() == 0 {
		t.Fatal("服务端应收到客户端帧")
	}

	// 客户端主动关闭 → ReadLoop 返回关闭类错误（非超时）
	c.Close()
	select {
	case err := <-readLoopDone:
		if ne, ok := err.(net.Error); ok && ne.Timeout() {
			t.Fatalf("主动关闭不应是超时: %v", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("客户端关闭后 ReadLoop 未返回")
	}
}

type atomicCounter struct {
	n int64
}

func (c *atomicCounter) add() { atomic.AddInt64(&c.n, 1) }
func (c *atomicCounter) get() int64 {
	return atomic.LoadInt64(&c.n)
}
