//go:build windows

package logging

import "fmt"

type syslogWriter struct{}

func (w *syslogWriter) Write(p []byte) (int, error) {
	return len(p), nil
}

func (w *syslogWriter) Close() error {
	return nil
}

func openSyslog(tag string) (*syslogWriter, error) {
	return nil, fmt.Errorf("syslog is not supported on windows")
}
