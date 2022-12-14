package log

import (
	"errors"
	"fmt"
	"io"
	"log"
	"time"
)

var (
	errEmptyLoggerName            = errors.New("cannot have empty logger name")
	errSubLoggerAlreadyregistered = errors.New("sub logger already registered")
)

func newLogger(c *Config) Logger {
	return Logger{
		Timestamp:         c.AdvancedSettings.TimeStampFormat,
		Spacer:            c.AdvancedSettings.Spacer,
		ErrorHeader:       c.AdvancedSettings.Headers.Error,
		InfoHeader:        c.AdvancedSettings.Headers.Info,
		WarnHeader:        c.AdvancedSettings.Headers.Warn,
		DebugHeader:       c.AdvancedSettings.Headers.Debug,
		ShowLogSystemName: *c.AdvancedSettings.ShowLogSystemName,
	}
}

func (l *Logger) newLogEvent(data, header, slName string, w io.Writer) error {
	if w == nil {
		return errors.New("io.Writer not set")
	}

	pool, ok := eventPool.Get().(*[]byte)
	if !ok {
		return errors.New("unable to type assert slice of bytes pointer")
	}

	*pool = append(*pool, header...)
	if l.ShowLogSystemName {
		*pool = append(*pool, l.Spacer...)
		*pool = append(*pool, slName...)
	}
	*pool = append(*pool, l.Spacer...)
	if l.Timestamp != "" {
		*pool = time.Now().AppendFormat(*pool, l.Timestamp)
	}
	*pool = append(*pool, l.Spacer...)
	*pool = append(*pool, data...)
	if data == "" || data[len(data)-1] != '\n' {
		*pool = append(*pool, '\n')
	}
	_, err := w.Write(*pool)
	*pool = (*pool)[:0]
	eventPool.Put(pool)

	return err
}

// CloseLogger is called on shutdown of application
func CloseLogger() error {
	return GlobalLogFile.Close()
}

// Level retries the current sublogger levels
func Level(name string) (Levels, error) {
	RWM.RLock()
	defer RWM.RUnlock()
	subLogger, found := SubLoggers[name]
	if !found {
		return Levels{}, fmt.Errorf("logger %s not found", name)
	}
	return subLogger.levels, nil
}

// SetLevel sets sublogger levels
func SetLevel(s, level string) (Levels, error) {
	RWM.Lock()
	defer RWM.Unlock()
	subLogger, found := SubLoggers[s]
	if !found {
		return Levels{}, fmt.Errorf("sub logger %v not found", s)
	}
	subLogger.SetLevels(splitLevel(level))
	return subLogger.levels, nil
}

// Info takes a pointer subLogger struct and string sends to newLogEvent
func Info(sl *SubLogger, data string) {
	fields := sl.getFields()
	if fields == nil || !fields.info {
		return
	}

	displayError(fields.logger.newLogEvent(data,
		fields.logger.InfoHeader,
		fields.name,
		fields.output))
}

// Infoln takes a pointer subLogger struct and interface sends to newLogEvent
func Infoln(sl *SubLogger, v ...interface{}) {
	fields := sl.getFields()
	if fields == nil || !fields.info {
		return
	}
	displayError(fields.logger.newLogEvent(fmt.Sprintln(v...),
		fields.logger.InfoHeader,
		fields.name,
		fields.output))
}

// Infof takes a pointer subLogger struct, string & interface formats and sends to Info()
func Infof(sl *SubLogger, data string, v ...interface{}) {
	Info(sl, fmt.Sprintf(data, v...))
}

// Debug takes a pointer subLogger struct and string sends to multiwriter
func Debug(sl *SubLogger, data string) {
	fields := sl.getFields()
	if fields == nil || !fields.debug {
		return
	}
	displayError(fields.logger.newLogEvent(data,
		fields.logger.DebugHeader,
		fields.name,
		fields.output))
}

// Debugln  takes a pointer subLogger struct, string and interface sends to newLogEvent
func Debugln(sl *SubLogger, v ...interface{}) {
	fields := sl.getFields()
	if fields == nil || !fields.debug {
		return
	}

	displayError(fields.logger.newLogEvent(fmt.Sprintln(v...),
		fields.logger.DebugHeader,
		fields.name,
		fields.output))
}

// Debugf takes a pointer subLogger struct, string & interface formats and sends to Info()
func Debugf(sl *SubLogger, data string, v ...interface{}) {
	Debug(sl, fmt.Sprintf(data, v...))
}

// Warn takes a pointer subLogger struct & string  and sends to newLogEvent()
func Warn(sl *SubLogger, data string) {
	fields := sl.getFields()
	if fields == nil || !fields.warn {
		return
	}
	displayError(fields.logger.newLogEvent(data,
		fields.logger.WarnHeader,
		fields.name,
		fields.output))
}

// Warnln takes a pointer subLogger struct & interface formats and sends to newLogEvent()
func Warnln(sl *SubLogger, v ...interface{}) {
	fields := sl.getFields()
	if fields == nil || !fields.warn {
		return
	}
	displayError(fields.logger.newLogEvent(fmt.Sprintln(v...),
		fields.logger.WarnHeader,
		fields.name,
		fields.output))
}

// Warnf takes a pointer subLogger struct, string & interface formats and sends to Warn()
func Warnf(sl *SubLogger, data string, v ...interface{}) {
	Warn(sl, fmt.Sprintf(data, v...))
}

// Error takes a pointer subLogger struct & interface formats and sends to newLogEvent()
func Error(sl *SubLogger, data ...interface{}) {
	fields := sl.getFields()
	if fields == nil || !fields.error {
		return
	}
	displayError(fields.logger.newLogEvent(fmt.Sprint(data...),
		fields.logger.ErrorHeader,
		fields.name,
		fields.output))
}

// Errorln takes a pointer subLogger struct, string & interface formats and sends to newLogEvent()
func Errorln(sl *SubLogger, v ...interface{}) {
	fields := sl.getFields()
	if fields == nil || !fields.error {
		return
	}
	displayError(fields.logger.newLogEvent(fmt.Sprintln(v...),
		fields.logger.ErrorHeader,
		fields.name,
		fields.output))
}

// Errorf takes a pointer subLogger struct, string & interface formats and sends to Debug()
func Errorf(sl *SubLogger, data string, v ...interface{}) {
	Error(sl, fmt.Sprintf(data, v...))
}

func displayError(err error) {
	if err != nil {
		log.Printf("Logger write error: %v\n", err)
	}
}