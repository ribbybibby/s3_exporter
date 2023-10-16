package main

import (
	"encoding/json"
	"net/http"
	"os"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promhttp"
	"github.com/prometheus/common/log"
	"github.com/prometheus/common/version"
	kingpin "gopkg.in/alecthomas/kingpin.v2"

	"github.com/aws/aws-sdk-go/aws"
	"github.com/aws/aws-sdk-go/aws/session"
	"github.com/aws/aws-sdk-go/service/s3"
	"github.com/aws/aws-sdk-go/service/s3/s3iface"
)

const (
	namespace = "s3"
)

var (
	s3ListSuccess = prometheus.NewDesc(
		prometheus.BuildFQName(namespace, "", "list_success"),
		"If the ListObjects operation was a success",
		[]string{"bucket", "prefix", "delimiter"}, nil,
	)
	s3ListDuration = prometheus.NewDesc(
		prometheus.BuildFQName(namespace, "", "list_duration_seconds"),
		"The total duration of the list operation",
		[]string{"bucket", "prefix", "delimiter"}, nil,
	)
	s3LastModifiedObjectDate = prometheus.NewDesc(
		prometheus.BuildFQName(namespace, "", "last_modified_object_date"),
		"The last modified date of the object that was modified most recently",
		[]string{"bucket", "prefix"}, nil,
	)
	s3LastModifiedObjectSize = prometheus.NewDesc(
		prometheus.BuildFQName(namespace, "", "last_modified_object_size_bytes"),
		"The size of the object that was modified most recently",
		[]string{"bucket", "prefix"}, nil,
	)
	s3ObjectTotal = prometheus.NewDesc(
		prometheus.BuildFQName(namespace, "", "objects"),
		"The total number of objects for the bucket/prefix combination",
		[]string{"bucket", "prefix"}, nil,
	)
	s3SumSize = prometheus.NewDesc(
		prometheus.BuildFQName(namespace, "", "objects_size_sum_bytes"),
		"The total size of all objects summed",
		[]string{"bucket", "prefix"}, nil,
	)
	s3BiggestSize = prometheus.NewDesc(
		prometheus.BuildFQName(namespace, "", "biggest_object_size_bytes"),
		"The size of the biggest object",
		[]string{"bucket", "prefix"}, nil,
	)
	s3CommonPrefixes = prometheus.NewDesc(
		prometheus.BuildFQName(namespace, "", "common_prefixes"),
		"A count of all the keys between the prefix and the next occurrence of the string specified by the delimiter",
		[]string{"bucket", "prefix", "delimiter"}, nil,
	)
)

// Counter is a receiver function that in the context of an Exporter populates an ItemAggregator
type Counter func(e *Exporter, ia *ItemAggregator) error

// Exporter is our exporter type
type Exporter struct {
	bucket    string
	prefix    string
	delimiter string
	svc       s3iface.S3API
	counter   Counter
}

// ItemAggregator is where we collect statistics on files/objects
type ItemAggregator struct {
	lastModified      time.Time
	numberOfObjects   float64
	totalSize         int64
	biggestObjectSize int64
	lastObjectSize    int64
	commonPrefixes    int
}

// Describe all the metrics we export
func (e *Exporter) Describe(ch chan<- *prometheus.Desc) {
	ch <- s3ListSuccess
	ch <- s3ListDuration
	if e.delimiter == "" {
		ch <- s3LastModifiedObjectDate
		ch <- s3LastModifiedObjectSize
		ch <- s3ObjectTotal
		ch <- s3SumSize
		ch <- s3BiggestSize
	} else {
		ch <- s3CommonPrefixes
	}
}

func (e *Exporter) CountViaListObjectsV2(ia *ItemAggregator) error {
	query := &s3.ListObjectsV2Input{
		Bucket:    aws.String(e.bucket),
		Prefix:    aws.String(e.prefix),
		Delimiter: aws.String(e.delimiter),
	}

	for {
		resp, err := e.svc.ListObjectsV2(query)
		if err != nil {
			return err
		}
		ia.commonPrefixes = ia.commonPrefixes + len(resp.CommonPrefixes)
		for _, item := range resp.Contents {
			ia.numberOfObjects++
			ia.totalSize = ia.totalSize + *item.Size
			if item.LastModified.After(ia.lastModified) {
				ia.lastModified = *item.LastModified
				ia.lastObjectSize = *item.Size
			}
			if *item.Size > ia.biggestObjectSize {
				ia.biggestObjectSize = *item.Size
			}
		}
		if resp.NextContinuationToken == nil {
			break
		}
		query.ContinuationToken = resp.NextContinuationToken
	}
	return nil
}

