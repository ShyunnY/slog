package handler

import (
	"io"

	"github.com/gookit/goutil/errorx"
	"github.com/gookit/goutil/fsutil"
	"github.com/gookit/slog"
	"github.com/gookit/slog/bufwrite"
	"github.com/gookit/slog/rotatefile"
)

// the buff mode consts
const (
	BuffModeLine = "line"
	BuffModeBite = "bite"
)

// ConfigFn for config some settings
type ConfigFn func(c *Config)

// Config struct
type Config struct {
	// Logfile for write logs
	Logfile string `json:"logfile" yaml:"logfile"`

	// Levels for log record
	Levels []slog.Level `json:"levels" yaml:"levels"`

	// UseJSON for format logs
	UseJSON bool `json:"use_json" yaml:"use_json"`

	// BuffMode type name. allow: line, bite
	BuffMode string `json:"buff_mode" yaml:"buff_mode"`

	// BuffSize for enable buffer, unit is bytes. set 0 to disable buffer
	BuffSize int `json:"buff_size" yaml:"buff_size"`

	// RotateTime for rotate file, unit is seconds.
	RotateTime rotatefile.RotateTime `json:"rotate_time" yaml:"rotate_time"`

	// MaxSize on rotate file by size, unit is bytes.
	MaxSize uint64 `json:"max_size" yaml:"max_size"`

	// Compress determines if the rotated log files should be compressed using gzip.
	// The default is not to perform compression.
	Compress bool `json:"compress" yaml:"compress"`

	// BackupNum max number for keep old files.
	//
	// 0 is not limit, default is 20.
	BackupNum uint `json:"backup_num" yaml:"backup_num"`

	// BackupTime max time for keep old files, unit is hours.
	//
	// 0 is not limit, default is a week.
	BackupTime uint `json:"backup_time" yaml:"backup_time"`

	// RenameFunc build filename for rotate file
	RenameFunc func(filepath string, rotateNum uint) string
}

// NewEmptyConfig new config instance
func NewEmptyConfig(fns ...ConfigFn) *Config {
	c := &Config{Levels: slog.AllLevels}
	return c.With(fns...)
}

// NewConfig new config instance with some default settings.
func NewConfig(fns ...ConfigFn) *Config {
	c := &Config{
		Levels:   slog.AllLevels,
		BuffMode: BuffModeLine,
		BuffSize: DefaultBufferSize,
		// rotate file settings
		MaxSize:    rotatefile.DefaultMaxSize,
		RotateTime: rotatefile.EveryHour,
		// old files clean settings
		BackupNum:  rotatefile.DefaultBackNum,
		BackupTime: rotatefile.DefaultBackTime,
	}

	return c.With(fns...)
}

// With more config settings func
func (c *Config) With(fns ...ConfigFn) *Config {
	for _, fn := range fns {
		fn(c)
	}
	return c
}

// CreateHandler quick create a handler by config
func (c *Config) CreateHandler() (*SyncCloseHandler, error) {
	output, err := c.CreateWriter()
	if err != nil {
		return nil, err
	}

	h := &SyncCloseHandler{
		Output: output,
		// with log levels and formatter
		LevelFormattable: slog.NewLvsFormatter(c.Levels),
	}

	if c.UseJSON {
		h.SetFormatter(slog.NewJSONFormatter())
	}
	return h, nil
}

// RotateWriter build rotate writer by config
func (c *Config) RotateWriter() (output SyncCloseWriter, err error) {
	if c.MaxSize == 0 && c.RotateTime == 0 {
		return nil, errorx.Raw("slog: cannot create rotate writer, MaxSize and RotateTime both is 0")
	}

	return c.CreateWriter()
}

// CreateWriter build writer by config
func (c *Config) CreateWriter() (output SyncCloseWriter, err error) {
	if c.Logfile == "" {
		return nil, errorx.Raw("slog: logfile cannot be empty for create writer")
	}

	// create a rotate config.
	if c.MaxSize > 0 || c.RotateTime > 0 {
		rc := rotatefile.EmptyConfigWith()

		// has locked on logger.write()
		rc.CloseLock = true
		rc.Filepath = c.Logfile
		// copy settings
		rc.MaxSize = c.MaxSize
		rc.RotateTime = c.RotateTime
		rc.BackupNum = c.BackupNum
		rc.BackupTime = c.BackupTime
		rc.Compress = c.Compress

		if c.RenameFunc != nil {
			rc.RenameFunc = c.RenameFunc
		}

		// create a rotating writer
		output, err = rc.Create()
	} else {
		output, err = fsutil.QuickOpenFile(c.Logfile)
	}

	if err != nil {
		return nil, err
	}

	// wrap buffer
	if c.BuffSize > 0 {
		output = c.wrapBuffer(output)
	}
	return
}

type flushSyncCloseWriter interface {
	FlushCloseWriter
	Sync() error
}

