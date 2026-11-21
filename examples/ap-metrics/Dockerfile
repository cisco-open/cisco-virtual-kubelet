FROM alpine:3.22
COPY ap-metrics /usr/local/bin/ap-metrics
COPY rootfs-data/.keep /data/.keep
COPY licenses /licenses
ENTRYPOINT ["/usr/local/bin/ap-metrics"]