func (e *Exporter) CountViaListObjectVersions(ia *ItemAggregator) error {
	query := &s3.ListObjectVersionsInput{
		Bucket:    aws.String(e.bucket),
		Prefix:    aws.String(e.prefix),
		Delimiter: aws.String(e.delimiter),
	}

	for {
		resp, err := e.svc.ListObjectVersions(query)
		if err != nil {
			return err
		}
		ia.commonPrefixes = ia.commonPrefixes + len(resp.CommonPrefixes)
		for _, item := range resp.Versions {
			ia.numberOfObjects++
			ia.totalSize = ia.totalSize + *item.Size
			if item.LastModified.After(ia.lastModified) {
				ia.lastModified = *item.LastModified
				ia.lastObjectSize = *item.Size
			}
			if *item.Size > ia.biggestObjectSize {
				ia.biggestObjectSize = *item.Size
			}
		}
		if !*resp.IsTruncated {
			break
		}
		query.KeyMarker = resp.NextKeyMarker
		query.VersionIdMarker = resp.NextVersionIdMarker
	}
	return nil
}

// Collect metrics
func (e *Exporter) Collect(ch chan<- prometheus.Metric) {
	ia := &ItemAggregator{
		numberOfObjects:   0,
		totalSize:         0,
		biggestObjectSize: 0,
		lastObjectSize:    0,
		commonPrefixes:    0,
	}

	// Continue making requests until we've listed and compared the date of every object
	startList := time.Now()
	if err := e.counter(e, ia); err != nil {
		log.Errorln(err)
		ch <- prometheus.MustNewConstMetric(
			s3ListSuccess, prometheus.GaugeValue, 0, e.bucket, e.prefix,
		)
		return
	}
	listDuration := time.Now().Sub(startList).Seconds()

	ch <- prometheus.MustNewConstMetric(
		s3ListSuccess, prometheus.GaugeValue, 1, e.bucket, e.prefix, e.delimiter,
	)
	ch <- prometheus.MustNewConstMetric(
		s3ListDuration, prometheus.GaugeValue, listDuration, e.bucket, e.prefix, e.delimiter,
	)
	if e.delimiter == "" {
		ch <- prometheus.MustNewConstMetric(
			s3LastModifiedObjectDate, prometheus.GaugeValue, float64(ia.lastModified.UnixNano()/1e9), e.bucket, e.prefix,
		)
		ch <- prometheus.MustNewConstMetric(
			s3LastModifiedObjectSize, prometheus.GaugeValue, float64(ia.lastObjectSize), e.bucket, e.prefix,
		)
		ch <- prometheus.MustNewConstMetric(
			s3ObjectTotal, prometheus.GaugeValue, ia.numberOfObjects, e.bucket, e.prefix,
		)
		ch <- prometheus.MustNewConstMetric(
			s3BiggestSize, prometheus.GaugeValue, float64(ia.biggestObjectSize), e.bucket, e.prefix,
		)
		ch <- prometheus.MustNewConstMetric(
			s3SumSize, prometheus.GaugeValue, float64(ia.totalSize), e.bucket, e.prefix,
		)
	} else {
		ch <- prometheus.MustNewConstMetric(
			s3CommonPrefixes, prometheus.GaugeValue, float64(ia.commonPrefixes), e.bucket, e.prefix, e.delimiter,
		)
	}
}

func probeHandler(w http.ResponseWriter, r *http.Request, svc s3iface.S3API, withVersions bool) {
	bucket := r.URL.Query().Get("bucket")
	if bucket == "" {
		http.Error(w, "bucket parameter is missing", http.StatusBadRequest)
		return
	}

	prefix := r.URL.Query().Get("prefix")
	delimiter := r.URL.Query().Get("delimiter")

	counter := (*Exporter).CountViaListObjectsV2
	if withVersions {
		counter = (*Exporter).CountViaListObjectVersions
	}
	exporter := &Exporter{
		bucket:    bucket,
		prefix:    prefix,
		delimiter: delimiter,
		svc:       svc,
		counter:   counter,
	}

	registry := prometheus.NewRegistry()
	registry.MustRegister(exporter)

	// Serve
	h := promhttp.HandlerFor(registry, promhttp.HandlerOpts{})
	h.ServeHTTP(w, r)
}

