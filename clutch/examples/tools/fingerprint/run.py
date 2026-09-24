#!/usr/bin/env python3
# Prints the fingerprint of the text in the arguments on stdin: the first 12 hex digits of its
# SHA-256, which clutch's tool scenario checks against the value it computes in Go.
import hashlib
import json
import sys

args = json.load(sys.stdin)
print(hashlib.sha256(args["text"].encode("utf-8")).hexdigest()[:12])
