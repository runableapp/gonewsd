# Usage Guide

This guide covers day-to-day administration of gonewsd: starting the server, managing users and groups, using the interactive admin CLI (`gonewsdadm`), configuring the mail gateway, and rotating logs.

For installation and build instructions, see [INSTALL.md](INSTALL.md).
For config directives, see [CONFIGURATION.md](CONFIGURATION.md).
For the auth DB schema, see [ACL_DB.md](ACL_DB.md).


## 📖 Quick reference

| Task | Command |
|------|---------|
| Show help | `gonewsd help` |
| Show version | `gonewsd version` |
| Start server (foreground) | `gonewsd -c /etc/gonewsd.conf` |
| Start server (debug) | `gonewsd -d -c /etc/gonewsd.conf` |
| Start server (background) | `gonewsd -b -c /etc/gonewsd.conf` |
| Interactive admin CLI | `sudo gonewsdadm` |
| Add a user | `gonewsd adduser -c /etc/gonewsd.conf` |
| List users | `gonewsd listuser -c /etc/gonewsd.conf` |
| Add a group | `gonewsd addgroup -c /etc/gonewsd.conf` |
| List groups | `gonewsd listgroup -c /etc/gonewsd.conf` |
| Inject email into group | `cat email.txt \| gonewsd mailgateway my.group -c /etc/gonewsd.conf` |
| Rotate logs | `gonewsd rotate -c /etc/gonewsd.conf` |


## 🖥️ Starting the server

gonewsd runs in the foreground by default. If your config is at `/etc/gonewsd.conf`, the `-c` flag can be omitted.

```bash
# Foreground (default)
gonewsd

# Foreground with debug output (verbose logging to stderr)
gonewsd -d

# With a custom config file
gonewsd -c /path/to/gonewsd.conf

# Background (fork/daemonize) -- not recommended on systemd
gonewsd -b
```

When running as a systemd service, the service file handles starting and stopping:

```bash
sudo systemctl start gonewsd
sudo systemctl stop gonewsd
sudo systemctl restart gonewsd
sudo systemctl status gonewsd
```

### Signals

- **SIGHUP** -- reload the auth database (users and groups) without restarting. This happens automatically when you use admin commands (`adduser`, `addgroup`, etc.) if `pidfile` is configured.
- **SIGINT / SIGTERM** -- graceful shutdown.


## 🔧 Interactive admin CLI (gonewsdadm)

`gonewsdadm` is a menu-driven shell for managing users and groups. It wraps gonewsd's admin subcommands in an interactive interface.

```bash
sudo gonewsdadm
```

> **Note:** Run with `sudo` (or as the service user) so it has write access to `auth.db`.

The menu:

```
  1) listuser    - list users and their groups
  2) adduser     - add a user (email, password, realname, groups)
  3) deleteuser  - remove a user
  4) updateuser  - update user password, realname, or groups
  5) listgroup   - list groups and permissions
  6) addgroup    - add a newsgroup
  7) deletegroup - delete or archive a group
  8) updategroup - update group permissions or archived
  m) show this menu
  q) quit
```

Each option runs the corresponding gonewsd subcommand interactively. You will be prompted for all required fields if not already provided.


## 👥 Managing users

Users are stored in the `auth.db` SQLite database. Passwords are bcrypt-hashed. Each user has an email address (username), an optional real name (display name), and belongs to one or more groups.

When authenticated, the server validates that the `From:` header in any POST matches the user's email address. This prevents impersonation.

### Add a user

**Interactive (prompted for all fields):**

```bash
gonewsd adduser -c /etc/gonewsd.conf
```

You will be asked for:
1. **Email** -- must be a valid email address (e.g. `user@example.com`)
2. **Password** -- 9-20 characters, letters/digits/special characters, no spaces
3. **Real name** -- optional display name (e.g. `John Doe`); press Enter to skip
4. **Groups** -- comma-separated group names, or `*` for all groups

**Non-interactive (all flags):**

```bash
gonewsd adduser -user user@example.com -pass "secretPass1" -realname "John Doe" -groups "my.group,other.group" -c /etc/gonewsd.conf
```

Or assign to all groups:

```bash
gonewsd adduser -user admin@example.com -pass "adminPass1!" -realname "Admin" -groups "*" -c /etc/gonewsd.conf
```

### List users

```bash
# Default (tab-separated)
gonewsd listuser -c /etc/gonewsd.conf

# Pretty ASCII table
gonewsd listuser -format pretty -c /etc/gonewsd.conf

# JSON
gonewsd listuser -format json -c /etc/gonewsd.conf
```

### Update a user

Change a user's password, real name, groups, or any combination. Omitted fields are left unchanged.

**Interactive:**

```bash
gonewsd updateuser -c /etc/gonewsd.conf
```

**Non-interactive:**