type discoveryTarget struct {
	Targets []string          `json:"targets"`
	Labels  map[string]string `json:"labels"`
}

func discoveryHandler(w http.ResponseWriter, r *http.Request, svc s3iface.S3API) {
	result, err := svc.ListBuckets(&s3.ListBucketsInput{})
	if err != nil {
		log.Errorln(err)
		http.Error(w, "error listing buckets", http.StatusInternalServerError)
		return
	}

	targets := []discoveryTarget{}
	for _, b := range result.Buckets {
		name := aws.StringValue(b.Name)
		if name != "" {
			t := discoveryTarget{
				Targets: []string{r.Host},
				Labels: map[string]string{
					"__param_bucket": name,
				},
			}
			targets = append(targets, t)
		}
	}

	data, err := json.Marshal(targets)
	if err != nil {
		http.Error(w, "error marshalling json", http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	w.Write(data)
}

func init() {
	prometheus.MustRegister(version.NewCollector(namespace + "_exporter"))
}

func main() {
	var (
		app            = kingpin.New(namespace+"_exporter", "Export metrics for S3 certificates").DefaultEnvars()
		listenAddress  = app.Flag("web.listen-address", "Address to listen on for web interface and telemetry.").Default(":9340").String()
		metricsPath    = app.Flag("web.metrics-path", "Path under which to expose metrics").Default("/metrics").String()
		probePath      = app.Flag("web.probe-path", "Path under which to expose the probe endpoint").Default("/probe").String()
		discoveryPath  = app.Flag("web.discovery-path", "Path under which to expose service discovery").Default("/discovery").String()
		endpointURL    = app.Flag("s3.endpoint-url", "Custom endpoint URL").Default("").String()
		disableSSL     = app.Flag("s3.disable-ssl", "Custom disable SSL").Bool()
		forcePathStyle = app.Flag("s3.force-path-style", "Custom force path style").Bool()
		withVersions   = app.Flag("s3.with-versions", "Count all versioned objects").Bool()
	)

	log.AddFlags(app)
	app.Version(version.Print(namespace + "_exporter"))
	app.HelpFlag.Short('h')
	kingpin.MustParse(app.Parse(os.Args[1:]))

	var sess *session.Session
	var err error

	sess, err = session.NewSession()
	if err != nil {
		log.Errorln("Error creating sessions ", err)
	}

	cfg := aws.NewConfig()
	if *endpointURL != "" {
		cfg.WithEndpoint(*endpointURL)
	}

	cfg.WithDisableSSL(*disableSSL)
	cfg.WithS3ForcePathStyle(*forcePathStyle)

	svc := s3.New(sess, cfg)

	log.Infoln("Starting "+namespace+"_exporter", version.Info())
	log.Infoln("Build context", version.BuildContext())

	http.Handle(*metricsPath, promhttp.Handler())
	http.HandleFunc(*probePath, func(w http.ResponseWriter, r *http.Request) {
		probeHandler(w, r, svc, *withVersions)
	})
	http.HandleFunc(*discoveryPath, func(w http.ResponseWriter, r *http.Request) {
		discoveryHandler(w, r, svc)
	})
	http.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(`<html>
						 <head><title>AWS S3 Exporter</title></head>
						 <body>
						 <h1>AWS S3 Exporter</h1>
						 <p><a href="` + *probePath + `?bucket=BUCKET&prefix=PREFIX">Query metrics for objects in BUCKET that match PREFIX</a></p>
						 <p><a href='` + *metricsPath + `'>Metrics</a></p>
						 <p><a href='` + *discoveryPath + `'>Service Discovery</a></p>
						 </body>
						 </html>`))
	})

	log.Infoln("Listening on", *listenAddress)
	log.Fatal(http.ListenAndServe(*listenAddress, nil))
}
