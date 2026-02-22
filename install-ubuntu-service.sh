#!/usr/bin/env bash
#
# Copyright © 2026 Runable.app. GPL-3.0.
#
# Automated install script for gonewsd on Ubuntu/Debian with systemd.
# Run as root or with sudo.
#
set -e

# Configuration
# 'usenet' is the default user for the gonewsd service.
# If you want to use a different user, set the GONEWSD_USER environment variable.
# For example:
#   GONEWSD_USER=news sudo ./install-ubuntu-service.sh
# will create the 'news' user and use it for the gonewsd service.
INSTALL_USER="${GONEWSD_USER:-usenet}"
INSTALL_BIN="/usr/local/bin"
CONFIG_FILE="/etc/gonewsd.conf"
SPOOL_DIR="/var/spool/gonewsd"
LIB_DIR="/var/lib/gonewsd"
LOG_DIR="/var/log/gonewsd"
SERVICE_FILE="/etc/systemd/system/gonewsd.service"

# Colors for output
RED='\033[0;31m'
GREEN='\033[0;32m'
YELLOW='\033[1;33m'
NC='\033[0m' # No Color

info()  { echo -e "${GREEN}[INFO]${NC} $1"; }
warn()  { echo -e "${YELLOW}[WARN]${NC} $1"; }
error() { echo -e "${RED}[ERROR]${NC} $1" >&2; exit 1; }

# Check if running as root
if [[ $EUID -ne 0 ]]; then
  error "This script must be run as root (use sudo)"
fi

# Find binaries (works from project root or extracted zip)
SCRIPT_DIR="$(cd "$(dirname "$0")" && pwd)"

# Look for gonewsd: same dir (zip) or bin/ subdir (project)
if [[ -f "$SCRIPT_DIR/gonewsd" ]]; then
  GONEWSD_BIN="$SCRIPT_DIR/gonewsd"
elif [[ -f "$SCRIPT_DIR/bin/gonewsd" ]]; then
  GONEWSD_BIN="$SCRIPT_DIR/bin/gonewsd"
else
  error "gonewsd not found. Build first (task build) or extract from zip."
fi

# Look for gonewsdadm: same dir (zip) or project root
if [[ -f "$SCRIPT_DIR/gonewsdadm" ]]; then
  GONEWSADM_BIN="$SCRIPT_DIR/gonewsdadm"
else
  error "gonewsdadm not found."
fi

# Look for service file: same dir (zip) or bootscripts/linux/ (project)
if [[ -f "$SCRIPT_DIR/gonewsd.service" ]]; then
  SERVICE_SRC="$SCRIPT_DIR/gonewsd.service"
elif [[ -f "$SCRIPT_DIR/bootscripts/linux/gonewsd.service" ]]; then
  SERVICE_SRC="$SCRIPT_DIR/bootscripts/linux/gonewsd.service"
else
  error "gonewsd.service not found."
fi

echo ""
echo "  ██████╗  ██████╗ ███╗   ██╗███████╗██╗    ██╗███████╗██████╗ "
echo " ██╔════╝ ██╔═══██╗████╗  ██║██╔════╝██║    ██║██╔════╝██╔══██╗"
echo " ██║  ███╗██║   ██║██╔██╗ ██║█████╗  ██║ █╗ ██║███████╗██║  ██║"
echo " ██║   ██║██║   ██║██║╚██╗██║██╔══╝  ██║███╗██║╚════██║██║  ██║"
echo " ╚██████╔╝╚██████╔╝██║ ╚████║███████╗╚███╔███╔╝███████║██████╔╝"
echo "  ╚═════╝  ╚═════╝ ╚═╝  ╚═══╝╚══════╝ ╚══╝╚══╝ ╚══════╝╚═════╝ "
echo ""
echo "gonewsd Ubuntu/Debian Service Installer"
echo ""

# Step 1: Create system user if needed
info "Checking system user '$INSTALL_USER'..."
if id "$INSTALL_USER" &>/dev/null; then
  info "User '$INSTALL_USER' already exists"
else
  info "Creating system user '$INSTALL_USER'..."
  adduser --system --group --no-create-home "$INSTALL_USER"
fi

# Step 2: Install binaries
info "Installing binaries to $INSTALL_BIN..."
install -m 755 "$GONEWSD_BIN" "$INSTALL_BIN/gonewsd"
install -m 755 "$GONEWSADM_BIN" "$INSTALL_BIN/gonewsdadm"
info "Installed: $INSTALL_BIN/gonewsd, $INSTALL_BIN/gonewsdadm"

# Step 3: Create directories
info "Creating directories..."

mkdir -p "$SPOOL_DIR"
chown "$INSTALL_USER:$INSTALL_USER" "$SPOOL_DIR"
info "Created $SPOOL_DIR"

mkdir -p "$LIB_DIR"
chown "$INSTALL_USER:$INSTALL_USER" "$LIB_DIR"
info "Created $LIB_DIR"

mkdir -p "$LOG_DIR"
chown "$INSTALL_USER:$INSTALL_USER" "$LOG_DIR"
info "Created $LOG_DIR"

# Step 4: Install config file (if not exists)
if [[ -f "$CONFIG_FILE" ]]; then
  warn "Config file $CONFIG_FILE already exists, not overwriting"
else
  info "Creating config file $CONFIG_FILE..."
  cat > "$CONFIG_FILE" << 'EOF'
# gonewsd configuration
# See manuals/CONFIGURATION.md for all directives

ErrorLog        /var/log/gonewsd/gonewsd.log
Listen          :119
SpoolDir        /var/spool/gonewsd
User            usenet

# Log level: error, info, debug
LogLevel        info

# Authentication (optional)
auth.mode       public
auth.db         /var/lib/gonewsd/auth.db
auth.log        /var/log/gonewsd/auth.log

# PID file (for SIGHUP reload)
pidfile         /run/gonewsd/gonewsd.pid
EOF
  info "Created $CONFIG_FILE"
fi

# Step 5: Install systemd service
info "Installing systemd service..."
cp "$SERVICE_SRC" "$SERVICE_FILE"

# Update service file with correct user if different from default
if [[ "$INSTALL_USER" != "usenet" ]]; then
  sed -i "s/User=usenet/User=$INSTALL_USER/" "$SERVICE_FILE"
  sed -i "s/Group=usenet/Group=$INSTALL_USER/" "$SERVICE_FILE"
  # Also update config file
  sed -i "s/User            usenet/User            $INSTALL_USER/" "$CONFIG_FILE"
fi

info "Installed $SERVICE_FILE"

# Step 6: Enable and start service
info "Reloading systemd daemon..."
systemctl daemon-reload

info "Enabling gonewsd service..."
systemctl enable gonewsd

echo ""
info "Installation complete!"
echo ""
echo "Next steps:"
echo "  1. Edit config:     sudo nano $CONFIG_FILE"
echo "  2. Create groups:   sudo -u $INSTALL_USER gonewsd -c $CONFIG_FILE addgroup"
echo "  3. Start service:   sudo systemctl start gonewsd"
echo "  4. Check status:    sudo systemctl status gonewsd"
echo "  5. View logs:       tail -f $LOG_DIR/gonewsd.log"
echo "  6. Admin CLI:       sudo gonewsdadm"
echo ""
echo "To customize the user, set GONEWSD_USER before running:"
echo "  GONEWSD_USER=news sudo ./install-ubuntu-service.sh"
echo ""
