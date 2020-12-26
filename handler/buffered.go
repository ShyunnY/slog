package handler

import (
	"bufio"

	"github.com/gookit/slog"
)

const defaultFlushInterval = 1000

// BufferedHandler definition
type BufferedHandler struct {
	lockWrapper
	LevelsWithFormatter

	buffer  *bufio.Writer
	handler slog.WriterHandler
	// options:
	// BuffSize for buffer
	BuffSize int
}

// NewBufferedHandler create new BufferedHandler
func NewBufferedHandler(handler slog.WriterHandler, bufSize int) *BufferedHandler {
	return &BufferedHandler{
		buffer:  bufio.NewWriterSize(handler.Writer(), bufSize),
		handler: handler,
		// options
		BuffSize: bufSize,
	}
}

// Flush all buffers to the `h.handler.Writer()`
func (h *BufferedHandler) Flush() error {
	h.Lock()
	defer h.Unlock()

	if err := h.buffer.Flush(); err != nil {
		return err
	}

	return h.handler.Flush()
}

// Close log records
func (h *BufferedHandler) Close() error {
	if err := h.Flush(); err != nil {
		return err
	}

	return h.handler.Close()
}

// Handle log record
func (h *BufferedHandler) Handle(record *slog.Record) error {
	bts, err := h.Formatter().Format(record)
	if err != nil {
		return err
	}

	h.Lock()
	defer h.Unlock()

	if h.buffer == nil {
		h.buffer = bufio.NewWriterSize(h.handler.Writer(), h.BuffSize)
	}

	_, err = h.buffer.Write(bts)

	// flush logs
	if h.buffer.Buffered() >= h.BuffSize {
		return h.Flush()
	}

	return err
}
