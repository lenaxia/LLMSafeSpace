#!/bin/bash

# Run all tests in the kubernetes package
cd "$(dirname "$0")"
go test -v ./...
