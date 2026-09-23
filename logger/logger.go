package logger

import (
	"io"
	"log"
	"os"
	"path/filepath"

	"github.com/pancpp/fairnet-portal/conf"
	"gopkg.in/natefinch/lumberjack.v2"
)

func Init() error {
	logDir := conf.GetString("log_dir")
	if err := os.MkdirAll(logDir, 0755); err != nil {
		return err
	}

	fileLogPath := filepath.Join(logDir, "fairnet-portal.log")
	fileLogWriter := &lumberjack.Logger{
		Filename:   fileLogPath,
		MaxSize:    10, // megabytes
		MaxBackups: 3,
		LocalTime:  true,
		Compress:   true,
	}
	if conf.GetBool("enable_console_log") {
		log.SetOutput(io.MultiWriter(fileLogWriter, os.Stderr))
	} else {
		log.SetOutput(fileLogWriter)
	}
	log.SetFlags(log.LstdFlags | log.Lshortfile)

	return nil
}
