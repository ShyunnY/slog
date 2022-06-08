package slog

import (
	"context"
	"os"
	"sync"
	"time"

	"github.com/gookit/gsr"
)

// SLogger interface
type SLogger interface {
	gsr.Logger
	Log(level Level, v ...interface{})
	Logf(level Level, format string, v ...interface{})
}

// Logger log dispatcher definition.
//
// The logger implements the `github.com/gookit/gsr.Logger`
type Logger struct {
	name string
	// lock for write logs
	mu sync.Mutex
	// log latest error
	err error

	handlers   []Handler
	processors []Processor

	// reusable empty record
	recordPool sync.Pool

	//
	// logger options
	//

	// LowerLevelName use lower level name
	LowerLevelName bool
	// ReportCaller on write log record
	ReportCaller bool
	CallerSkip   int
	CallerFlag   uint8
	// TimeClock custom time clock, timezone
	TimeClock ClockFn

	// handlers on exit.
	exitHandlers []func()
	// custom exit, panic handle.
	ExitFunc  func(code int)
	PanicFunc func(v interface{})
}

// New create a new logger
func New() *Logger {
	return NewWithName("logger")
}

// NewWithConfig create a new logger with config func
func NewWithConfig(fn func(l *Logger)) *Logger {
	return NewWithName("logger").Configure(fn)
}

// NewWithHandlers create an new logger with handlers
func NewWithHandlers(hs ...Handler) *Logger {
	logger := NewWithName("logger")
	logger.AddHandlers(hs...)
	return logger
}

// NewWithName create an new logger with name
func NewWithName(name string) *Logger {
	logger := &Logger{
		name: name,
		// exit handle
		ExitFunc:     os.Exit,
		PanicFunc:    DefaultPanicFn,
		exitHandlers: []func(){},
		// options
		ReportCaller: true,
		CallerSkip:   5,
		TimeClock:    DefaultClockFn,
	}

	logger.recordPool.New = func() interface{} {
		return newRecord(logger)
	}
	return logger
}

// NewRecord get new logger record
func (l *Logger) newRecord() *Record {
	return l.recordPool.Get().(*Record)
}

func (l *Logger) releaseRecord(r *Record) {
	// reset data
	r.Time = emptyTime
	r.Data = nil
	r.Extra = nil
	r.Fields = nil
	r.CallerSkip = l.CallerSkip
	l.recordPool.Put(r)
}

//
// ---------------------------------------------------------------------------
// Management logger
// ---------------------------------------------------------------------------
//

const flushInterval = 30 * time.Second

// Config current logger
func (l *Logger) Config(fns ...func(l *Logger)) *Logger {
	for _, fn := range fns {
		fn(l)
	}
	return l
}

// Configure current logger
func (l *Logger) Configure(fn func(l *Logger)) *Logger {
	fn(l)
	return l
}

// FlushDaemon run flush handle on daemon
//
// Usage:
// 	go slog.FlushDaemon()
func (l *Logger) FlushDaemon() {
	for range time.NewTicker(flushInterval).C {
		err := l.lockAndFlushAll()
		printlnStderr("slog: daemon flush logs error: ", err)
	}
}

// FlushTimeout flush logs on limit time.
// refer from glog package
func (l *Logger) FlushTimeout(timeout time.Duration) {
	done := make(chan bool, 1)
	go func() {
		err := l.lockAndFlushAll()
		printlnStderr("slog: flush logs error: ", err)

		done <- true
	}()

	select {
	case <-done:
	case <-time.After(timeout):
		printlnStderr("slog: flush took longer than timeout:", timeout)
	}
}

// Sync flushes buffered logs (if any).
//
// alias of the Flush()
func (l *Logger) Sync() error { return Flush() }

// Flush flushes all the logs and attempts to "sync" their data to disk.
// l.mu is held.
func (l *Logger) Flush() error { return l.lockAndFlushAll() }

// MustFlush flush logs. will panic on error
func (l *Logger) MustFlush() {
	err := l.lockAndFlushAll()
	if err != nil {
		panic(err)
	}
}

// FlushAll flushes all the logs and attempts to "sync" their data to disk.
//
// alias of the Flush()
func (l *Logger) FlushAll() error { return l.lockAndFlushAll() }

// lockAndFlushAll is like flushAll but locks l.mu first.
func (l *Logger) lockAndFlushAll() error {
	l.mu.Lock()
	l.flushAll()
	l.mu.Unlock()

	return l.err
}

