package archive

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/credentials"
	"github.com/aws/aws-sdk-go-v2/service/s3"
)

// rgw is a minimal S3 server: path-style PUTs of buckets and objects.
type rgw struct {
	mu      sync.Mutex
	buckets map[string]bool
	objects map[string]string
	calls   []string
}

func (r *rgw) ServeHTTP(w http.ResponseWriter, req *http.Request) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.calls = append(r.calls, req.Method+" "+req.URL.Path)
	if req.Method != http.MethodPut {
		w.WriteHeader(http.StatusMethodNotAllowed)
		return
	}
	var bucket, key string
	path := req.URL.Path[1:]
	for i := range path {
		if path[i] == '/' {
			bucket, key = path[:i], path[i+1:]
			break
		}
	}
	if bucket == "" {
		r.buckets[path] = true
		return
	}
	if !r.buckets[bucket] {
		w.WriteHeader(http.StatusNotFound)
		io.WriteString(w, `<?xml version="1.0" encoding="UTF-8"?><Error><Code>NoSuchBucket</Code><BucketName>`+bucket+`</BucketName></Error>`)
		return
	}
	body, _ := io.ReadAll(req.Body)
	r.objects[bucket+"/"+key] = string(body)
}

func TestPutCreatesMissingBucket(t *testing.T) {
	server := &rgw{buckets: map[string]bool{}, objects: map[string]string{}}
	ts := httptest.NewServer(server)
	defer ts.Close()
	a := &S3{
		Client: s3.New(s3.Options{
			Region:       "us-east-1",
			BaseEndpoint: aws.String(ts.URL),
			UsePathStyle: true,
			Credentials:  credentials.NewStaticCredentialsProvider("k", "s", ""),
		}),
		Bucket: "agent-sessions",
	}
	ctx := context.Background()
	if err := a.Put(ctx, "pi/c1/chat-x-1.jsonl", []byte("{}\n")); err != nil {
		t.Fatal(err)
	}
	if err := a.Put(ctx, "pi/c1/chat-x-1.jsonl", []byte("{}\n{}\n")); err != nil {
		t.Fatal(err)
	}
	if got := server.objects["agent-sessions/pi/c1/chat-x-1.jsonl"]; got != "{}\n{}\n" {
		t.Errorf("object %q", got)
	}
	want := []string{
		"PUT /agent-sessions/pi/c1/chat-x-1.jsonl", // NoSuchBucket
		"PUT /agent-sessions",
		"PUT /agent-sessions/pi/c1/chat-x-1.jsonl",
		"PUT /agent-sessions/pi/c1/chat-x-1.jsonl",
	}
	if len(server.calls) != len(want) {
		t.Fatalf("calls %v", server.calls)
	}
	for i := range want {
		if server.calls[i] != want[i] {
			t.Errorf("call %d: %s, want %s", i, server.calls[i], want[i])
		}
	}
}
