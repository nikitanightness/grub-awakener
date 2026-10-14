#!/usr/bin/env bash

set -e

echo "Building 'grub-lan-awakener' for linux/arm64..."

CGO_ENABLED=0 GOOS=linux GOARCH=arm64 go build \
    -trimpath \
    -ldflags="-s -w" \
    -o grub-lan-awakener \
    main.go

echo "Complete! Output: ./grub-lan-awakener"