```bash
# Change password only
gonewsd updateuser -user user@example.com -pass "newPass123" -c /etc/gonewsd.conf

# Change real name only
gonewsd updateuser -user user@example.com -realname "New Name" -c /etc/gonewsd.conf

# Change groups only
gonewsd updateuser -user user@example.com -groups "group.a,group.b" -c /etc/gonewsd.conf

# Change multiple fields
gonewsd updateuser -user user@example.com -pass "newPass123" -realname "New Name" -groups "*" -c /etc/gonewsd.conf
```

### Delete a user

```bash
# Interactive (asks for confirmation)
gonewsd deleteuser -user user@example.com -c /etc/gonewsd.conf

# Skip confirmation
gonewsd deleteuser -user user@example.com -y -c /etc/gonewsd.conf
```


## 📰 Managing groups

Newsgroups are stored in both the `auth.db` (permissions, metadata) and the spool directory (article files). When you create a group with `addgroup`, both are set up automatically.

### Add a group

**Interactive (prompted for all fields):**

```bash
gonewsd addgroup -c /etc/gonewsd.conf
```

You will be asked for:
1. **Group name** -- dotted notation (e.g. `myorg.general`, `myorg.dev.golang`)
2. **Group permission** -- `r` (read), `w` (write), or `rw` (read+write) for group members
3. **World permission** -- `r`, `w`, or `rw` for non-members (and unauthenticated users)
4. **Description** -- short text describing the group
5. **Creator** -- who created the group (default: `-`)
6. **Post limit** -- max lines per article (default: `1000`, `0` = unlimited)
7. **CC post** -- email to CC all posts to (default: `-` for none)
8. **Reply-To** -- Reply-To header for posts (default: `-` for none)
9. **Void email** -- email for bounce messages (default: `root`)

**Non-interactive (with flags):**

```bash
gonewsd addgroup \
  -group myorg.general \
  -g rw \
  -o r \
  -desc "General discussion" \
  -creator "admin" \
  -postlimit 2000 \
  -c /etc/gonewsd.conf
```

### Understanding permissions

Permissions control who can read and post to each group:

| Permission | Meaning |
|------------|---------|
| `r` | Read only (can browse and read articles) |
| `w` | Write only (can post but not read) |
| `rw` | Read and write |

Two levels of permission are set per group:

- **Group permission (`-g`)** -- applies to authenticated users who are members of the group
- **World permission (`-o`)** -- applies to everyone else (non-members and unauthenticated users)

These interact with the server's `auth.mode` setting:

| auth.mode | Behavior |
|-----------|----------|
| `public` | No auth required. Everyone uses world permission. |
| `private` | Auth required for all access. Members use group permission; non-members use world permission. |
| `read_public_write_private` | Anyone can read. Only authenticated group members can post. |

**Example scenarios:**

| Group perm | World perm | Effect |
|------------|------------|--------|
| `rw` | `r` | Members can read+post; everyone else can only read |
| `rw` | `rw` | Everyone can read+post (open group) |
| `rw` | `w` | Members can read+post; non-members can only post |
| `r` | `r` | Read-only for everyone (announcement group) |

### List groups

```bash
# Default (vertical block per group)
gonewsd listgroup -c /etc/gonewsd.conf

# Pretty ASCII table
gonewsd listgroup -format pretty -c /etc/gonewsd.conf

# JSON
gonewsd listgroup -format json -c /etc/gonewsd.conf

# Tab-separated
gonewsd listgroup -format tsv -c /etc/gonewsd.conf
```

### Update a group

Change a group's permissions, metadata, or archived status. Omitted fields are left unchanged.

**Interactive:**

```bash
gonewsd updategroup -c /etc/gonewsd.conf
```

**Non-interactive:**

```bash
# Change world permission to read-only
gonewsd updategroup -group myorg.general -o r -c /etc/gonewsd.conf

# Update description and post limit
gonewsd updategroup -group myorg.general -desc "Updated description" -postlimit 5000 -c /etc/gonewsd.conf
```

### Delete a group

Deleting a group removes it from the auth DB and deletes the spool directory (articles).

```bash
# Interactive (asks for confirmation)
gonewsd deletegroup -group myorg.old -c /etc/gonewsd.conf

# Skip confirmation
gonewsd deletegroup -group myorg.old -y -c /etc/gonewsd.conf
```

### Archive a group

Archiving hides a group from LIST but preserves its data. Archived groups cannot be accessed by clients.

```bash
gonewsd deletegroup -group myorg.old -archive -c /etc/gonewsd.conf
```

To restore an archived group:

```bash
gonewsd updategroup -group myorg.old -archive=false -c /etc/gonewsd.conf
```


## 📧 Mail gateway

The mail gateway injects email messages into a newsgroup. This is useful for piping mailing list traffic into a group.

### Basic usage

