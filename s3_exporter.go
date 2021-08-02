package main

import (
	"encoding/json"
	"net/http"
	"os"
	"strings"
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
		[]string{"bucket", "prefix", "storage_class"}, nil,
	)
	s3ListDuration = prometheus.NewDesc(
		prometheus.BuildFQName(namespace, "", "list_duration_seconds"),
		"The total duration of the list operation",
		[]string{"bucket", "prefix", "storage_class"}, nil,
	)
	s3LastModifiedObjectDate = prometheus.NewDesc(
		prometheus.BuildFQName(namespace, "", "last_modified_object_date"),
		"The last modified date of the object that was modified most recently",
		[]string{"bucket", "prefix", "storage_class"}, nil,
	)
	s3LastModifiedObjectSize = prometheus.NewDesc(
		prometheus.BuildFQName(namespace, "", "last_modified_object_size_bytes"),
		"The size of the object that was modified most recently",
		[]string{"bucket", "prefix", "storage_class"}, nil,
	)
	s3ObjectTotal = prometheus.NewDesc(
		prometheus.BuildFQName(namespace, "", "objects"),
		"The total number of objects for the bucket/prefix combination",
		[]string{"bucket", "prefix", "storage_class"}, nil,
	)
	s3SumSize = prometheus.NewDesc(
		prometheus.BuildFQName(namespace, "", "objects_size_sum_bytes"),
		"The total size of all objects summed",
		[]string{"bucket", "prefix", "storage_class"}, nil,
	)
	s3BiggestSize = prometheus.NewDesc(
		prometheus.BuildFQName(namespace, "", "biggest_object_size_bytes"),
		"The size of the biggest object",
		[]string{"bucket", "prefix", "storage_class"}, nil,
	)
)

// Exporter is our exporter type
type Exporter struct {
	bucket       string
	prefixes     []string
	svc          s3iface.S3API
	storageclass string
}

// Describe all the metrics we export
func (e *Exporter) Describe(ch chan<- *prometheus.Desc) {
	ch <- s3ListSuccess
	ch <- s3ListDuration
	ch <- s3LastModifiedObjectDate
	ch <- s3LastModifiedObjectSize
	ch <- s3ObjectTotal
	ch <- s3SumSize
	ch <- s3BiggestSize
}

// Collect metrics
func (e *Exporter) Collect(ch chan<- prometheus.Metric) {
	log.Infoln("Bucket: ", e.bucket)

	for _, prefix := range e.prefixes {
		var lastModified time.Time
		var numberOfObjects float64
		var totalSize int64
		var biggestObjectSize int64
		var lastObjectSize int64

		log.Infoln("Prefix: ", prefix)

		labels := []string{e.bucket, prefix}

		if e.storageclass != "" {
			labels = append(labels, e.storageclass)
		} else {
			labels = append(labels, "*")
		}

		query := &s3.ListObjectsV2Input{
			Bucket: &e.bucket,
			Prefix: &prefix,
		}

		// Continue making requests until we've listed and compared the date of every object
		startList := time.Now()
		for {
			resp, err := e.svc.ListObjectsV2(query)
			if err != nil {
				log.Errorln(err)
				ch <- prometheus.MustNewConstMetric(
					s3ListSuccess, prometheus.GaugeValue, 0, e.bucket, prefix,
				)
				return
			}
			for _, item := range resp.Contents {
				if e.storageclass != "" &&
					*item.StorageClass != e.storageclass {
					log.Debugf("Filter out %s: %s != %s\n",
						*item.Key,
						*item.StorageClass,
						e.storageclass)

					continue
				}

				numberOfObjects++
				totalSize = totalSize + *item.Size
				if item.LastModified.After(lastModified) {
					lastModified = *item.LastModified
					lastObjectSize = *item.Size
				}
				if *item.Size > biggestObjectSize {
					biggestObjectSize = *item.Size
				}
			}
			if resp.NextContinuationToken == nil {
				break
			}
			query.ContinuationToken = resp.NextContinuationToken
		}
		listDuration := time.Now().Sub(startList).Seconds()

		ch <- prometheus.MustNewConstMetric(
			s3ListSuccess, prometheus.GaugeValue, 1, labels...,
		)
		ch <- prometheus.MustNewConstMetric(
			s3ListDuration, prometheus.GaugeValue, listDuration, labels...,
		)
		ch <- prometheus.MustNewConstMetric(
			s3LastModifiedObjectDate, prometheus.GaugeValue, float64(lastModified.UnixNano()/1e9), labels...,
		)
		ch <- prometheus.MustNewConstMetric(
			s3LastModifiedObjectSize, prometheus.GaugeValue, float64(lastObjectSize), labels...,
		)
		ch <- prometheus.MustNewConstMetric(
			s3ObjectTotal, prometheus.GaugeValue, numberOfObjects, labels...,
		)
		ch <- prometheus.MustNewConstMetric(
			s3BiggestSize, prometheus.GaugeValue, float64(biggestObjectSize), labels...,
		)
		ch <- prometheus.MustNewConstMetric(
			s3SumSize, prometheus.GaugeValue, float64(totalSize), labels...,
		)
	}
}

func probeHandler(w http.ResponseWriter, r *http.Request, svc s3iface.S3API, cfgBucket string, cfgPrefixes string, cfgStorageClass string) {
	bucket := r.URL.Query().Get("bucket")

	if bucket == "" && cfgBucket == "" {
		http.Error(w, "bucket parameter is missing", http.StatusBadRequest)
		return
	}

	var bucketName = cfgBucket

	if bucket != "" {
		log.Infoln("Use provided bucket in query", bucket)
		bucketName = bucket
	} else {
		log.Infoln("Use statically set bucket", cfgBucket)
	}

	queryString := r.URL.Query()
	prefixesArg := queryString.Get("prefixes")
	prefixArg := queryString.Get("prefix")
	storageclassArg := queryString.Get("storageclass")

	var prefixes []string

	if prefixesArg != "" {
		log.Infoln("Use provided prefixes in query", prefixesArg)
		prefixes = strings.Split(prefixesArg, ",")
	} else if prefixArg != "" {
		log.Infoln("Use provided single prefix in query", prefixArg)
		prefixes = append(prefixes, prefixArg)
	} else if cfgPrefixes != "" {
		log.Infoln("Use statically set prefixes", cfgPrefixes)
		prefixes = strings.Split(cfgPrefixes, ",")
	} else {
		log.Infoln("Use empty prefixes", cfgBucket)
		prefixes = append(prefixes, "")
	}

	storageclass := storageclassArg

	if storageclass == "" && cfgStorageClass != "" {
		storageclass = cfgStorageClass
	}

	exporter := &Exporter{
		bucket:       bucketName,
		prefixes:     prefixes,
		svc:          svc,
		storageclass: storageclass,
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
		bucket         = app.Flag("s3.bucket", "Define statically the bucket to monitor").Default("").String()
		prefixes       = app.Flag("s3.prefixes", "Define statically the prefixes to monitor").Default("").String()
		storageclass   = app.Flag("s3.storageclass", "Define statically the storage class to monitor").Default("").String()
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

	log.Infoln("- Bucket", *bucket)
	log.Infoln("- Prefixes", *prefixes)
	log.Infoln("- StorageClass", *storageclass)

	http.Handle(*metricsPath, promhttp.Handler())
	http.HandleFunc(*probePath, func(w http.ResponseWriter, r *http.Request) {
		probeHandler(w, r, svc, *bucket, *prefixes, *storageclass)
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
