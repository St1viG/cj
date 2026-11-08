#!/usr/bin/env bash
# demo-sleep.sh — roadmap P6.2: keep this machine awake for the demo, and let
# it sleep again afterwards. Run it on MAC-P, on every node, and on anything
# else that has to stay on the tailnet while the audience watches.
#
#   hack/demo-sleep.sh on        hold a sleep assertion until `off`
#   hack/demo-sleep.sh off       release it
#   hack/demo-sleep.sh status    say whether it is held (exit 0) or not (exit 1)
#
# It changes no system setting. `on` starts a background process that holds
# the assertion, and `off` stops that process; if the process dies, or the
# machine reboots, the machine sleeps normally again.
#   macOS   caffeinate -dims: no display, idle or system sleep, on AC power
#   Linux   systemd-inhibit on sleep, idle and the lid switch
#
# A closed MacBook lid sleeps the machine whatever an assertion says, unless an
# external display is attached. To run a node lid-closed, the owner of the
# machine runs, once and as root:
#   sudo pmset -a disablesleep 1      # and `... 0` afterwards
# This script never does that; `status` reports whether it is set.
#
#   DEMO_SLEEP_PID   pid file (default: ~/.cache/kondukter/demo-sleep.pid)
set -euo pipefail

pidfile=${DEMO_SLEEP_PID:-$HOME/.cache/kondukter/demo-sleep.pid}
os=$(uname -s)

# held succeeds when the pid file names a live holder process.
held() {
	local pid
	[ -e "$pidfile" ] || return 1
	pid=$(cat "$pidfile")
	[ -n "$pid" ] && kill -0 "$pid" 2>/dev/null
}

# lid prints a line about pmset's disablesleep on macOS, which a closed lid needs.
lid() {
	[ "$os" = Darwin ] || return 0
	if pmset -g | grep -Eq '(^|[[:space:]])(SleepDisabled|disablesleep)[[:space:]]+1'; then
		echo "lid: sleep is disabled system-wide (pmset disablesleep 1); a closed lid keeps the machine awake"
	else
		echo "lid: a closed lid still sleeps this machine, unless an external display is attached (see the header)"
	fi
}

case ${1:-} in
on)
	if held; then
		echo "demo-sleep: already on (pid $(cat "$pidfile"))"
		exit 0
	fi
	mkdir -p "$(dirname "$pidfile")"
	case $os in
	Darwin)
		command -v caffeinate >/dev/null || { echo "demo-sleep: caffeinate not found" >&2; exit 1; }
		nohup caffeinate -dims >/dev/null 2>&1 &
		;;
	Linux)
		command -v systemd-inhibit >/dev/null || { echo "demo-sleep: systemd-inhibit not found" >&2; exit 1; }
		nohup systemd-inhibit --what=sleep:idle:handle-lid-switch --who=kondukter-demo \
			--why="Kondukter demo" --mode=block sleep infinity >/dev/null 2>&1 &
		;;
	*)
		echo "demo-sleep: unsupported OS $os" >&2
		exit 1
		;;
	esac
	echo $! >"$pidfile"
	disown
	# A holder that dies at once (a missing display, no logind) must not look like success.
	sleep 1
	held || { echo "demo-sleep: the holder process exited at once" >&2; rm -f "$pidfile"; exit 1; }
	echo "demo-sleep: on (pid $(cat "$pidfile")); \`hack/demo-sleep.sh off\` after the demo"
	lid
	;;
off)
	if held; then
		kill "$(cat "$pidfile")"
		echo "demo-sleep: off"
	else
		echo "demo-sleep: was not on"
	fi
	rm -f "$pidfile"
	;;
status)
	if held; then
		echo "demo-sleep: on (pid $(cat "$pidfile"))"
		lid
	else
		echo "demo-sleep: off"
		exit 1
	fi
	;;
*)
	echo "usage: ${0##*/} on|off|status" >&2
	exit 2
	;;
esac
