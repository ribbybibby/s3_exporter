# AWS S3 Exporter
__Situation/Problem:__ I want to verify that my backup tasks are running but I don't necessarily have the ability to instrument the backup application I'm using. My backups go to an S3 bucket. If I could alert when the last modified date is outside a known range, then I'd have a fairly good indiciation when something is wrong with my backups.

__Solution:__ An exporter that can report the last modified date for objects in a bucket that match a given prefix.

This exporter queries the S3 API with a given bucket and prefix and constructs metrics based on the returned objects. For my purposes the last modified date is the important one, but there are others.

## Building
    make

## Running
    ./s3_exporter <flags>

You can query a bucket and prefix combination by supplying them as parameters to /probe:

    localhost:9340/probe?bucket=some-bucket&prefix=some-prefix.txt

### AWS Credentials
The exporter creates an AWS session without any configuration. You must specify credentials yourself as documented [here](https://docs.aws.amazon.com/sdk-for-go/v1/developer-guide/configuring-sdk.html).

Remember, if you want to load credentials from `~/.aws/config` then you need to to set:

    export AWS_SDK_LOAD_CONFIG=true

## Docker
    docker pull ribbybibby/s3-exporter
    docker run -p 9340:9340 s3-exporter:latest <flags>

## Flags
    ./s3_exporter --help
 * __`--web.listen-address`:__ The port (default ":9340").
 * __`--web.metrics-path`:__ The path metrics are exposed under (default "/metrics")
 * __`--web.probe-path`:__ The path the probe endpoint is exposed under (default "/probe")

## Metrics


| Metric | Meaning | Labels |
| ------ | ------- | ------ |
| s3_biggest_object_size_bytes | The size of the largest object. | bucket, prefix |
| s3_last_modified_object_date | The modification date of the most recently modified object. | bucket, prefix |
| s3_list_success | Did the ListObjects operation complete successfully? | bucket, prefix |
| s3_objects_size_sum_bytes | The sum of the size of all the objects. | bucket, prefix |
| s3_objects_total | The total number of objects. | bucket, prefix |

## Prometheus
### Configuration
You should pass the params to a single instance of the exporter using relabelling, like so:
```yml
scrape_configs:
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
    
    (time() - s3_last_modified_object_date) / 3600 > 24
