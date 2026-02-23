# goNewsD
goNewsD -- Standalone Local [NNTP News Server](https://en.wikipedia.org/wiki/Network_News_Transfer_Protocol)



## 📖 WHAT IS GONEWSD?

Gonewsd is a standalone local **NNTP news server** for private newsgroups
(not part of Usenet). It serves one or more groups over an intranet or
the Internet. Clients post and read via NNTP; you can also inject
messages by email using the mailgateway command. Articles are stored
as plain text--one file per article, one directory per newsgroup.

Gonewsd does not interface with other Usenet news servers and cannot
act as a Usenet node. It is a single server for local newsgroups only:
NNTP clients connect to it to read and post; it does not feed or pull
from other sites. That keeps the design simple and local.


## ✨ FEATURES

• File-based and easy to run
- Plain files for configuration, articles, and (optionally) auth
- No database server or extra services--single binary and config file

• Easy administration via CLI
- Commands: `adduser, listuser, addgroup, listgroup, deleteuser, updategroup,` etc.
- Use flags for scripting, or run interactively and get prompted for missing options
- Optional `admin-cli.sh` provides a menu-driven interface

• Simple storage and logging
- One file per article, one directory per newsgroup under the spool
- Single key-value config file; auth (when enabled) uses one SQLite database
- Logs to file, stderr, syslog, or pipe--inspect and back up with normal file tools

## 🔨 BUILD INSTRUCTIONS

Gonewsd uses Task (https://taskfile.dev) for build and install.
Ensure Go is installed (https://go.dev/dl/) and Task is available:

    go install github.com/go-task/task/v3/cmd/task@latest

Build the binary:

    task build
    # or (equivalent):
    ./build.sh

Or build manually:

    go build -o gonewsd ./cmd/gonewsd

Clean built artifacts:

    task clean

To build a static binary (no CGO):

    CGO_ENABLED=0 go build -ldflags="-s -w" -o gonewsd ./cmd/gonewsd

## 📥 INSTALL INSTRUCTIONS

Once the software builds (see above), install the binary to /usr/local/bin:

    sudo cp bin/gonewsd /usr/local/bin/
    sudo cp gonewsdadm /usr/local/bin/

For more detail (config file, spool dir, systemd, etc.), see
[manuals/INSTALL.md](manuals/INSTALL.md).

Then create and edit your config file (e.g. /etc/gonewsd.conf) with
SpoolDir, Listen, User, ErrorLog, and other options. See also the embedded help for config details:

    gonewsd help

NOTE: By default, the config file's 'User' option may force gonewsd
        to run as a non-root user (e.g. 'news').

        If that user doesn't exist, either change the setting to
        an existing user account, or create the news account
        (Ubuntu/Debian: sudo adduser news).

Run in foreground with debug to verify settings:

    gonewsd -d -c /etc/gonewsd.conf

Or run as a daemon (logs to file if ErrorLog is set in config):

    gonewsd -c /etc/gonewsd.conf

### After install
1. Create newsgroups first using `addgroup` (e.g., `gonewsd addgroup -group my.news -g rw -o r -desc "My newsgroup"`).
2. Then, if using auth (`auth.db` set), create users with
`adduser` and assign them to groups. Alternatively, you can make groups
world readable and writable (e.g. `addgroup -g rw -o rw`); then no users
need to be created and everyone can read and post without logging in.

## 🚀 Make gonewsd a system service

See the section "Using gonewsd as a system service (Ubuntu)" below,
or the bootscripts/linux/README.md file for systemd and SysV init
options.

### ⚙️ USING GONEWSD AS A SYSTEM SERVICE (Ubuntu)
See [manuals/INSTALL.md](manuals/INSTALL.md) for detailed system service installation instructions on Ubuntu (and more).
    ```

## 🔐 AUTH (PERMISSIONS)

When `auth.db` is set, access is controlled like Unix file permissions on
a folder: each newsgroup is like a directory; you define who can read (r)
and who can write (post) (w) for that group.

- **Group permission (group_perm)**: 
    applies to users who are members of that
    group (adduser -groups ...). Values: r (read only), w (write only), rw
    (read and post).
- **World permission (world_perm)**: 
    applies to everyone else (including
    unauthenticated clients if auth.mode allows). Same values: r, w, rw.


> **Example:**  
> `addgroup -group my.group -g rw -o r -desc "My group"`  
> gives members **rw** (read & write) and non-members **r** (read-only).  
> Additional options: `-creator`, `-postlimit`, `-ccpost`, `-replyto`, `-voidemail`.
>  
> Use:  
> `-g rw -o rw`  
> to make the group world-readable and writable (no login required).

### auth.mode ### 

*public, private, read_public_write_private* sets the default when a group has no ACL: 

- public = everyone can read and post
- private = only authenticated group members
- read_public_write_private = anyone can read, only members can post. 

See manuals/CONFIGURATION.md and manuals/ACL_DB.md.

## 🛠️ ADMIN COMMANDS (AUTH)

Admin commands require a config file (`-c`) and an auth.db. If `-c` is not specified, gonewsd will look for the config file at `/etc/gonewsd.conf` by default.

Examples:

    gonewsd adduser
    gonewsd listuser -format pretty
    gonewsd listuser -format json
    gonewsd listgroup -format json


listuser / listgroup -format:

    -format pretty   ASCII box-drawing table (default: tab-separated).
    -format json     JSON array; useful for scripting or APIs.

User management commands: 

    adduser, listuser, deleteuser, updateuser

Group ACL management commands: 

    addgroup, deletegroup, updategroup, listgroup

Full usage: 

    gonewsd help

### USING ADMIN-CLI

admin-cli.sh is an interactive menu-driven wrapper for gonewsd auth
commands. Run it from the gonewsd repo root; it uses the same config
and auth.db as gonewsd.

From the project root:

    ./admin-cli.sh

You can override the config and binary location with environment
variables:

    CONFIG=/etc/gonewsd.conf ./admin-cli.sh
    BINDIR=/usr/local/bin CONFIG=/etc/gonewsd.conf ./admin-cli.sh

The menu offers:

    1) listuser    - list users and their groups
    2) adduser     - add a user (email, password, groups)
    3) deleteuser  - remove a user
    4) updateuser  - update user password or groups
    5) listgroup   - list groups and permissions
    6) addgroup    - add a newsgroup ACL
    7) deletegroup - delete or archive a group
    8) updategroup - update group permissions or archived
    m) show menu   
    q) quit

Each option runs the corresponding gonewsd subcommand; prompts behave
the same as when running gonewsd adduser, gonewsd addgroup, etc.
directly. Build gonewsd first (e.g. task build or go build into bin/).

### CREATING NEWSGROUPS

To create new newsgroups, use the `addgroup` admin command:

    gonewsd addgroup -group my.newsgroup -g rw -o r -desc "Description"

Additional options: `-creator`, `-postlimit`, `-ccpost`, `-replyto`, `-voidemail`.
If you omit `-group` and stdin is a TTY, you will be prompted for all fields.

Once a new group is created, any news clients should immediately be able to subscribe to the new group and if posting is enabled, post messages to it.

You can also create group directories and `.config` files manually; see bootscripts or existing spool layout for `.config` format (description, creator, postlimit, ccpost, replyto, voidemail). Posting is controlled by auth/ACL, not by `.config`.

## 📧 MAIL GATEWAY (not tested)

Email messages can be injected into the gonewsd groups
using e.g.

    cat email_message | ./gonewsd mailgateway rush.general

..which would add the text contents of email_message to
the newsgroup 'rush.general'.

For each email address to be used as a gateway to a group,
configure `gonewsd mailgateway <GROUP_NAME>` as the mail
forward command (e.g. in `/etc/aliases`). Ensure gonewsd is
setuid root or run by the MTA if required for pipe delivery.

## 📚 DOCUMENTATION

Embedded help (no separate man page). This will shows usage, options, and administration:

    gonewsd help

Version and copyright:

    gonewsd version

For more detailed documentation, see the [manuals/](manuals/) directory:
- [INSTALL.md](manuals/INSTALL.md) -- Installation and setup
- [USAGE.md](manuals/USAGE.md) -- How to use gonewsd and gonewsdadm
- [CONFIGURATION.md](manuals/CONFIGURATION.md) -- Configuration reference
- [ACL_DB.md](manuals/ACL_DB.md) -- Auth database schema
- [CONFIG-COMPAT.md](manuals/CONFIG-COMPAT.md) -- newsd compatibility notes

## 🆕 Improvements over newsd

**Authentication and access control:**
- Multi-user SQLite authentication (vs single-user plaintext)
- Per-group ACL (group_perm / world_perm)
- Group metadata fields (description, creator, postlimit, ccpost, replyto, voidemail)
- Auth logging (auth.log)
- AUTHINFO SIMPLE (legacy auth support)

**NNTP protocol features not in `newsd`:**
- CAPABILITIES (RFC 3977 mandatory discovery)
- LAST (backward article navigation)
- CANCEL (article deletion via control messages, with From: validation)
- OVER / HDR (RFC 3977 standard replacements for XOVER / XHDR)
- XHDR (header field retrieval by range or message-ID)
- NEWNEWS (list new articles since a date)
- NEWGROUPS date filtering (correctly filters by creation time)
- LIST ACTIVE wildmat pattern matching
- LIST HEADERS
- LISTGROUP range argument
- XPAT (RFC 2980 -- pattern matching against header fields)
- LIST COUNTS / LIST DISTRIBUTIONS / LIST DISTRIB.PATS
- STARTTLS (TLS connection upgrade, RFC 4642)
- COMPRESS DEFLATE (connection compression, RFC 8054)

**Security and reliability:**
- Input validation (group names, email addresses, post size limits)
- PostCommand executed without shell (prevents injection)
- Path traversal and header injection protections


## 🔒 TLS (STARTTLS)

gonewsd supports STARTTLS (RFC 4642) to upgrade plaintext connections to TLS.
To enable it, add `tls.cert` and `tls.key` to your config file pointing to
PEM-format certificate and key files:

    tls.cert  /etc/gonewsd/server.crt
    tls.key   /etc/gonewsd/server.key

When both are set, the server advertises STARTTLS in CAPABILITIES and clients
can issue the STARTTLS command to upgrade. If not configured, the server
responds `580 TLS not available`.

You can generate a self-signed certificate for testing with:

    openssl req -x509 -newkey rsa:2048 -keyout server.key -out server.crt \
        -days 365 -nodes -subj "/CN=localhost"

See [manuals/CONFIGURATION.md](manuals/CONFIGURATION.md) for details.

**Note:** STARTTLS has not been tested end-to-end with a real NNTP client.

## 🗜️ COMPRESS DEFLATE

gonewsd supports COMPRESS DEFLATE (RFC 8054) to compress data on the
connection after negotiation. Clients that support it can send
`COMPRESS DEFLATE` after connecting, and all subsequent traffic is
compressed in both directions using zlib/DEFLATE.

This is always available -- no configuration required. The server advertises
it in CAPABILITIES and LIST EXTENSIONS.

**Note:** COMPRESS DEFLATE has not been tested end-to-end with a real NNTP client.

## ⚠️ LIMITATIONS

By design, gonewsd does NOT manage Usenet news feeds. It acts as a
private news server only. There is no way to constrain posting or
readership by IP or domain.

**Not implemented (by design -- not a news feed):**
- IHAVE (RFC 977, 3977) -- peer article transfer
- CHECK / TAKETHIS (RFC 3977) -- streaming article transfer
- MODE STREAM (RFC 3977) -- streaming mode
- XREPLIC (legacy) -- article replication

**Not implemented (deprecated):**
- AUTHINFO GENERIC (RFC 4643) -- deprecated; returns 501 with notice to use AUTHINFO USER/PASS

See [notes/NNTP-UNIMPLEMENTED.md](notes/NNTP-UNIMPLEMENTED.md) for
the full protocol implementation status.


## 🐛 REPORTING BUGS

Please use your project's issue page:

https://github.com/runableapp/gonewsd/issues


## 📄 LICENSING

<img src="https://github.com/user-attachments/assets/22de0187-eee8-46b2-aad0-3a34f9ad7cb3" alt="runable-dad-frame-small" width="180">


Copyright © 2026 Runable.app. GPL-3.0.

Gonewsd is free software under the GNU General Public License v3.
See the LICENSE file for full terms.

### CREDIT

Gonewsd is a Go port of **newsd**, the standalone local NNTP news server.
Original newsd (C language) by Greg Ercolano and Michael Sweet: https://github.com/erco77/newsd
