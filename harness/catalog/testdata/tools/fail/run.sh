#!/bin/sh
# Fails as a tool does: a reason on stderr and a non-zero exit.
echo "lookup failed: no such key" >&2
exit 3
