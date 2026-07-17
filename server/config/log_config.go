// Copyright (c) 2023-2026 Super Durable, Inc.
//
// This file is part of Dex
//
// This program is free software: you can redistribute it and/or modify
// it under the terms of the GNU Affero General Public License as published by
// the Free Software Foundation, either version 3 of the License, or
// (at your option) any later version.
//
// This program is distributed in the hope that it will be useful,
// but WITHOUT ANY WARRANTY; without even the implied warranty of
// MERCHANTABILITY or FITNESS FOR A PARTICULAR PURPOSE. See the
// GNU Affero General Public License for more details.
//
// You should have received a copy of the GNU Affero General Public License
// along with this program. If not, see <https://www.gnu.org/licenses/>.

package config

type LogLevel string

const (
	LogLevelDebug LogLevel = "DEBUG"
	LogLevelInfo  LogLevel = "INFO"
	LogLevelWarn  LogLevel = "WARN"
	LogLevelError LogLevel = "ERROR"
	LogLevelFatal LogLevel = "FATAL"
)

// LogConfig contains the config items for logger
type LogConfig struct {
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
}

func DefaultLoggerConfig() LogConfig {
	return LogConfig{
		Level: LogLevelInfo,
	}
}
