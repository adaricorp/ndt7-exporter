# ndt7-exporter

This is a prometheus exporter for running regular
[NDT7](https://www.measurementlab.net/tests/ndt/ndt7/) throughput tests
and allowing the data to be scraped by prometheus.

This is based on the
[ndt7-prometheus-exporter](https://github.com/m-lab/ndt7-client-go/tree/main/cmd/ndt7-prometheus-exporter)
which is part of the M-Lab reference ndt7 golang client, but with additional features such as
configurable source IP.

## Downloading

Download prebuilt binaries from [GitHub](https://github.com/adaricorp/ndt7-exporter/releases/latest).

## Running

To run regular throughput tests to the nearest M-Lab endpoint from the source IP 192.0.2.1
and make results available on `localhost:9191/metrics`, run:

```
ndt7_exporter \
    -quiet \
    -listen=localhost:9191 \
    -source-ip=192.0.2.1
```

## Metrics

### Prometheus

All metric names for prometheus start with `ndt7_`.
