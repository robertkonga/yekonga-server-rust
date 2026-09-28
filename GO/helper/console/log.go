package console

import "github.com/robertkonga/yekonga-server-go/helper/logger"

func Log(args ...any) {
	logger.Log(args...)
}

func Error(args ...any) {
	logger.Error(args...)
}

func Success(args ...any) {
	logger.Success(args...)
}

func Warn(args ...any) {
	logger.Warn(args...)
}

func Info(args ...any) {
	logger.Info(args...)
}
func Logo() {
	logger.Logo()
}
