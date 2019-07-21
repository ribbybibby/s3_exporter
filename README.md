# AWS S3 Exporter
This exporter provides metrics for AWS S3 bucket objects by querying the API with a given bucket and prefix and constructing metrics based on the returned objects.

I find it useful for ensuring that backup jobs and batch uploads are functioning by comparing the growth in size/number of objects over time, or comparing the last modified date to an expected value.

## Building
```
make
```

## Running
```
./s3_exporter <flags>
```

You can query a bucket and prefix combination by supplying them as parameters to /probe:
```
curl localhost:9340/probe?bucket=some-bucket&prefix=some-folder/some-file.txt
```

### AWS Credentials
The exporter creates an AWS session without any configuration. You must specify credentials yourself as documented [here](https://docs.aws.amazon.com/sdk-for-go/v1/developer-guide/configuring-sdk.html).

Remember, if you want to load credentials from `~/.aws/config` then you need to to set:
```
export AWS_SDK_LOAD_CONFIG=true
```

### Docker
```
docker pull ribbybibby/s3-exporter
```

You will need to supply AWS credentials to the container, as mentioned in the previous section, either by setting the appropriate environment variables with `-e`, or by mounting your `~/.aws/` directory with `-v`.
```
# Environment variables
docker run -p 9340:9340 -e AWS_ACCESS_KEY_ID=<value> -e AWS_SECRET_ACCESS_KEY=<value> -e AWS_REGION=<value> s3-exporter:latest <flags>
# Mounted volume
docker run -p 9340:9340 -e AWS_SDK_LOAD_CONFIG=true -e HOME=/ -v $HOME/.aws:/.aws s3-exporter:latest <flags>
```

## Flags
    ./s3_exporter --help
 * __`--web.listen-address`:__ The port (default ":9340").
 * __`--web.metrics-path`:__ The path metrics are exposed under (default "/metrics")
 * __`--web.probe-path`:__ The path the probe endpoint is exposed under (default "/probe")
 * __`--s3.endpoint-url`:__ A [custom endpoint URL](https://docs.aws.amazon.com/general/latest/gr/rande.html) (optional)

Flags can also be set as environment variables, prefixed by `S3_EXPORTER_`. For example: `S3_EXPORTER_S3_ENDPOINT_URL=http://s3.example.local`.

## Metrics

| Metric | Meaning | Labels |
| ------ | ------- | ------ |
| s3_biggest_object_size_bytes | The size of the largest object. | bucket, prefix |
| s3_last_modified_object_date | The modification date of the most recently modified object. | bucket, prefix |
| s3_last_modified_object_size_bytes | The size of the object that was modified most recently. | bucket, prefix |
| s3_list_success | Did the ListObjects operation complete successfully? | bucket, prefix |
| s3_objects_size_sum_bytes | The sum of the size of all the objects. | bucket, prefix |
| s3_objects_total | The total number of objects. | bucket, prefix |

## Prometheus
### Configuration
You should pass the params to a single instance of the exporter using relabelling, like so:
```yml
scrape_configs:
  - job_name: 's3'
    metrics_path: /probe
    static_configs:
      - targets:
        - bucket=stuff;prefix=thing.txt;
        - bucket=other-stuff;prefix=another-thing.gif;
    relabel_configs:
      - source_labels: [__address__]
        regex: '^bucket=(.*);prefix=(.*);$'
        replacement: '${1}'
        target_label: '__param_bucket'
      - source_labels: [__address__]
        regex: '^bucket=(.*);prefix=(.*);$'
        replacement: '${2}'
        target_label: '__param_prefix'
      - target_label: __address__
        replacement: 127.0.0.1:9340  # S3 exporter.

```
### Example Queries
Return series where the last modified object date is more than 24 hours ago:
```
(time() - s3_last_modified_object_date) / 3600 > 24
```
