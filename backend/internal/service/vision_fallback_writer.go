package service

import (
	"bufio"
	"bytes"
	"errors"
	"net"
	"net/http"
)

// 辅助回合使用独立、有界的响应缓冲区，不把辅助文本、响应头或 SSE 事件发给客户端。
type visionResponseWriter struct {
	header   http.Header
	body     bytes.Buffer
	status   int
	written  bool
	overflow bool
	closed   chan bool
}

func newVisionResponseWriter() *visionResponseWriter {
	return &visionResponseWriter{header: make(http.Header), status: http.StatusOK, closed: make(chan bool)}
}

func (w *visionResponseWriter) Header() http.Header      { return w.header }
func (w *visionResponseWriter) Status() int              { return w.status }
func (w *visionResponseWriter) Written() bool            { return w.written }
func (w *visionResponseWriter) WriteHeaderNow()          { w.written = true }
func (w *visionResponseWriter) Flush()                   { w.WriteHeaderNow() }
func (w *visionResponseWriter) CloseNotify() <-chan bool { return w.closed }
func (w *visionResponseWriter) Pusher() http.Pusher      { return nil }

func (w *visionResponseWriter) Size() int {
	if !w.written {
		return -1
	}
	return w.body.Len()
}

func (w *visionResponseWriter) WriteHeader(status int) {
	if !w.written {
		w.status = status
	}
}

func (w *visionResponseWriter) Write(body []byte) (int, error) {
	w.WriteHeaderNow()
	if w.body.Len()+len(body) > 1<<20 {
		w.overflow = true
		return 0, errors.New("vision helper response exceeds 1 MiB")
	}
	return w.body.Write(body)
}

func (w *visionResponseWriter) WriteString(body string) (int, error) {
	return w.Write([]byte(body))
}

func (w *visionResponseWriter) Hijack() (net.Conn, *bufio.ReadWriter, error) {
	return nil, nil, errors.New("vision helper cannot hijack the client connection")
}
