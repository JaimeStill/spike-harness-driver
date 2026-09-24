#!/bin/sh
# Runs until killed, in a child process of the script whose PID it records.
sleep 30 &
echo $! > "$CATALOG_TEST_PIDFILE"
wait
