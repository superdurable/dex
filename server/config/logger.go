// Copyright (c) 2025 superdurable
// SPDX-License-Identifier: MIT

package config

type LogLevel string

const (
	LogLevelDebug LogLevel = "DEBUG"
	LogLevelInfo  LogLevel = "INFO"
	LogLevelWarn  LogLevel = "WARN"
	LogLevelError LogLevel = "ERROR"
	LogLevelFatal LogLevel = "FATAL"
)

// LoggerConfig contains the config items for logger
type LoggerConfig struct {
	// Stdout is true then the output needs to goto standard out
	// By default this is false and output will go to standard error
	Stdout bool `yaml:"stdout"`
	// Level is the desired log level
	Level LogLevel `yaml:"level" env:"LOG_LEVEL"`
	// OutputFile is the path to the log output file
	// Stdout must be false, otherwise Stdout will take precedence
	OutputFile string `yaml:"outputFile"`
	// LevelKey is the desired log level, defaults to "level"
	LevelKey string `yaml:"levelKey"`
	// LogJson decides the format to be JSON or pure text.
	LogJson bool `yaml:"logJSON" env:"LOG_JSON"`
	// EnablePreviousPathForLoggingCaller will let logger's "_log-at" "_err-at" to record both current and previous code calling location
	EnablePreviousPathForLoggingCaller bool `yaml:"enablePreviousPathForLoggingCaller"`
}

func DefaultLoggerConfig() LoggerConfig {
	return LoggerConfig{
		Level: LogLevelInfo,
	}
}
