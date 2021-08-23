package main

import (
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/aws/aws-sdk-go/aws"
	"github.com/aws/aws-sdk-go/service/s3"
	"github.com/aws/aws-sdk-go/service/s3/s3iface"
)

var (
	mockSvc   = &mockS3Client{}
	testCases = s3ExporterTestCases{
		// Test one object in a bucket
		s3ExporterTestCase{
			Name:   "one object",
			Bucket: "mock",
			Prefix: "one",
			ExpectedOutputLines: []string{
				"s3_list_success{bucket=\"mock\",delimiter=\"\",prefix=\"one\"} 1",
				"s3_last_modified_object_date{bucket=\"mock\",prefix=\"one\"} 1.5604596e+09",
				"s3_last_modified_object_size_bytes{bucket=\"mock\",prefix=\"one\"} 1234",
				"s3_biggest_object_size_bytes{bucket=\"mock\",prefix=\"one\"} 1234",
				"s3_objects_size_sum_bytes{bucket=\"mock\",prefix=\"one\"} 1234",
				"s3_objects{bucket=\"mock\",prefix=\"one\"} 1",
			},
			ListObjectsV2Response: &s3.ListObjectsV2Output{
				Contents: []*s3.Object{
					&s3.Object{
						Key:          String("one"),
						LastModified: Time(time.Date(2019, time.June, 13, 21, 0, 0, 0, time.UTC)),
						Size:         Int64(1234),
					},
				},
				IsTruncated: Bool(false),
				KeyCount:    Int64(1),
				MaxKeys:     Int64(1000),
				Name:        String("mock"),
				Prefix:      String("one"),
			},
		},
		// Test no matching objects in the bucket
		s3ExporterTestCase{
			Name:   "no objects",
			Bucket: "mock",
			Prefix: "none",
			ExpectedOutputLines: []string{
				"s3_biggest_object_size_bytes{bucket=\"mock\",prefix=\"none\"} 0",
				"s3_last_modified_object_date{bucket=\"mock\",prefix=\"none\"} -6.795364578e+09",
				"s3_last_modified_object_size_bytes{bucket=\"mock\",prefix=\"none\"} 0",
				"s3_list_success{bucket=\"mock\",delimiter=\"\",prefix=\"none\"} 1",
				"s3_objects_size_sum_bytes{bucket=\"mock\",prefix=\"none\"} 0",
				"s3_objects{bucket=\"mock\",prefix=\"none\"} 0",
			},
			ListObjectsV2Response: &s3.ListObjectsV2Output{
				Contents:    []*s3.Object{},
				IsTruncated: Bool(false),
				KeyCount:    Int64(0),
				MaxKeys:     Int64(1000),
				Name:        String("mock"),
				Prefix:      String("none"),
			},
		},
		// Test multiple objects
		s3ExporterTestCase{
			Name:   "multiple objects",
			Bucket: "mock",
			Prefix: "multiple",
			ExpectedOutputLines: []string{
				"s3_biggest_object_size_bytes{bucket=\"mock\",prefix=\"multiple\"} 4567",
				"s3_last_modified_object_date{bucket=\"mock\",prefix=\"multiple\"} 1.568592e+09",
				"s3_last_modified_object_size_bytes{bucket=\"mock\",prefix=\"multiple\"} 4567",
				"s3_list_success{bucket=\"mock\",delimiter=\"\",prefix=\"multiple\"} 1",
				"s3_objects_size_sum_bytes{bucket=\"mock\",prefix=\"multiple\"} 11602",
				"s3_objects{bucket=\"mock\",prefix=\"multiple\"} 4",
			},
			ListObjectsV2Response: &s3.ListObjectsV2Output{
				Contents: []*s3.Object{
					&s3.Object{
						Key:          String("multiple0"),
						LastModified: Time(time.Date(2019, time.June, 13, 21, 0, 0, 0, time.UTC)),
						Size:         Int64(1234),
					},
					&s3.Object{
						Key:          String("multiple1"),
						LastModified: Time(time.Date(2019, time.July, 14, 22, 0, 0, 0, time.UTC)),
						Size:         Int64(2345),
					},
					&s3.Object{
						Key:          String("multiple2"),
						LastModified: Time(time.Date(2019, time.August, 15, 23, 0, 0, 0, time.UTC)),
						Size:         Int64(3456),
					},
					&s3.Object{
						Key:          String("multiple/0"),
						LastModified: Time(time.Date(2019, time.September, 16, 00, 0, 0, 0, time.UTC)),
						Size:         Int64(4567),
					},
				},
				IsTruncated: Bool(false),
				KeyCount:    Int64(4),
				MaxKeys:     Int64(1000),
				Name:        String("mock"),
				Prefix:      String("multiple"),
			},
		},
		// Test delimiter
		s3ExporterTestCase{
			Name:      "common prefixes",
			Bucket:    "mock",
			Prefix:    "mock-prefix",
			Delimiter: "/",
			ExpectedOutputLines: []string{
				"s3_list_success{bucket=\"mock\",delimiter=\"/\",prefix=\"mock-prefix\"} 1",
				"s3_common_prefixes{bucket=\"mock\",delimiter=\"/\",prefix=\"mock-prefix\"} 3",
			},
			ListObjectsV2Response: &s3.ListObjectsV2Output{
				Name:   aws.String("mock"),
				Prefix: aws.String("mock-prefix"),
				CommonPrefixes: []*s3.CommonPrefix{
					{
						Prefix: aws.String("one"),
					},
					{
						Prefix: aws.String("two"),
					},
					{
						Prefix: aws.String("three"),
					},
				},
			},
		},
	}
)

