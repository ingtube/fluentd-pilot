FROM hub.bunny-tech.com/third_party/alpine:3.5-localtime

RUN mkdir -p /ingtube/log/bin
ADD fluentd_pilot /ingtube/log/bin/fluentd_pilot
