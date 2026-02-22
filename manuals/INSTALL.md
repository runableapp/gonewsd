# Installing and running gonewsd

gonewsd is a standalone local NNTP news server (Go port of newsd). It serves private newsgroups over NNTP and can receive posts via NNTP or via the **mailgateway** command from email.

This document describes how to build, install, and run gonewsd -- including **configuring it to start on boot** using the provided systemd service template or SysV init script.



## 🔨 Build

From the gonewsd directory:

```bash
./build.sh # this calls task build
# or,
task build
# or,
go build -o gonewsd ./cmd/gonewsd
```

This produces a static binary `gonewsd`. For a static build with CGO disabled:

```bash
CGO_ENABLED=0 go build -ldflags="-s -w" -o gonewsd ./cmd/gonewsd
```


## 📥 Install

### Automatic install (Ubuntu/Debian with systemd)

For a quick install, use the provided script:

```bash
task build
sudo ./install-ubuntu-service.sh
```

This script:
- Creates the `usenet` system user (if needed)
- Installs `gonewsd` and `gonewsdadm` to `/usr/local/bin`
- Creates required directories (`/var/spool/gonewsd`, `/var/lib/gonewsd`, `/var/log/gonewsd`)
- Installs a default config to `/etc/gonewsd.conf`
- Installs and enables the systemd service

To use a different user, set `GONEWSD_USER`:
```bash
GONEWSD_USER=news sudo ./install-ubuntu-service.sh
```

### Manual install

1. **Copy the binary and admin script** to a directory in your PATH:
   ```bash
   sudo cp gonewsd /usr/local/bin/gonewsd
   sudo cp gonewsdadm /usr/local/bin/gonewsdadm
   sudo chmod 755 /usr/local/bin/gonewsd /usr/local/bin/gonewsdadm
   ```
   
   `gonewsdadm` is an interactive menu-driven admin CLI for managing users and groups.

2. **Create a config file** (or use the one from your build tree). A template **gonewsd.conf** is provided in the project root; copy it to e.g. `/etc/gonewsd.conf` and edit. Minimal example:
   ```
   ErrorLog        /var/log/gonewsd/gonewsd.log
   Listen          :119
   SpoolDir        /var/spool/gonewsd
   User            usenet
   auth.mode       public
   auth.db         /var/lib/gonewsd/auth.db
   auth.log        /var/log/gonewsd/auth.log
   pidfile         /run/gonewsd/gonewsd.pid
   ```
   For auth (adduser/addgroup etc.), set `auth.mode`, `auth.db`, and optionally `auth.log` and `pidfile` in the config. See **manuals/CONFIG-COMPAT.md** for all directives and **gonewsd.conf** for a commented template.

3. **Create required directories** and set ownership (assuming `usenet` user):

   ```bash
   # Spool directory (newsgroup data)
   sudo mkdir -p /var/spool/gonewsd
   sudo chown usenet:usenet /var/spool/gonewsd

   # Auth database directory
   sudo mkdir -p /var/lib/gonewsd
   sudo chown usenet:usenet /var/lib/gonewsd

   # Log directory
   sudo mkdir -p /var/log/gonewsd
   sudo chown usenet:usenet /var/log/gonewsd
   ```

   > **Note:** The PID file directory (`/run/gonewsd/`) is created automatically by systemd via `RuntimeDirectory=gonewsd` in the service file. No manual creation needed.

   If the `usenet` user does not exist, create it:
   ```bash
   sudo adduser --system --group --no-create-home usenet
   ```

   > **Important:** The user must be consistent across:
   > - **Config file** (`User usenet`) -- gonewsd drops privileges to this user
   > - **Systemd service** (`User=usenet`, `Group=usenet`) -- systemd runs gonewsd as this user
   > - **Directory ownership** -- all directories above must be owned by this user
   >
   > If running as a systemd service with `User=usenet`, the config `User` directive is optional (gonewsd is already running as that user). But keep them consistent to avoid permission issues.

4. **Test run** in the foreground with debug output:
   ```bash
   ./gonewsd -d -c <path to gonewsd.conf>
   ```
   Or run as a daemon (logs to the path set by `ErrorLog` in the config):
   ```bash
   ./gonewsd -c <path to gonewsd.conf>
   ```

   > **Note:** If your config file is already at `/etc/gonewsd.conf`, you can omit the `-c` option--gonewsd will find it automatically:
   > ```bash
   > ./gonewsd -d
   > ```
   > or
   > ```bash
   > ./gonewsd
   > ```


## 🚀 Configuring gonewsd to start on boot

gonewsd provides **service templates** so you can run it as a system daemon.