```bash
cat email_message.txt | gonewsd mailgateway myorg.general -c /etc/gonewsd.conf
```

### Setting up with /etc/aliases

1. Set the `replyto` field when creating the group:

   ```bash
   gonewsd addgroup -group myorg.general -replyto myorg.general@yourdomain.com -c /etc/gonewsd.conf
   ```

2. Add an alias in `/etc/aliases`:

   ```
   myorg.general: "|/usr/local/bin/gonewsd mailgateway myorg.general -c /etc/gonewsd.conf"
   ```

3. Run `newaliases` to apply:

   ```bash
   sudo newaliases
   ```

4. Send a test email to `myorg.general@yourdomain.com` and verify it appears in the group.

### Options

- `-preserve-date` -- keep the original `Date:` header from the email instead of rewriting it:

  ```bash
  cat email.txt | gonewsd mailgateway -preserve-date myorg.general -c /etc/gonewsd.conf
  ```

### CC posts

If a group's `ccpost` field is set to an email address, every post to that group (including those received via the mail gateway) will be CC'd to that address.

```bash
gonewsd addgroup -group myorg.announce -ccpost admin@example.com -c /etc/gonewsd.conf
```


## 🔄 Log rotation

gonewsd can automatically rotate its log file when it exceeds `MaxLogSize` (configured in `gonewsd.conf`). You can also force a rotation:

```bash
gonewsd rotate -c /etc/gonewsd.conf
```

The rotated log is renamed with a `.1` suffix. Configure the max size:

```
MaxLogSize  5m
```

Set `MaxLogSize 0` to disable automatic rotation (useful if you use an external log rotation tool like `logrotate`).


## 🔑 Auth database reload

When you add or modify users/groups via the admin CLI, gonewsd automatically sends a `SIGHUP` to the running server process (if `pidfile` is configured) to reload the auth database. No restart needed.

If you modify the auth database directly (e.g. with `sqlite3`), send `SIGHUP` manually:

```bash
kill -HUP $(cat /run/gonewsd/gonewsd.pid)
```


## 📋 Typical setup walkthrough

Here is a complete example of setting up gonewsd from scratch:

### 1. Install

```bash
task build
sudo ./install-ubuntu-service.sh
```

### 2. Create groups

```bash
sudo gonewsd addgroup -group myorg.general -g rw -o r -desc "General discussion" -c /etc/gonewsd.conf
sudo gonewsd addgroup -group myorg.dev -g rw -o r -desc "Development" -c /etc/gonewsd.conf
sudo gonewsd addgroup -group myorg.announce -g rw -o r -desc "Announcements" -c /etc/gonewsd.conf
```

### 3. Create users

```bash
sudo gonewsd adduser -user alice@example.com -pass "alicePass1!" -realname "Alice" -groups "*" -c /etc/gonewsd.conf
sudo gonewsd adduser -user bob@example.com -pass "bobSecure2@" -realname "Bob" -groups "myorg.general,myorg.dev" -c /etc/gonewsd.conf
```

### 4. Start the server

```bash
sudo systemctl start gonewsd
```

### 5. Connect with a newsreader

Configure your NNTP client (e.g. Thunderbird, tin) to connect to your server on port 119 (or whatever `Listen` is set to). Use the email/password you created to authenticate.

**For tin:**

```bash
tin -r -g your-server-hostname
```

Add credentials to `~/.newsauth`:

```
your-server-hostname alice@example.com alicePass1!
```

### 6. Verify

```bash
# Check server is running
sudo systemctl status gonewsd

# Check groups exist
sudo gonewsd listgroup -format pretty -c /etc/gonewsd.conf

# Check users exist
sudo gonewsd listuser -format pretty -c /etc/gonewsd.conf

# Check auth log
tail /var/log/gonewsd/auth.log
```


## 🔧 Troubleshooting

### UTF-8 characters appear as ??? when posting

The issue is your newsreader's charset configuration, not gonewsd. The server handles UTF-8 correctly.

**For tin newsreader**, add these settings to `~/.tinrc`:

```
mm_charset=UTF-8
post_mime_encoding=8bit
post_8bit_header=ON
```

Then restart tin and try posting again.

### "servers active-file contains no newsgroups"

Make sure you have created at least one group with `gonewsd addgroup`. The server only lists groups that exist in the auth DB.

### Permission denied errors

Check that:
- The spool, lib, and log directories are owned by the configured user (e.g. `usenet`)
- The `User` directive in the config matches the directory ownership
- If using systemd, `User=` in the service file matches

```bash
ls -la /var/spool/gonewsd /var/lib/gonewsd /var/log/gonewsd
```

### Authentication fails on first attempt

Some newsreaders (e.g. tin) may not send credentials on the first connection attempt. Quit and reconnect -- the client typically caches the credentials after the first prompt. Make sure `~/.newsauth` is set up for automated authentication.