// wrap buffer for the writer
func (c *Config) wrapBuffer(w io.Writer) (bw flushSyncCloseWriter) {
	if c.BuffSize == 0 {
		panic("slog: buff size cannot be zero on wrap buffer")
	}

	if c.BuffMode == BuffModeLine {
		bw = bufwrite.NewLineWriterSize(w, c.BuffSize)
	} else {
		bw = bufwrite.NewBufIOWriterSize(w, c.BuffSize)
	}
	return bw
}

// WithLogfile setting
func WithLogfile(logfile string) ConfigFn {
	return func(c *Config) {
		c.Logfile = logfile
	}
}

// WithLogLevels setting
func WithLogLevels(levels slog.Levels) ConfigFn {
	return func(c *Config) {
		c.Levels = levels
	}
}

// WithRotateTime setting
func WithRotateTime(rt rotatefile.RotateTime) ConfigFn {
	return func(c *Config) {
		c.RotateTime = rt
	}
}

// WithBuffMode setting
func WithBuffMode(buffMode string) ConfigFn {
	return func(c *Config) {
		c.BuffMode = buffMode
	}
}

// WithBuffSize setting
func WithBuffSize(buffSize int) ConfigFn {
	return func(c *Config) {
		c.BuffSize = buffSize
	}
}

// WithMaxSize setting
func WithMaxSize(maxSize uint64) ConfigFn {
	return func(c *Config) {
		c.MaxSize = maxSize
	}
}

// WithCompress setting
func WithCompress(compress bool) ConfigFn {
	return func(c *Config) {
		c.Compress = compress
	}
}

// WithUseJSON setting
func WithUseJSON(useJSON bool) ConfigFn {
	return func(c *Config) {
		c.UseJSON = useJSON
	}
}

//
// ---------------------------------------------------------------------------
// handler builder
// ---------------------------------------------------------------------------
//

// Builder struct for create handler
type Builder struct {
	*Config
	Output io.Writer
}

// NewBuilder create
func NewBuilder() *Builder {
	return &Builder{
		Config: NewEmptyConfig(),
	}
}

// WithOutput to the builder
func (b *Builder) WithOutput(w io.Writer) *Builder {
	b.Output = w
	return b
}

// With some config fn
func (b *Builder) With(fns ...ConfigFn) *Builder {
	b.Config.With(fns...)
	return b
}

// WithLogfile setting
func (b *Builder) WithLogfile(logfile string) *Builder {
	b.Logfile = logfile
	return b
}

// WithLogLevels setting
func (b *Builder) WithLogLevels(levels []slog.Level) *Builder {
	b.Levels = levels
	return b
}

// WithBuffMode setting
func (b *Builder) WithBuffMode(bufMode string) *Builder {
	b.BuffMode = bufMode
	return b
}

// WithBuffSize setting
func (b *Builder) WithBuffSize(bufSize int) *Builder {
	b.BuffSize = bufSize
	return b
}

// WithMaxSize setting
func (b *Builder) WithMaxSize(maxSize uint64) *Builder {
	b.MaxSize = maxSize
	return b
}

// WithRotateTime setting
func (b *Builder) WithRotateTime(rt rotatefile.RotateTime) *Builder {
	b.RotateTime = rt
	return b
}

// WithCompress setting
func (b *Builder) WithCompress(compress bool) *Builder {
	b.Compress = compress
	return b
}

// WithUseJSON setting
func (b *Builder) WithUseJSON(useJSON bool) *Builder {
	b.UseJSON = useJSON
	return b
}

// Build slog handler.
func (b *Builder) Build() slog.Handler {
	if b.Output != nil {
		return b.buildFromWriter(b.Output)
	}

	if b.Logfile != "" {
		w, err := b.CreateWriter()
		if err != nil {
			panic(err)
		}
		return b.buildFromWriter(w)
	}

	panic("missing some information for build slog handler")
}

// Build slog handler.
func (b *Builder) reset() {
	b.Output = nil
	b.Config = NewEmptyConfig()
}

// Build slog handler.
func (b *Builder) buildFromWriter(w io.Writer) (h slog.Handler) {
	defer b.reset()
	bufSize := b.BuffSize

	if scw, ok := w.(SyncCloseWriter); ok {
		if bufSize > 0 {
			scw = b.wrapBuffer(scw)
		}

		h = NewSyncCloseHandler(scw, b.Levels)
	} else if fcw, ok := w.(FlushCloseWriter); ok {
		if bufSize > 0 {
			fcw = b.wrapBuffer(fcw)
		}

		h = NewFlushCloseHandler(fcw, b.Levels)
	} else if wc, ok := w.(io.WriteCloser); ok {
		if bufSize > 0 {
			wc = b.wrapBuffer(wc)
		}

		h = NewWriteCloser(wc, b.Levels)
	} else {
		if bufSize > 0 {
			w = b.wrapBuffer(w)
		}

		h = NewIOWriter(w, b.Levels)
	}

	// use json format.
	if b.UseJSON {
		type formatterSetter interface {
			SetFormatter(slog.Formatter)
		}

		// has setter
		_, ok := h.(formatterSetter)
		if ok {
			h.(formatterSetter).SetFormatter(slog.NewJSONFormatter())
		}
	}
	return
}