- **systemd** -- Use the systemd unit file (recommended on most Linux systems).
- **SysV init** -- Use the init script if you prefer traditional init or compatibility.

**See:** [bootscripts/linux/README.md](bootscripts/linux/README.md) for step-by-step instructions:

1. **systemd:** copy `bootscripts/linux/gonewsd.service` to `/etc/systemd/system/`, edit paths if needed, then `systemctl enable gonewsd` and `systemctl start gonewsd`.
2. **SysV init:** copy `bootscripts/linux/gonewsd-boot` to `/etc/init.d/gonewsd`, create rc*.d symlinks (or use `update-rc.d`), then `/etc/init.d/gonewsd start`.

That README also covers log rotation and restart after config changes.


## 📁 Creating newsgroups

To create new newsgroups, use the `addgroup` admin command:

```bash
sudo ./gonewsd addgroup -group my.newsgroup -g rw -o r -desc "My newsgroup" -c /etc/gonewsd.conf
```

Additional options:
- `-desc` - Group description
- `-creator` - Creator name (default: -)
- `-postlimit` - Max articles per posting (default: 1000)
- `-ccpost` - Email to CC posts to (default: -)
- `-replyto` - Reply-To header for posts (default: -)
- `-voidemail` - Email for bounce messages (default: root)

If you omit `-group` and stdin is a TTY, you will be prompted for all fields.
For more options (manual creation, test groups), see [USAGE.md](USAGE.md) for more details.



## 📧 Mail gateway

Email can be injected into a newsgroup with:

```bash
cat email_message | ./gonewsd mailgateway rush.general -c /etc/gonewsd.conf
```

Same usage as newsd. To test the mail gateway:

```bash
test-scripts/test-mailgateway.sh [groupname]
```

Default group is `test.group1`; ensure test groups exist (e.g. run `test-scripts/create-test-groups.sh` first).


## ⚙️ Config file

By default gonewsd looks for **`/etc/gonewsd.conf`**; use **`-c`** to specify another path. A commented example **gonewsd.conf** is in the project root--copy it to `/etc/gonewsd.conf` and edit.

For a full list of directives and default values, see **[CONFIGURATION.md](CONFIGURATION.md)**. For newsd compatibility notes, see [CONFIG-COMPAT.md](CONFIG-COMPAT.md). Quick reference:

| Option    | Meaning |
|----------|---------|
| ErrorLog | Log file path, or `stderr`, or `syslog`, or `\|command` |
| Listen   | Address:port (e.g. `:1119`), or service name (e.g. `nntp`) |
| SpoolDir | Newsgroup spool directory |
| User     | Run as this user (e.g. `news`) |
| auth.mode | `public`, `private`, or `read_public_write_private` (when using auth) |
| auth.db  | Path to SQLite auth database (required for adduser/addgroup etc.) |
| auth.log | Path for auth event log (optional) |
| pidfile  | Path for PID file (used for SIGHUP reload from CLI) |

See **gonewsd help** for command-line options.



## 🔧 Troubleshooting

### UTF-8 characters appear as ??? when posting

If non-ASCII characters (e.g., Korean, Japanese, Chinese) appear as `???` in posted articles, the issue is likely your **newsreader's charset configuration**, not gonewsd. The server handles UTF-8 correctly.

**For tin newsreader**, add these settings to `~/.tinrc`:
```
mm_charset=UTF-8
post_mime_encoding=8bit
post_8bit_header=ON
```

Then restart tin and try posting again.

**To verify gonewsd handles UTF-8 correctly**, use the included test tool:
```bash
NNTP_ADDR=127.0.0.1:119 AUTH_USER=user@example.com AUTH_PASS=yourpass ./bin/utf8test groupname
```

This posts Korean text and reads it back to confirm UTF-8 is preserved.


## 📋 Summary

| Task              | Command / doc |
|-------------------|----------------|
| Build             | `task build` (version from `VERSION.txt` in project root) or `go build -o gonewsd ./cmd/gonewsd` |
| Auto install      | `sudo ./install-ubuntu-service.sh` (Ubuntu/Debian) |
| Manual install    | Copy binary to e.g. `/usr/local/bin`, create config and spool |
| Config template   | **gonewsd.conf** in project root; see [CONFIGURATION.md](CONFIGURATION.md) and [CONFIG-COMPAT.md](CONFIG-COMPAT.md) |
| Auth DB schema    | [ACL_DB.md](ACL_DB.md) (users, groups, group_membership) |
| Start on boot     | [bootscripts/linux/README.md](../bootscripts/linux/README.md) (systemd or init script) |
| Create newsgroups| `gonewsd addgroup` -- See [USAGE.md](USAGE.md) |
| Mail gateway      | `cat email \| gonewsd mailgateway <group>` |
