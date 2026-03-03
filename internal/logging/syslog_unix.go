//go:build !windows

package logging

import "log/syslog"

type syslogWriter = syslog.Writer

func openSyslog(tag string) (*syslogWriter, error) {
	return syslog.New(syslog.LOG_NEWS|syslog.LOG_DAEMON, tag)
}
