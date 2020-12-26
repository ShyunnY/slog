package handler

import (
	"bufio"
	"io"
	"os"
	"path"
	"path/filepath"
	"sync"

	"github.com/gookit/slog"
)

var onceLogDir sync.Once

var (
	// program pid
	pid = os.Getpid()
	// program name
	pName = filepath.Base(os.Args[0])
	hName = "unknownHost" // TODO
	// uName = "unknownUser"
)

// bufferSize sizes the buffer associated with each log file. It's large
// so that log records can accumulate without the logging thread blocking
// on disk I/O. The flushDaemon will block instead.
const bufferSize = 256 * 1024

var (
	// DefaultMaxSize is the maximum size of a log file in bytes.
	DefaultMaxSize uint64 = 1024 * 1024 * 1800
	// perm and flags for create log file
	DefaultFilePerm  = 0664
	DefaultFileFlags = os.O_CREATE | os.O_WRONLY | os.O_APPEND
)

// FileHandler definition
type FileHandler struct {
	// fileWrapper
	lockWrapper
	// LevelsWithFormatter support limit log levels and formatter
	LevelsWithFormatter

	// log file path. eg: "/var/log/my-app.log"
	fpath string
	file  *os.File
	bufio *bufio.Writer

	useJSON bool
	// NoBuffer on write log records
	NoBuffer bool
	// BuffSize for enable buffer
	BuffSize int
}

// JSONFileHandler create new FileHandler with JSON formatter
func JSONFileHandler(filepath string) (*FileHandler, error) {
	return NewFileHandler(filepath, true)
}

// MustFileHandler create file handler
func MustFileHandler(filepath string, useJSON bool) *FileHandler {
	h, err := NewFileHandler(filepath, useJSON)
	if err != nil {
		panic(err)
	}

	return h
}

// NewFileHandler create new FileHandler
func NewFileHandler(filepath string, useJSON bool) (*FileHandler, error) {
	h := &FileHandler{
		fpath:   filepath,
		useJSON: useJSON,
		BuffSize:  bufferSize,
		// FileMode: DefaultFilePerm, // default FileMode
		// FileFlag: DefaultFileFlags,
		// init log levels
		LevelsWithFormatter: LevelsWithFormatter{
			Levels: slog.AllLevels, // default log all levels
		},
	}

	if useJSON {
		h.SetFormatter(slog.NewJSONFormatter())
	} else {
		h.SetFormatter(slog.NewTextFormatter())
	}

	file, err := openFile(filepath, DefaultFileFlags, DefaultFilePerm)
	if err != nil {
		return nil, err
	}

	h.file = file
	return h, nil
}

// Configure the handler
func (h *FileHandler) Configure(fn func(h *FileHandler)) *FileHandler {
	fn(h)
	return h
}

// ReopenFile the log file
func (h *FileHandler) ReopenFile() error {
	file, err := openFile(h.fpath, DefaultFileFlags, DefaultFilePerm)
	if err != nil {
		return err
	}

	h.file = file
	return err
}

// Writer return *os.File
func (h *FileHandler) Writer() io.Writer {
	return h.file
}

// Close handler, will be flush logs to file, then close file
func (h *FileHandler) Close() error {
	if err := h.Flush(); err != nil {
		return err
	}

	return h.file.Close()
}

// Flush logs to disk file
func (h *FileHandler) Flush() error {
	// flush buffers to h.file
	if h.bufio != nil {
		err := h.bufio.Flush()
		if err != nil {
			return err
		}
	}

	return h.file.Sync()
}

// Handle the log record
func (h *FileHandler) Handle(r *slog.Record) (err error) {
	var bts []byte
	bts, err = h.Formatter().Format(r)
	if err != nil {
		return
	}

	// if enable lock
	h.Lock()
	defer h.Unlock()

	// create file
	// if h.file == nil {
	// 	h.file, err = openFile(h.fpath, h.FileFlag, h.FileMode)
	// 	if err != nil {
	// 		return
	// 	}
	// }

	// direct write logs
	if h.NoBuffer {
		_, err = h.file.Write(bts)
		return
	}

	// enable buffer
	if h.bufio == nil {
		h.bufio = bufio.NewWriterSize(h.file, h.BuffSize)
	}

	_, err = h.bufio.Write(bts)
	return
}

func openFile(filepath string, flag int, mode int) (*os.File, error) {
	fileDir := path.Dir(filepath)

	// if err := os.Mkdir(dir, 0777); err != nil {
	if err := os.MkdirAll(fileDir, 0777); err != nil {
		return nil, err
	}

	file, err := os.OpenFile(filepath, flag, os.FileMode(mode))
	if err != nil {
		return nil, err
	}

	return file, nil
}
