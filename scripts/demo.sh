#!/usr/bin/env bash
# Plays the README demo: real commands, typed out, run from the current
# directory (a clone of this repository with offload.yml set up).
# .github/workflows/demo.yml records it and renders docs/demo.gif.
set -u

type_out() {
	printf '\033[1;32m$\033[0m '
	sleep 0.6
	local s=$1 i
	for ((i = 0; i < ${#s}; i++)); do
		printf '%s' "${s:i:1}"
		sleep 0.04
	done
	sleep 0.5
	printf '\n'
}

run() {
	type_out "$1"
	bash -c "$1"
	sleep 1.5
}

clear
rm -f gh-offload
run 'uname -m'
run 'gh offload --os linux-arm --get gh-offload -y -- go build -o gh-offload .'
run 'file gh-offload | cut -d, -f1-2'
sleep 2
