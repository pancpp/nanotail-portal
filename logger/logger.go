package logger

import (
	"io"
	"log"
	"os"
	"path/filepath"

	"github.com/pancpp/nanotail-portal/conf"
	"gopkg.in/natefinch/lumberjack.v2"
)

var fileWriter *lumberjack.Logger

func Init() error {
	logDir := conf.GetString("log_dir")
	if err := os.MkdirAll(logDir, 0755); err != nil {
		return err
	}

	fileLogPath := filepath.Join(logDir, "nanotail.log")
	fileLogWriter := &lumberjack.Logger{
		Filename:   fileLogPath,
		MaxSize:    10, // megabytes
		MaxBackups: 3,
		LocalTime:  true,
		Compress:   true,
	}
	fileWriter = fileLogWriter
	if conf.GetBool("enable_console_log") {
		log.SetOutput(io.MultiWriter(fileLogWriter, os.Stderr))
	} else {
		log.SetOutput(fileLogWriter)
	}
	log.SetFlags(log.LstdFlags | log.Lshortfile)

	return nil
}

// Close redirects subsequent diagnostics to stderr before reset removes logs.
func Close() error {
	log.SetOutput(os.Stderr)
	if fileWriter != nil {
		return fileWriter.Close()
	}
	return nil
}
