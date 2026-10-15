#!/usr/bin/env sh

set -e

GRUB_CFG="${1:-/boot/grub/grub.cfg}"

awk -F"'" '/^[[:space:]]*menuentry / {print "Boot Entry: " $2 "  --->  ID: " $4}' "$GRUB_CFG"