// flush all without lock
func (l *Logger) flushAll() {
	// Flush from fatal down, in case there's trouble flushing.
	_ = l.VisitAll(func(handler Handler) error {
		err := handler.Flush()
		if err != nil {
			l.err = err
			printlnStderr("slog: call handler.Flush() error:", err)
		}
		return nil
	})
}

// Close the logger
func (l *Logger) Close() error {
	_ = l.VisitAll(func(handler Handler) error {
		// Flush logs and then close
		err := handler.Close()
		if err != nil {
			l.err = err
			printlnStderr("slog: call handler.Close() error:", err)
		}
		return nil
	})

	return l.err
}

// VisitAll logger handlers
func (l *Logger) VisitAll(fn func(handler Handler) error) error {
	for _, handler := range l.handlers {
		// you can return nil for ignore error
		if err := fn(handler); err != nil {
			return err
		}
	}
	return nil
}

// Reset the logger
func (l *Logger) Reset() {
	l.ResetHandlers()
	l.ResetProcessors()
}

// ResetProcessors for the logger
func (l *Logger) ResetProcessors() {
	l.processors = make([]Processor, 0)
}

// ResetHandlers for the logger
func (l *Logger) ResetHandlers() {
	l.handlers = make([]Handler, 0)
}

// Exit logger handle
func (l *Logger) Exit(code int) {
	l.runExitHandlers()

	// global exit handlers
	runExitHandlers()

	if l.ExitFunc == nil {
		l.ExitFunc = os.Exit
	}
	l.ExitFunc(code)
}

// SetName for logger
func (l *Logger) SetName(name string) { l.name = name }

// Name of the logger
func (l *Logger) Name() string { return l.name }

// DoNothingOnPanicFatal do nothing on panic or fatal level.
// useful on testing.
func (l *Logger) DoNothingOnPanicFatal() {
	l.PanicFunc = DoNothingOnPanic
	l.ExitFunc = DoNothingOnExit
}

// LastErr fetch, will clear after read.
func (l *Logger) LastErr() error {
	err := l.err
	l.err = nil
	return err
}

//
// ---------------------------------------------------------------------------
// Register handlers and processors
// ---------------------------------------------------------------------------
//

// AddHandler to the logger
func (l *Logger) AddHandler(h Handler) { l.handlers = append(l.handlers, h) }

// AddHandlers to the logger
func (l *Logger) AddHandlers(hs ...Handler) { l.handlers = append(l.handlers, hs...) }

// PushHandlers to the logger
func (l *Logger) PushHandlers(hs ...Handler) { l.handlers = append(l.handlers, hs...) }

// PushHandler to the l. alias of AddHandler()
func (l *Logger) PushHandler(h Handler) { l.AddHandler(h) }

// SetHandlers for the logger
func (l *Logger) SetHandlers(hs []Handler) { l.handlers = hs }

// AddProcessor to the logger
func (l *Logger) AddProcessor(p Processor) { l.processors = append(l.processors, p) }

// PushProcessor to the logger
// alias of AddProcessor()
func (l *Logger) PushProcessor(p Processor) { l.processors = append(l.processors, p) }

// AddProcessors to the logger
func (l *Logger) AddProcessors(ps ...Processor) { l.processors = append(l.processors, ps...) }

// SetProcessors for the logger
func (l *Logger) SetProcessors(ps []Processor) { l.processors = ps }

//
// ---------------------------------------------------------------------------
// New record with log data, fields
// ---------------------------------------------------------------------------
//

// Record return a new record for log
func (l *Logger) Record() *Record {
	r := l.newRecord()

	defer l.releaseRecord(r)
	return r
}

// WithField new record with field
func (l *Logger) WithField(name string, value interface{}) *Record {
	r := l.newRecord()

	defer l.releaseRecord(r)
	return r.WithField(name, value)
}

// WithFields new record with fields
func (l *Logger) WithFields(fields M) *Record {
	r := l.newRecord()

	defer l.releaseRecord(r)
	return r.WithFields(fields)
}

// WithData new record with data
func (l *Logger) WithData(data M) *Record {
	r := l.newRecord()

	defer l.releaseRecord(r)
	return r.WithData(data)
}

// WithTime new record with time.Time
func (l *Logger) WithTime(t time.Time) *Record {
	r := l.newRecord()

	defer l.releaseRecord(r)
	return r.WithTime(t)
}

