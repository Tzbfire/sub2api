package handler

import (
	"bytes"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
)

// fakeWriter 实现 gin.ResponseWriter 的最小子集供 sseShapeBase 包装。
// 只关心 underlying body 和 header；Flush 计数证明流式 emit 路径触发。
type fakeWriter struct {
	gin.ResponseWriter
	body       bytes.Buffer
	header     http.Header
	statusCode int
	flushed    int
}

func newFakeWriter() *fakeWriter {
	rec := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(rec)
	return &fakeWriter{ResponseWriter: c.Writer, header: http.Header{}}
}

func (f *fakeWriter) Header() http.Header         { return f.header }
func (f *fakeWriter) WriteHeader(code int)        { f.statusCode = code }
func (f *fakeWriter) Write(p []byte) (int, error) { return f.body.Write(p) }
func (f *fakeWriter) WriteString(s string) (int, error) {
	return f.body.WriteString(s)
}
func (f *fakeWriter) Flush() { f.flushed++ }

// 注意：fakeWriter 只覆盖测试关心的方法，其它 ResponseWriter 接口方法走 embed nil，
// 在测试路径上 sseShapeBase 不会调用它们。

func collectFrames(t *testing.T) (*sseShapeBase, *[]string) {
	t.Helper()
	frames := []string{}
	fw := newFakeWriter()
	// gin.ResponseWriter 并不直接接受 fakeWriter，但 sseShapeBase.ResponseWriter 字段
	// 是 interface 类型，赋值即可。
	b := &sseShapeBase{
		ResponseWriter: fw,
		stream:         true,
		statusCode:     200,
		onFrame: func(data []byte) {
			frames = append(frames, string(data))
		},
	}
	return b, &frames
}

func TestSSEShapeBase_NonStream_BuffersBody(t *testing.T) {
	fw := newFakeWriter()
	b := &sseShapeBase{ResponseWriter: fw, stream: false, statusCode: 200}

	n, err := b.Write([]byte("hello "))
	assert.NoError(t, err)
	assert.Equal(t, 6, n)
	_, _ = b.WriteString("world")

	assert.Equal(t, "hello world", string(b.bufferedBody()))
	// 非流式不该有 underlying flush 或写出
	assert.Equal(t, 0, fw.flushed)
	assert.Equal(t, 0, fw.body.Len())
}

func TestSSEShapeBase_Stream_FullFrame(t *testing.T) {
	b, frames := collectFrames(t)
	_, _ = b.Write([]byte("data: {\"a\":1}\n\n"))
	assert.Equal(t, []string{`{"a":1}`}, *frames)
}

func TestSSEShapeBase_Stream_MultipleFramesOneWrite(t *testing.T) {
	b, frames := collectFrames(t)
	_, _ = b.Write([]byte("data: 1\n\ndata: 2\n\ndata: 3\n\n"))
	assert.Equal(t, []string{"1", "2", "3"}, *frames)
}

func TestSSEShapeBase_Stream_PartialFrameThenComplete(t *testing.T) {
	b, frames := collectFrames(t)
	_, _ = b.Write([]byte("data: hel"))
	assert.Empty(t, *frames, "未遇到 \\n\\n 不应触发 onFrame")
	_, _ = b.Write([]byte("lo\n\n"))
	assert.Equal(t, []string{"hello"}, *frames)
}

func TestSSEShapeBase_Stream_DoneSentinel(t *testing.T) {
	b, frames := collectFrames(t)
	_, _ = b.Write([]byte("data: [DONE]\n\n"))
	assert.Equal(t, []string{"[DONE]"}, *frames)
}

func TestSSEShapeBase_Stream_MultiLineDataFrame(t *testing.T) {
	// SSE 规范允许多行 data: 字段拼接
	b, frames := collectFrames(t)
	_, _ = b.Write([]byte("event: msg\ndata: line1\ndata: line2\n\n"))
	assert.Equal(t, []string{"line1\nline2"}, *frames)
}

func TestSSEShapeBase_Stream_FrameWithoutDataLineSkipped(t *testing.T) {
	b, frames := collectFrames(t)
	_, _ = b.Write([]byte(":heartbeat\n\nevent: ping\n\n"))
	assert.Empty(t, *frames, "无 data: 行的帧应被忽略")
}

func TestSSEShapeBase_Stream_HeaderWrittenOnce(t *testing.T) {
	fw := newFakeWriter()
	b := &sseShapeBase{ResponseWriter: fw, stream: true, statusCode: 200}
	b.WriteHeader(200)
	b.WriteHeader(500) // 第二次应被忽略，statusCode 跟踪到最后一次但 underlying 不重发
	assert.Equal(t, 200, fw.statusCode)
	assert.Equal(t, 500, b.statusCode)
	assert.Equal(t, http.Header{"Content-Type": {"text/event-stream"}}, fw.header)
}

func TestSSEShapeBase_NonStream_WriteHeaderNoUnderlying(t *testing.T) {
	fw := newFakeWriter()
	b := &sseShapeBase{ResponseWriter: fw, stream: false, statusCode: 200}
	b.WriteHeader(503)
	// 非流式时 WriteHeader 不该立刻透传到底层
	assert.Equal(t, 0, fw.statusCode)
	assert.Equal(t, 503, b.statusCode)
}

func TestSSEShapeBase_Stream_FlushPropagates(t *testing.T) {
	fw := newFakeWriter()
	b := &sseShapeBase{ResponseWriter: fw, stream: true, statusCode: 200}
	b.Flush()
	b.Flush()
	assert.Equal(t, 2, fw.flushed)
}

func TestSSEShapeBase_NonStream_FlushIgnored(t *testing.T) {
	fw := newFakeWriter()
	b := &sseShapeBase{ResponseWriter: fw, stream: false, statusCode: 200}
	b.Flush()
	assert.Equal(t, 0, fw.flushed)
}

func TestSSEShapeBase_Stream_WriteRawAndFlushUnderlying(t *testing.T) {
	fw := newFakeWriter()
	b := &sseShapeBase{ResponseWriter: fw, stream: true, statusCode: 200}
	b.writeRaw("hello")
	b.flushUnderlying()
	assert.Equal(t, "hello", fw.body.String())
	assert.Equal(t, 1, fw.flushed)
}

func TestSSEShapeBase_Stream_InterleavedPartialFrames(t *testing.T) {
	// 模拟 chunked transfer：一帧分成 5 次 Write 抵达
	b, frames := collectFrames(t)
	chunks := []string{"data:", " {\"k\":", "\"v\"", "}", "\n\n"}
	for _, c := range chunks {
		_, _ = b.Write([]byte(c))
	}
	assert.Equal(t, []string{`{"k":"v"}`}, *frames)
}
