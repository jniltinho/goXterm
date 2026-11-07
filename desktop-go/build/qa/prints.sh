#!/bin/sh
# Screenshot sweep of the Go app on Linux (task 13.1): every screen in one skin, driven with xdotool
# on Xvfb. Runs inside the QA container (toolchain image on the lab network, /w = desktop-go, a QA
# store with the lab sessions and the throwaway master password in $RXT_QA_PASS):
#   docker exec -e RXT_QA_PASS=… rxt-qa sh /w/build/qa/prints.sh light|dark /w/.cache/prints/light
# Coordinates are for the 1280×800 window at (60,50) on a 1400×900 screen and a 320 px sidebar.
set -eu
skin=$1 out=$2
export DISPLAY=:99 WEBKIT_DISABLE_SANDBOX_THIS_IS_DANGEROUS=1 GTK_A11Y=none
cfg=${XDG_CONFIG_HOME:-/tmp/cfg}/rustxterm/config.toml
mkdir -p "$out"; rm -f "$out"/*.png

pgrep -x Xvfb >/dev/null || { Xvfb :99 -screen 0 1400x900x24 >/dev/null 2>&1 & sleep 2; }
pkill -f rustxterm-go-dev-linux || true; sleep 1
# The skin under test, the Sessions page, a 320 px sidebar and the monitor bar on.
sed -i -e "s/^theme = .*/theme = \"$skin\"/" -e 's/^sidebar_page = .*/sidebar_page = "sessions"/' \
  -e 's/^sidebar_width = .*/sidebar_width = 320.0/' -e 's/^remote_monitoring = .*/remote_monitoring = true/' "$cfg"
(cd /w && dbus-run-session -- ./dist/rustxterm-go-dev-linux-amd64 --safe-gpu >/tmp/prints-app.log 2>&1 &)

n=0
shot() { n=$((n + 1)); sleep "${2:-1}"; xdotool mousemove 1395 895; import -window root "$out/$(printf %02d $n)-$1.png"; }
click() { xdotool mousemove "$1" "$2" click "${3:-1}"; sleep 0.6; }
key() { xdotool key "$@"; sleep 0.5; }
dbl() { xdotool mousemove "$1" "$2" click --repeat 2 --delay 80 1; }

shot unlock 9
xdotool type "$RXT_QA_PASS"; key Return
shot home 2
click 187 451 3; shot tree-session-menu; click 900 700
click 166 211 3; shot tree-folder-menu; click 900 700 # a click outside closes the tree menu

# Session editor: the untyped prompt, the SSH tabs, then every session type.
click 91 95; shot editor-new 2
click 294 210; shot editor-ssh-advanced
click 330 342
for page in terminal network bookmark agent; do key Right; shot "editor-ssh-$page"; done
for t in "352 telnet" "410 sftp" "468 ftp" "526 ftps" "584 rdp" "642 vnc" "700 serial"; do
  set -- $t; click "$1" 210; click 330 342; shot "editor-$2"
done
key Escape

# Settings: every tab.
click 345 95; click 360 220
for tab in general terminal ssh display shortcuts security agent diagnostics; do
  shot "settings-$tab"; key Right
done
key Escape

click 216 95; shot tunnels 2; key Escape

# Live sessions against the lab: SSH (terminal, SFTP browser, monitor bar), RDP, VNC, FTP.
dbl 187 307; shot ssh-sftp-monitor 7
click 77 170; dbl 176 499; shot rdp 9
dbl 208 283; shot vnc 7
dbl 176 475; shot ftp 6
pkill -f rustxterm-go-dev-linux || true
echo "$n screenshots in $out"