// WithContext new record with context.Context
func (l *Logger) WithContext(ctx context.Context) *Record {
	r := l.newRecord()

	defer l.releaseRecord(r)
	return r.WithContext(ctx)
}

//
// ---------------------------------------------------------------------------
// Add log message with level
// ---------------------------------------------------------------------------
//

func (l *Logger) log(level Level, args []interface{}) {
	r := l.newRecord()
	r.CallerSkip++
	r.log(level, args)
	l.releaseRecord(r)
}

// Logf a format message with level
func (l *Logger) logf(level Level, format string, args []interface{}) {
	r := l.newRecord()
	r.CallerSkip++
	r.logf(level, format, args)
	l.releaseRecord(r)
}

// Log a message with level
func (l *Logger) Log(level Level, args ...interface{}) {
	l.log(level, args)
}

// Logf a format message with level
func (l *Logger) Logf(level Level, format string, args ...interface{}) {
	l.logf(level, format, args)
}

// Print logs a message at level PrintLevel
func (l *Logger) Print(args ...interface{}) {
	l.log(PrintLevel, args)
}

// Println logs a message at level PrintLevel
func (l *Logger) Println(args ...interface{}) {
	l.log(PrintLevel, args)
}

// Printf logs a message at level PrintLevel
func (l *Logger) Printf(format string, args ...interface{}) {
	l.logf(PrintLevel, format, args)
}

// Warn logs a message at level Warn
func (l *Logger) Warn(args ...interface{}) {
	l.log(WarnLevel, args)
}

// Warnf logs a message at level Warn
func (l *Logger) Warnf(format string, args ...interface{}) {
	l.logf(WarnLevel, format, args)
}

// Warning logs a message at level Warn
func (l *Logger) Warning(args ...interface{}) {
	l.log(WarnLevel, args)
}

// Info logs a message at level Info
func (l *Logger) Info(args ...interface{}) {
	l.log(InfoLevel, args)
}

// Infof logs a message at level Info
func (l *Logger) Infof(format string, args ...interface{}) {
	l.logf(InfoLevel, format, args)
}

// Trace logs a message at level Trace
func (l *Logger) Trace(args ...interface{}) {
	l.log(TraceLevel, args)
}

// Tracef logs a message at level Trace
func (l *Logger) Tracef(format string, args ...interface{}) {
	l.logf(TraceLevel, format, args)
}

// Error logs a message at level error
func (l *Logger) Error(args ...interface{}) {
	l.log(ErrorLevel, args)
}

// Errorf logs a message at level Error
func (l *Logger) Errorf(format string, args ...interface{}) {
	l.logf(ErrorLevel, format, args)
}

// ErrorT logs a error type at level Error
func (l *Logger) ErrorT(err error) {
	if err != nil {
		l.log(ErrorLevel, []interface{}{err})
	}
}

// Notice logs a message at level Notice
func (l *Logger) Notice(args ...interface{}) {
	l.log(NoticeLevel, args)
}

// Noticef logs a message at level Notice
func (l *Logger) Noticef(format string, args ...interface{}) {
	l.logf(NoticeLevel, format, args)
}

// Debug logs a message at level Debug
func (l *Logger) Debug(args ...interface{}) {
	l.log(DebugLevel, args)
}

// Debugf logs a message at level Debug
func (l *Logger) Debugf(format string, args ...interface{}) {
	l.logf(DebugLevel, format, args)
}

// Fatal logs a message at level Fatal
func (l *Logger) Fatal(args ...interface{}) {
	l.log(FatalLevel, args)
}

// Fatalf logs a message at level Fatal
func (l *Logger) Fatalf(format string, args ...interface{}) {
	l.logf(FatalLevel, format, args)
}

// Fatalln logs a message at level Fatal
func (l *Logger) Fatalln(args ...interface{}) {
	l.log(FatalLevel, args)
}

// Panic logs a message at level Panic
func (l *Logger) Panic(args ...interface{}) {
	l.log(PanicLevel, args)
}

// Panicf logs a message at level Panic
func (l *Logger) Panicf(format string, args ...interface{}) {
	l.logf(PanicLevel, format, args)
}

// Panicln logs a message at level Panic
func (l *Logger) Panicln(args ...interface{}) {
	l.log(PanicLevel, args)
}
