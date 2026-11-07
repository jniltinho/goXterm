#!/bin/sh
# Benchmark (task 13.2): cold start, local-terminal throughput, keystroke-to-process and Ctrl+C
# latency of the Go app against the Rust app, in the same clean container on Xvfb.
#   run.sh GO_BINARY RUST_BINARY OUT_DIR
# Cold start = launch until the window shows rendered content (median of 3). Throughput = `seq` lines
# per second through the local terminal. Latencies = median from xdotool sending a key to the
# process in the terminal reading it (input path, not glyph rendering).
set -u
GO=$1 RUST=$2 OUT=$3
KEYLAT=/tmp/keylat
export DISPLAY=:99 WEBKIT_DISABLE_SANDBOX_THIS_IS_DANGEROUS=1 GTK_A11Y=none LIBGL_ALWAYS_SOFTWARE=1
export HOME=/tmp/bench-home XDG_CONFIG_HOME=/tmp/bench-home/cfg XDG_DATA_HOME=/tmp/bench-home/data
mkdir -p "$OUT" "$HOME"
Xvfb :99 -screen 0 1400x900x24 >/dev/null 2>&1 &
sleep 2
(cd /src/build/bench/keylat && go build -o $KEYLAT .) || exit 1
now() { date +%s%N; }
median() { sort -n | awk '{a[NR]=$1} END {if (NR==0) print 0; else print a[int((NR+1)/2)]}'; }

# wait_rendered PID: ns when a rustXterm window of that run shows content (stddev of its pixels).
wait_rendered() {
  for i in $(seq 1 600); do
    wid=$(xdotool search --onlyvisible --name rustXterm 2>/dev/null | head -1)
    if [ -n "$wid" ] && import -window "$wid" /tmp/w.png 2>/dev/null; then
      sd=$(identify -format '%[fx:standard_deviation]' /tmp/w.png 2>/dev/null || echo 0)
      if [ "$(echo "$sd > 0.08" | bc)" = 1 ]; then now; return; fi
    fi
    sleep 0.05
  done
  echo 0
}

start_app() { rm -rf "$HOME/cfg" "$HOME/data"; mkdir -p "$HOME/cfg" "$HOME/data"; (cd "$HOME" && dbus-run-session -- "$@" >/tmp/app-$$.log 2>&1 &); }
stop_app() { pkill -f "$1" 2>/dev/null; sleep 1.5; }

measure() { # name plusX plusY binary [args]  (plusX/plusY: the "+" tab button inside the window)
  name=$1 px=$2 py=$3; shift 3
  : > "$OUT/cold-$name"
  for run in 1 2 3; do
    t0=$(now); start_app "$@"; t1=$(wait_rendered)
    echo $(( (t1 - t0) / 1000000 )) >> "$OUT/cold-$name"
    [ $run -lt 3 ] && stop_app "$1"
  done
  sleep 2
  # first-run "Create master password": click Later (same place in both windows)
  wid=$(xdotool search --onlyvisible --name rustXterm | head -1)
  eval "$(xdotool getwindowgeometry --shell "$wid")"
  xdotool windowfocus --sync "$wid"   # no window manager: give the window the keyboard
  xdotool mousemove $((X + 693)) $((Y + 505)) click 1; sleep 1
  xdotool mousemove $((X + 900)) $((Y + 300)) click 1; sleep 0.5
  xdotool mousemove $((X + px)) $((Y + py)) click 1; sleep 4       # "+": local terminal
  import -window root "$OUT/terminal-$name.png"
  xdotool mousemove $((X + 700)) $((Y + 400)) click 1; sleep 0.5   # focus the terminal
  # throughput
  rm -f /tmp/tp; xdotool type --delay 20 "bash -c 'time -p seq 1 1000000' 2>/tmp/tp"; xdotool key Return
  for i in $(seq 1 600); do grep -q real /tmp/tp 2>/dev/null && break; sleep 0.2; done
  awk '/real/ {printf "%d\n", 1000000/$2}' /tmp/tp > "$OUT/tp-$name"
  sleep 1; xdotool type --delay 20 "clear"; xdotool key Return; sleep 1
  # keystroke-to-process
  rm -f /tmp/lat; : > /tmp/sent
  xdotool type --delay 20 "stty raw -echo; $KEYLAT input 30 /tmp/lat; stty sane"; xdotool key Return; sleep 2
  for i in $(seq 1 30); do now >> /tmp/sent; xdotool key a; sleep 0.08; done
  for i in $(seq 1 100); do [ -s /tmp/lat ] && break; sleep 0.1; done
  paste /tmp/sent /tmp/lat | awk 'NF==2 {print ($2-$1)/1000000}' | median > "$OUT/key-$name"
  sleep 1
  # Ctrl+C
  rm -f /tmp/int; xdotool type --delay 20 "$KEYLAT sigint /tmp/int"; xdotool key Return; sleep 2
  s=$(now); xdotool key ctrl+c
  for i in $(seq 1 100); do [ -s /tmp/int ] && break; sleep 0.05; done
  echo "scale=2; ($(cat /tmp/int 2>/dev/null || echo $s) - $s)/1000000" | bc > "$OUT/int-$name"
  stop_app "$1"
}

measure go 291 86 "$GO" --safe-gpu
measure rust 291 86 "$RUST"

r() { cat "$OUT/$1" 2>/dev/null | head -1; }
cold_go=$(median < "$OUT/cold-go"); cold_rust=$(median < "$OUT/cold-rust")
{
  echo "| Measure | Go | Rust | Ratio Go/Rust | Threshold | Result |"
  echo "|---|---|---|---|---|---|"
  row() { # label go rust threshold-expr pass-expr unit
    ratio=$(echo "scale=2; $2 / $3" | bc 2>/dev/null || echo "?")
    pass=$(echo "$ratio $5" | bc 2>/dev/null)
    [ "$pass" = 1 ] && res=PASS || res=FAIL
    echo "| $1 | $2 $6 | $3 $6 | $ratio | $4 | $res |"
  }
  row "Cold start (median of 3)" "$cold_go" "$cold_rust" "≤ 1.5×" "<= 1.5" ms
  row "Terminal throughput (seq 1..1e6)" "$(r tp-go)" "$(r tp-rust)" "≥ 0.80×" ">= 0.80" "lines/s"
  row "Keystroke to process (median of 30)" "$(r key-go)" "$(r key-rust)" "≤ 1.5×" "<= 1.5" ms
  row "Ctrl+C to SIGINT" "$(r int-go)" "$(r int-rust)" "≤ 1.5×" "<= 1.5" ms
} > "$OUT/results.md"
cat "$OUT/results.md"