type mockS3Client struct {
	s3iface.S3API
}

type s3ExporterTestCase struct {
	Name                  string
	Bucket                string
	Prefix                string
	Delimiter             string
	ExpectedOutputLines   []string
	ListObjectsV2Response *s3.ListObjectsV2Output
}

// testBody tests the body returned by the exporter against the expected output
func (tc *s3ExporterTestCase) testBody(body string, t *testing.T) {
	for _, l := range tc.ExpectedOutputLines {
		ok := strings.Contains(body, l)
		if !ok {
			t.Errorf("expected " + l)
		}
	}
}

type s3ExporterTestCases []s3ExporterTestCase

// Returns the mocked response for a bucket+prefix combination
func (tcs *s3ExporterTestCases) response(bucket, prefix string) (*s3.ListObjectsV2Output, error) {
	for _, c := range *tcs {
		if c.Bucket == bucket && c.Prefix == prefix {
			return c.ListObjectsV2Response, nil
		}
	}

	return nil, errors.New("Can't find a response for the bucket and prefix combination")
}

// TestProbeHandler iterates over a list of test cases
func TestProbeHandler(t *testing.T) {
	for _, c := range testCases {
		rr, err := probe(c.Bucket, c.Prefix, c.Delimiter)
		if err != nil {
			t.Errorf(err.Error())
		}

		c.testBody(rr.Body.String(), t)
	}
}

// ListObjectsV2 mocks out the corresponding function in the S3 client, returning the response that corresponds to the test case
func (m *mockS3Client) ListObjectsV2(input *s3.ListObjectsV2Input) (*s3.ListObjectsV2Output, error) {
	r, err := testCases.response(*input.Bucket, *input.Prefix)
	if err != nil {
		return nil, err
	}

	return r, nil
}

// Repeatable probe function
func probe(bucket, prefix, delimiter string) (rr *httptest.ResponseRecorder, err error) {
	uri := "/probe?bucket=" + bucket
	if len(prefix) > 0 {
		uri = uri + "&prefix=" + prefix
	}
	if len(delimiter) > 0 {
		uri = uri + "&delimiter=" + delimiter
	}
	req, err := http.NewRequest("GET", uri, nil)
	if err != nil {
		return
	}

	rr = httptest.NewRecorder()
	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		probeHandler(w, r, mockSvc)
	})

	handler.ServeHTTP(rr, req)

	return
}

// Functions to help return pointers succinctly
func String(s string) *string {
	return &s
}

func Time(t time.Time) *time.Time {
	return &t
}

func Int64(i int64) *int64 {
	return &i
}

func Bool(b bool) *bool {
	return &b
}
