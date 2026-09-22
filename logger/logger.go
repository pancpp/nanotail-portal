package logger

import (
	"errors"
	"io"
	"log"
	"os"
	"path"

	"github.com/pancpp/fairnet-portal/conf"
	"gopkg.in/natefinch/lumberjack.v2"
)

func Init() error {
	logDir := conf.GetString("log_dir")
	// check log directory existence
	if _, err := os.Stat(logDir); errors.Is(err, os.ErrNotExist) {
		if err := os.MkdirAll(logDir, 0755); err != nil {
			return err
		}
	}

	fileLogPath := path.Join(logDir, "fairnet-portal.log")
	fileLogWriter := &lumberjack.Logger{
		Filename:   fileLogPath,
		MaxSize:    500, // megabytes
		MaxBackups: 5,
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
