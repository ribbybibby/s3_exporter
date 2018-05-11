FROM quay.io/prometheus/busybox:latest

COPY s3_exporter /bin/s3_exporter

ENTRYPOINT ["/bin/s3_exporter"]