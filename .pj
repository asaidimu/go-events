#!/usr/bin/env sh
set -eu

session=$1
folder=$2

# otherwise commands will run in current dir
cd $folder

tmux send-keys -t $session:1 "$EDITOR" Enter
tmux neww -d -n workspace  -t $session:2
tmux neww -d -n compiler  -t $session:8

# vim:set ft=sh:
