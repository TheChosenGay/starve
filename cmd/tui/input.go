package main

import (
	"bufio"
	"fmt"
	"io"
	"os"
	"os/exec"
	"strconv"
	"strings"
)

// keyAction 是一个按键语义（由 input 层解出，主循环只处理语义）。
type keyAction int

const (
	keyNone keyAction = iota
	keyMove
	keyStop
	keyAutomate
	keyWork
	keyPickup
	keyToggleOverlay
	keyToggleCamera
	keyRedraw
	keyQuit
)

type keyEvent struct {
	action keyAction
	dx, dy int
}

// term 是终端原始模式封装。刻意不引第三方库：stty 在 macOS/Linux 上都有，
// 而 TUI 只需要"原始模式 + 窗口大小"两件事。任何一步失败都优雅降级
// （不 raw 也能跑，只是按键要回车）。
type term struct {
	tty     *os.File
	saved   string
	raw     bool
	cols    int
	rows    int
	release func()
}

// openTerm 打开 /dev/tty 并切到原始模式；失败时返回可用的降级终端。
func openTerm() (*term, error) {
	t := &term{cols: 80, rows: 24}
	f, err := os.OpenFile("/dev/tty", os.O_RDWR, 0)
	if err != nil {
		f = os.Stdin
	}
	t.tty = f
	if saved, err := t.stty("-g"); err == nil {
		t.saved = strings.TrimSpace(saved)
	}
	if _, err := t.stty("raw", "-echo"); err == nil && t.saved != "" {
		t.raw = true
	}
	t.refreshSize()
	return t, nil
}

func (t *term) stty(args ...string) (string, error) {
	cmd := exec.Command("stty", args...)
	cmd.Stdin = t.tty
	out, err := cmd.Output()
	return string(out), err
}

func (t *term) refreshSize() {
	out, err := t.stty("size")
	if err != nil {
		return
	}
	fields := strings.Fields(strings.TrimSpace(out))
	if len(fields) != 2 {
		return
	}
	if rows, err := strconv.Atoi(fields[0]); err == nil && rows > 4 {
		t.rows = rows
	}
	if cols, err := strconv.Atoi(fields[1]); err == nil && cols > 10 {
		t.cols = cols
	}
}

// restore 还原终端（退出前必须调用，否则终端会留在 raw 模式）。
func (t *term) restore() {
	fmt.Print("\x1b[?25h\x1b[0m\r\n") // 显示光标 + 复位属性
	if t.raw && t.saved != "" {
		_, _ = t.stty(t.saved)
	}
}

// enter 进入全屏（隐藏光标 + 清屏）。
func (t *term) enter() {
	fmt.Print("\x1b[?25l\x1b[2J")
}

// readKeys 持续读按键并翻译成语义事件。
func (t *term) readKeys(out chan<- keyEvent) {
	r := bufio.NewReader(t.tty)
	for {
		b, err := r.ReadByte()
		if err != nil {
			if err == io.EOF {
				select {
				case out <- keyEvent{action: keyQuit}:
				default:
				}
				return
			}
			continue
		}
		if b == 0x1b { // ESC：可能是方向键序列 ESC [ A；不是则忽略（退出统一用 q）
			if b1, err := r.ReadByte(); err == nil && b1 == '[' {
				if b2, err := r.ReadByte(); err == nil {
					switch b2 {
					case 'A':
						out <- keyEvent{action: keyMove, dx: 0, dy: -1}
					case 'B':
						out <- keyEvent{action: keyMove, dx: 0, dy: 1}
					case 'C':
						out <- keyEvent{action: keyMove, dx: 1, dy: 0}
					case 'D':
						out <- keyEvent{action: keyMove, dx: -1, dy: 0}
					}
				}
			}
			continue
		}
		switch b {
		case 'w', 'k':
			out <- keyEvent{action: keyMove, dx: 0, dy: -1}
		case 's', 'j':
			out <- keyEvent{action: keyMove, dx: 0, dy: 1}
		case 'a', 'h':
			out <- keyEvent{action: keyMove, dx: -1, dy: 0}
		case 'd', 'l':
			out <- keyEvent{action: keyMove, dx: 1, dy: 0}
		case 'q', 'Q', 3: // q / Ctrl-C
			out <- keyEvent{action: keyQuit}
		case ' ':
			out <- keyEvent{action: keyStop}
		case 'f', 'F':
			out <- keyEvent{action: keyAutomate}
		case 'e', 'E':
			out <- keyEvent{action: keyWork}
		case 'p', 'P':
			out <- keyEvent{action: keyPickup}
		case 'c', 'C':
			out <- keyEvent{action: keyToggleOverlay}
		case 'v', 'V':
			out <- keyEvent{action: keyToggleCamera}
		case 'r', 'R':
			out <- keyEvent{action: keyRedraw}
		}
	}
}
