#!/usr/bin/env bash

set -x

CGO_ENABLED=0 GOOS=linux go build -a -installsuffix cgo -o fluentd_pilot

docker build -t hub.bunny-tech.com/prod/fluentd_pilot:git.$1 -f Dockerfile .
docker push hub.bunny-tech.com/prod/fluentd_pilot:git.$1
