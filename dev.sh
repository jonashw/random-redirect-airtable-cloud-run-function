#!/bin/sh
env $(cat .env | xargs) "$(go env GOPATH)/bin/air"
