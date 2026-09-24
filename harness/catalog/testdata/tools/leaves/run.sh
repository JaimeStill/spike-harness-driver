#!/bin/sh
# Answers and exits at once, leaving a child running that holds its output and whose PID it
# records.
echo out
sleep 30 &
echo $! > "$CATALOG_TEST_PIDFILE"
exit 0
