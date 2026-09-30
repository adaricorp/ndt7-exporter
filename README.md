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
    -listen=localhost:9191 \
    -source-ip=192.0.2.1
```

## Metrics

### Prometheus

All metric names for prometheus start with `ndt7_`.

A direction that is not run exports no throughput, latency or
`ndt7_result_timestamp_seconds` series at all, rather than a zero.
`-download=false` and `-upload=false` each leave one direction out. A
`-service-url` for the download or upload path (`/ndt/v7/download` or
`/ndt/v7/upload`) runs that direction alone, whatever those two flags say. A
`-service-url` whose path names neither direction is ignored with a warning,
and the two flags apply as given.
