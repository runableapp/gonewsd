# Configuring gonewsd to start on boot (Linux)

This directory provides service templates so you can run gonewsd as a system daemon. Choose **systemd** (recommended on most Linux systems) or the **SysV init** script.

Before enabling boot start, verify gonewsd runs correctly from a shell:

```bash
./gonewsd -d -c /etc/gonewsd.conf
```

Then use one of the methods below.

## 1️⃣ Option 1: systemd (recommended)

1. **Install the binary** (if not already):
   ```bash
   sudo cp gonewsd /usr/local/bin/gonewsd
   chmod 755 /usr/local/bin/gonewsd
   ```

2. **Copy the service file** and edit paths if needed:
   ```bash
   sudo cp bootscripts/linux/gonewsd.service /etc/systemd/system/
   # Edit ExecStart path/config if you use different paths:
   # sudo sed -i 's|/usr/local/bin/gonewsd|/path/to/gonewsd|' /etc/systemd/system/gonewsd.service
   # sudo sed -i 's|/etc/gonewsd.conf|/path/to/gonewsd.conf|' /etc/systemd/system/gonewsd.service
   ```

3. **Enable and start**:
   ```bash
   sudo systemctl daemon-reload
   sudo systemctl enable gonewsd
   sudo systemctl start gonewsd
   sudo systemctl status gonewsd
   ```

4. **Useful commands**:
   - `sudo systemctl stop gonewsd` -- stop
   - `sudo systemctl restart gonewsd` -- restart
   - `journalctl -u gonewsd -f` -- follow service log (if gonewsd logs to stderr and systemd captures it)

Your config file (`ErrorLog` in gonewsd.conf) may write to a file (e.g. `/var/log/gonewsd.log`). Check that path for application logs.

## 2️⃣ Option 2: SysV init script

If your system uses traditional init or you prefer the init script (systemd can also run init scripts):

1. **Copy the boot script** to `/etc/init.d/`:
   ```bash
   sudo cp bootscripts/linux/gonewsd-boot /etc/init.d/gonewsd
   sudo chmod 755 /etc/init.d/gonewsd
   ```

2. **Set paths** (optional; defaults are `/usr/local/bin/gonewsd` and `/etc/gonewsd.conf`):
   ```bash
   # Edit /etc/init.d/gonewsd and set GONEWSD= and GONEWSD_CONFIG= if needed
   ```

3. **Create start/stop links** (run levels 2–5 start, 0/1/6 stop):
   ```bash
   sudo ln -sf /etc/init.d/gonewsd /etc/rc0.d/K01gonewsd
   sudo ln -sf /etc/init.d/gonewsd /etc/rc1.d/K01gonewsd
   sudo ln -sf /etc/init.d/gonewsd /etc/rc6.d/K01gonewsd
   sudo ln -sf /etc/init.d/gonewsd /etc/rc2.d/S99gonewsd
   sudo ln -sf /etc/init.d/gonewsd /etc/rc3.d/S99gonewsd
   sudo ln -sf /etc/init.d/gonewsd /etc/rc4.d/S99gonewsd
   sudo ln -sf /etc/init.d/gonewsd /etc/rc5.d/S99gonewsd
   ```
   On Debian/Ubuntu you can instead run: `sudo update-rc.d gonewsd defaults`

4. **Start the daemon**:
   ```bash
   sudo /etc/init.d/gonewsd start
   ```

5. **Stop / restart**:
   ```bash
   sudo /etc/init.d/gonewsd stop
   sudo /etc/init.d/gonewsd restart
   ```

## 🔄 Log rotation

If gonewsd is configured to log to a file (e.g. `ErrorLog /var/log/gonewsd.log`), rotate it to avoid unbounded growth.

**Example for logrotate** -- create `/etc/logrotate.d/gonewsd`:

```
/var/log/gonewsd.log {
    rotate 5
    weekly
    missingok
    postrotate
        systemctl reload gonewsd 2>/dev/null || /etc/init.d/gonewsd restart
    endscript
}
```

Or rotate manually on a schedule:

```bash
# Rotate and then restart so gonewsd reopens the log
/usr/local/bin/gonewsd rotate -c /etc/gonewsd.conf
sudo systemctl restart gonewsd   # or: sudo /etc/init.d/gonewsd restart
```

## 📋 Summary

| Method   | Service file / script      | Enable boot start                    |
|----------|-----------------------------|--------------------------------------|
| systemd  | `gonewsd.service`           | `systemctl enable gonewsd`          |
| SysV init| `gonewsd-boot` → init.d    | rc*.d symlinks or `update-rc.d`      |

After this, create newsgroups with `gonewsd addgroup` (see [manuals/USAGE.md](../../manuals/USAGE.md)) and use your NNTP client to connect.
