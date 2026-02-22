# Configuration Reference

gonewsd reads a single config file (default `/etc/gonewsd.conf`; use `-c <file>` to override). Lines are `key value`; `#` starts a comment. If a directive is not specified, the default value is used. This document explains each directive and its default; see **gonewsd.conf** in the project root for a commented template.

## 📋 Logging

| Directive | Default | Description |
|-----------|---------|-------------|
| **ErrorLog** | **stderr** | Where to send log output: a **file path** (append), **stderr**, **syslog**, or **\|program** to pipe to a command (e.g. `\|logger -f /var/log/gonewsd/error_log`). |
| **ErrorLog.Hex** | **yes** | If **on**/yes, non-ASCII bytes in client commands are logged as `<0x##>`. Useful for debugging; set **off** to see raw characters. |

## 🌐 Network and server

| Directive | Default | Description |
|-----------|---------|-------------|
| **Listen** | **:119** | Address and port to bind: **host:port**, **:port** (all interfaces), or a TCP service name (e.g. **nntp** for port 119). |
| **ServerName** | system hostname (or **localhost**) | Hostname reported to NNTP clients (e.g. in Path and Xref headers). If unset, gonewsd uses the system hostname (from the OS); if that cannot be determined, it uses **localhost**. |
| **HostnameLookups** | **off** | **off** = no reverse DNS; **on** = lookup client IP to hostname; **double** = lookup and verify name resolves back to same IP. |

## ⏱️ Limits and timeouts

| Directive | Default | Description |
|-----------|---------|-------------|
| **MaxClients** | **0** (unlimited) | Maximum number of simultaneous NNTP connections; **0** = unlimited. |
| **Timeout** | **43200** (12 hours) | Idle connection timeout in **seconds**; **0** = no timeout. |
| **MaxLogSize** | **1m** (1048576 bytes) | Max size of the log **file** in bytes before rotation (suffixes **k**, **m**, **g** supported). **0** = disable rotation. Ignored when ErrorLog is stderr/syslog/pipe. |

## 📊 Log level

| Directive | Default | Description |
|-----------|---------|-------------|
| **LogLevel** | **info** | **error** = errors only; **info** = informational and errors; **debug** = verbose (e.g. each client command and response). |

## 💾 Spool and storage

| Directive | Default | Description |
|-----------|---------|-------------|
| **SpoolDir** | **/var/spool/gonewsd** | Root directory for newsgroup data. Each group is a subdirectory (dots in group name become path separators); articles are files inside. |
| **NoRecurseMsgDir** | **on** | **on** = when listing groups, do not recurse into message-number subdirs (faster, typical). **off** = recurse (needed if you use subgroups under a group dir). |
| **MsgModDirs** | **off** | **off** = articles stored as `SpoolDir/group/123`. **on** = articles in modulo dirs, e.g. `SpoolDir/group/1000/1234`, to avoid huge directories. |

## 👤 User and privileges

| Directive | Default | Description |
|-----------|---------|-------------|
| **User** | **news** | System user name the server runs as (e.g. **news**). The binary must be started as root for this to take effect; gonewsd then drops privileges. The user must exist. |

## 📧 Mail and filters

| Directive | Default | Description |
|-----------|---------|-------------|
| **SendMail** | **/usr/sbin/sendmail -t** | Command used to send email (e.g. for mailgateway CCPost). |
| **SpamFilter** | (none) | Optional command; each posted article is piped to it. Exit 0 = accept; non-zero = reject. Example: **spamc -c** for SpamAssassin. |
| **PostCommand** | **-** | Command run after each new article is saved. Arguments are appended as separate command-line arguments (not via shell interpolation) for security: *groupname*, *article#*, *absolute path to article file*. Use **-** to disable. |

## 🔑 Authentication (optional)

When **auth.db** is set, gonewsd uses SQLite for users and group ACLs. Manage with `gonewsd adduser`, `addgroup`, etc.

| Directive | Default | Description |
|-----------|---------|-------------|
| **auth.mode** | **public** | **public** = no auth required, everyone can read/post; **private** = auth required for read and post; **read_public_write_private** = anyone can read, only authenticated users in a group can post. |
| **auth.db** | (empty; auth disabled) | Path to the SQLite database (users, groups, group_membership). Created if missing; ensure the directory exists and is writable by **User**. |
| **auth.log** | (empty; use ErrorLog) | Path for auth-related log lines (e.g. failed logins). If empty or unset, auth events go to **ErrorLog**. |

## ⚙️ Process and PID

| Directive | Default | Description |
|-----------|---------|-------------|
| **PidFile** | (empty; disabled) | Path where gonewsd writes its process ID (one line, number). Used so tools (e.g. admin CLI) can send SIGHUP to trigger auth reload. Optional; use **-** or leave unset to disable. Must be writable by **User**. |

## ⚠️ Deprecated / compatibility

- **LogFile** -- deprecated; use **ErrorLog**.
- **NewHostname** -- deprecated; use **ServerName**.

See **manuals/CONFIG-COMPAT.md** for newsd compatibility notes.
