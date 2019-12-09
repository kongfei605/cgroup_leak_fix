#!/bin/sh
GOOS=linux GOARCH=amd64  go build -o cgroup_fix main.go stack.go
