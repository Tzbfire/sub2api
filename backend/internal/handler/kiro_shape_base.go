// Kiro shape writer 公共基础。
//
// /v1/messages 与 /v1/responses 都通过包装 gin.ResponseWriter 拦截下游 KiroChatCompletions
// 写出的 OpenAI chat-completion 字节流，再翻译为目标协议。三块共享的逻辑在这里抽取：
//   - 非流式：缓冲整个 body，由调用方在 finalize() 里翻译
//   - 流式：按 \n\n 切帧、提取 data: 行、识别 [DONE] 哨兵后回调上层 onFrame
//   - http.Flusher / http.Hijacker / WriteHeader 透传
//
// 上层只需实现 onFrame(data) 处理已解析出来的 SSE data 字节（其中 data == []byte("[DONE]") 是结束哨兵）。
package handler

import (
	"bufio"
	"bytes"
	"fmt"
	"net"
	"net/http"
	"sync"

	"github.com/gin-gonic/gin"
)

// sseShapeBase 提供包装 gin.ResponseWriter 时的共用 plumbing。
type sseShapeBase struct {
	gin.ResponseWriter
	stream bool

	// 非流式：缓冲整个 body
	buf bytes.Buffer

	// 流式
	mu            sync.Mutex
	pending       []byte
	headerWritten bool
	statusCode    int

	// onFrame 由上层注入，每解析出一帧 data 就回调一次。
	// data == []byte("[DONE]") 表示 SSE 结束哨兵。
	//
	// 并发约束：onFrame 始终在 b.mu 锁内被调用（来自 Write）。上层若想读写自己的状态，
	// 直接在 onFrame 里访问无需再锁。
	onFrame func(data []byte)
}

func newSSEShapeBase(w gin.ResponseWriter, stream bool, onFrame func([]byte)) *sseShapeBase {
	return &sseShapeBase{
		ResponseWriter: w,
		stream:         stream,
		statusCode:     http.StatusOK,
		onFrame:        onFrame,
	}
}

func (b *sseShapeBase) WriteHeader(code int) {
	b.statusCode = code
	if b.stream && !b.headerWritten {
		b.ResponseWriter.Header().Set("Content-Type", "text/event-stream")
		b.ResponseWriter.WriteHeader(code)
		b.headerWritten = true
	}
	// 非流式：延迟到 finalize 由上层决定
}

func (b *sseShapeBase) Write(p []byte) (int, error) {
	if !b.stream {
		return b.buf.Write(p)
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	b.pending = append(b.pending, p...)
	for {
		idx := bytes.Index(b.pending, []byte("\n\n"))
		if idx < 0 {
			break
		}
		frame := b.pending[:idx]
		b.pending = b.pending[idx+2:]
		b.processFrame(frame)
	}
	return len(p), nil
}

func (b *sseShapeBase) WriteString(s string) (int, error) {
	return b.Write([]byte(s))
}

func (b *sseShapeBase) Flush() {
	if !b.stream {
		return
	}
	if f, ok := b.ResponseWriter.(http.Flusher); ok {
		f.Flush()
	}
}

func (b *sseShapeBase) Hijack() (net.Conn, *bufio.ReadWriter, error) {
	if hj, ok := b.ResponseWriter.(http.Hijacker); ok {
		return hj.Hijack()
	}
	return nil, nil, fmt.Errorf("hijack not supported")
}

// bufferedBody 给非流式 finalize 用。
func (b *sseShapeBase) bufferedBody() []byte { return b.buf.Bytes() }

// writeRaw 直接写到底层 ResponseWriter。
func (b *sseShapeBase) writeRaw(s string) {
	_, _ = b.ResponseWriter.Write([]byte(s))
}

// flushUnderlying 给流式 emit 后立即 flush。
func (b *sseShapeBase) flushUnderlying() {
	if f, ok := b.ResponseWriter.(http.Flusher); ok {
		f.Flush()
	}
}

// processFrame 从一帧（可能多行）里抽出 data: 部分，
// 拼接后交给 onFrame；识别 [DONE] 哨兵。
func (b *sseShapeBase) processFrame(frame []byte) {
	var dataLines [][]byte
	for _, line := range bytes.Split(frame, []byte("\n")) {
		if bytes.HasPrefix(line, []byte("data:")) {
			dataLines = append(dataLines, bytes.TrimSpace(line[5:]))
		}
	}
	if len(dataLines) == 0 {
		return
	}
	data := bytes.Join(dataLines, []byte("\n"))
	if b.onFrame != nil {
		b.onFrame(data)
	}
}
