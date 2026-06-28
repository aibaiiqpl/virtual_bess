package zaplog

import (
	"fmt"
	"go.uber.org/zap"
	"go.uber.org/zap/zapcore"
	"gopkg.in/natefinch/lumberjack.v2"
	"os"
	"strings"
	"sync"
)

// First, define our level-handling logic.
var (
	infoLevel = zap.LevelEnablerFunc(func(lvl zapcore.Level) bool {
		return lvl >= zapcore.InfoLevel
	})
	debugLevel = zap.LevelEnablerFunc(func(lvl zapcore.Level) bool {
		return lvl >= zapcore.DebugLevel
	})
	zLog  *zap.Logger
	lOnce sync.Once
)

func Debug(msg string, kvs ...any) {
	getLogger().Debug(msg, parser(kvs...)...)
}

func Debugf(template string, args ...any) {
	info := fmt.Sprintf(template, args...)
	getLogger().Debug(info)
}

func Info(msg string, kvs ...any) {
	getLogger().Info(msg, parser(kvs...)...)
}

func Infof(template string, args ...any) {
	info := fmt.Sprintf(template, args...)
	getLogger().Info(info)
}

func Warn(msg string, kvs ...any) {
	getLogger().Warn(msg, parser(kvs...)...)
}

func Warnf(template string, args ...any) {
	info := fmt.Sprintf(template, args...)
	getLogger().Warn(info)
}

func Error(msg string, kvs ...any) {
	getLogger().Error(msg, parser(kvs...)...)
}

func Errorf(template string, args ...any) {
	info := fmt.Sprintf(template, args...)
	getLogger().Error(info)
}

func Panic(msg string, kvs ...any) {
	getLogger().Panic(msg, parser(kvs...)...)
}

func Panicf(template string, args ...any) {
	info := fmt.Sprintf(template, args...)
	getLogger().Panic(info)
}

func NewConsoleEncoderConfig() zapcore.EncoderConfig {
	return zapcore.EncoderConfig{
		// Keys can be anything except the empty string.
		TimeKey:        "timestamp",
		LevelKey:       "level",
		NameKey:        "N",
		CallerKey:      "caller",
		FunctionKey:    zapcore.OmitKey,
		MessageKey:     "M",
		StacktraceKey:  "S",
		LineEnding:     zapcore.DefaultLineEnding,
		EncodeLevel:    zapcore.CapitalLevelEncoder,
		EncodeTime:     zapcore.ISO8601TimeEncoder,
		EncodeDuration: zapcore.StringDurationEncoder,
		//EncodeCaller:   zapcore.FullCallerEncoder,
		EncodeCaller: zapcore.ShortCallerEncoder,
	}
}

func NewFileEncoderConfig() zapcore.EncoderConfig {
	return zapcore.EncoderConfig{
		// Keys can be anything except the empty string.
		TimeKey:        "timestamp",
		LevelKey:       "level",
		NameKey:        "N",
		CallerKey:      "caller",
		FunctionKey:    zapcore.OmitKey,
		MessageKey:     "M",
		StacktraceKey:  "S",
		LineEnding:     zapcore.DefaultLineEnding,
		EncodeLevel:    zapcore.CapitalLevelEncoder,
		EncodeTime:     zapcore.ISO8601TimeEncoder,
		EncodeDuration: zapcore.StringDurationEncoder,
		EncodeCaller:   zapcore.ShortCallerEncoder,
	}
}

func NewStdCore(level string) zapcore.Core {
	consoleWriter := zapcore.Lock(os.Stdout)
	consoleEncoder := zapcore.NewConsoleEncoder(NewConsoleEncoderConfig())
	if strings.ToLower(level) == "debug" {
		return zapcore.NewCore(consoleEncoder, consoleWriter, debugLevel)
	} else {
		return zapcore.NewCore(consoleEncoder, consoleWriter, infoLevel)
	}
}

func NewFileCore(filename string, level string) zapcore.Core {
	writer := &lumberjack.Logger{
		Filename:   filename,
		MaxSize:    50, // megabytes
		MaxBackups: 10,
		MaxAge:     30,   //days
		Compress:   true, // disabled by default
	}
	fileWriter := zapcore.Lock(zapcore.AddSync(writer))
	fileEncoder := zapcore.NewConsoleEncoder(NewFileEncoderConfig())
	if strings.ToLower(level) == "debug" {
		return zapcore.NewCore(fileEncoder, fileWriter, debugLevel)
	} else {
		return zapcore.NewCore(fileEncoder, fileWriter, infoLevel)
	}
}

func InitZapLogger(toConsole bool, filename string, level string) {
	multiCore := make([]zapcore.Core, 0)
	if toConsole {
		multiCore = append(multiCore, NewStdCore(level))
	}
	if filename != "" {
		multiCore = append(multiCore, NewFileCore(filename, level))
	}
	core := zapcore.NewTee(multiCore...)
	zLog = zap.New(core, zap.AddCaller(), zap.AddCallerSkip(1))
}

// NewDefaultLogger 获取默认logger
// 提供标准输出
func NewDefaultLogger() *zap.Logger {
	core := zapcore.NewTee(
		NewStdCore("info"),
	)
	return zap.New(core, zap.AddCaller(), zap.AddCallerSkip(1))
}

func Defer() {
	if zLog == nil {
		return
	}
	err := zLog.Sync()
	if err != nil {
		return
	}
}

func getLogger() *zap.Logger {
	lOnce.Do(func() {
		if zLog == nil {
			zLog = NewDefaultLogger()
		}
	})
	return zLog
}

func parser(kvs ...any) []zap.Field {
	var fields []zap.Field
	if len(kvs)%2 != 0 {
		fields = append(fields, zap.Any("error", "invalid kvs"))
	} else {
		for i := 0; i < len(kvs); i += 2 {
			if _, ok := kvs[i+1].(string); !ok {
				fields = append(fields, zap.Any("error", "invalid kvs"))
				break
			}
			fields = append(fields, zap.Any(kvs[i].(string), kvs[i+1]))
		}
	}
	return fields
}
