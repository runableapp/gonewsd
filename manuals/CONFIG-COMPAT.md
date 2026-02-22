# Config compatibility with newsd

gonewsd is a Go (Golang) port of the original 'newsd' NNTP news server. This project was designed to be as compatible as possible with newsd -- many configuration directives are supported with the same names and behaviors, allowing drop-in use of existing newsd config files with minimal changes.

Below is a comparison of newsd config directives and their implementation or equivalents in gonewsd.


**Sources:** [newsd.conf](https://github.com/erco77/newsd/blob/master/newsd.conf), [newsd.conf.pod](https://github.com/erco77/newsd/blob/master/newsd.conf.pod) (man/docs).

## ✅ Implemented (same or equivalent)

| newsd.conf directive | gonewsd | Notes |
|----------------------|---------|--------|
| **ErrorLog** | `errorlog` | filename, `stderr`, `syslog`, `\|command` |
| **ErrorLog.Hex** | `errorlog.hex` | Log non-ASCII as `<0x##>` |
| **HostnameLookups** | `hostnamelookups` | off / on / double |
| **Listen** | `listen` | host:port, `:port`, or service name (e.g. `nntp` → port 119) |
| **LogLevel** | `loglevel` | error / info / debug |
| **MaxClients** | `maxclients` | 0 = unlimited |
| **MaxLogSize** | `maxlogsize` | bytes; k/m/g suffixes |
| **SendMail** | `sendmail` | Mail command for mailgateway |
| **ServerName** | `servername` | Hostname sent to clients |
| **SpamFilter** | `spamfilter` | Filter program (e.g. spamc) |
| **SpoolDir** | `spooldir` | Root for newsgroup files |
| **Timeout** | `timeout` | Idle timeout in seconds |
| **User** | `user` | Run-as user (e.g. news, nobody) |
| **NoRecurseMsgDir** | `norecursemsgdir` | Don’t recurse into msg dirs for LIST |
| **MsgModDirs** | `msgmoddirs` | Store articles in N/ subdirs (e.g. 1000/) |
| **PostCommand** | `postcommand` | Command after article added; args passed directly (no shell); `-` = none |

gonewsd also accepts deprecated names: **LogFile** → use ErrorLog; **Newshostname** → use ServerName.

## 🔐 Auth: different model (by design)

newsd.conf uses simple single-user auth:

- **Auth.User** – username
- **Auth.Pass** – password
- **Auth.Protect** – groups to protect
- **Auth.Sleep** – sleep on failed auth

gonewsd uses **SQLite-backed multi-user auth** instead:

- **auth.mode** – `public` / `private` / `read_public_write_private`
- **auth.db** – path to SQLite auth database (users, groups, group_membership)
- **auth.log** – path for auth event log (optional)

There is no equivalent of Auth.User/Auth.Pass/Auth.Protect/Auth.Sleep in gonewsd; use `gonewsd adduser` / `addgroup` / etc. and the auth DB.

## 📌 gonewsd-only

- **pidfile** – path for PID file (used for SIGHUP reload from CLI)

## ☑️ Checklist vs newsd.conf.pod

Every directive documented in **newsd.conf.pod** is implemented in gonewsd as follows:

| .pod directive | Implemented | gonewsd / notes |
|----------------|-------------|------------------|
| ErrorLog | ✓ | `errorlog` – stderr, syslog, \|program, pathname |
| HostnameLookups | ✓ | `hostnamelookups` – off / on / double |
| Listen | ✓ | `listen` – address and/or port, or service name (e.g. nntp) |
| LogLevel | ✓ | `loglevel` – error / info / debug |
| MaxClients | ✓ | `maxclients` – 0 = no limit |
| MaxLogSize | ✓ | `maxlogsize` – bytes; k/m/g; 0 disables |
| ServerName | ✓ | `servername` |
| SendMail | ✓ | `sendmail` |
| SpoolDir | ✓ | `spooldir` |
| Timeout | ✓ | `timeout` – seconds; 0 disables |
| User | ✓ | `user` - run-as user |
| Auth.User / Auth.Pass / Auth.Protect / Auth.Sleep | -- | Replaced by `auth.mode`, `auth.db`, `auth.log` (SQLite multi-user) |
| NoRecurseMsgDir | ✓ | `norecursemsgdir` |
| MsgModDirs | ✓ | `msgmoddirs` |
| PostCommand | ✓ | `postcommand` – program invoked with groupname, article#, path (same order as newsd) |

**Not in .pod but in newsd.conf / gonewsd:** ErrorLog.Hex, SpamFilter (both implemented in gonewsd).

## 📋 Summary

All directives from **newsd.conf** and **newsd.conf.pod** that apply to gonewsd are implemented, except the old auth block (Auth.User/Pass/Protect/Sleep), which is replaced by auth.mode, auth.db, and auth.log.

A commented config template **gonewsd.conf** is provided in the project root for use as a starting point.
