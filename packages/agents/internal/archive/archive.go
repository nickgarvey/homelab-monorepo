// Package archive stores pi's session files in a bucket on Ceph RGW. S3 is
// the real store; Fake keeps objects in memory for tests.
package archive

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"sync"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/service/s3"
	"github.com/aws/smithy-go"
)

type Archive interface {
	// Put writes the object, replacing any with the same key.
	Put(ctx context.Context, key string, body []byte) error
}

// S3 puts objects in one bucket, creating it on the first put that finds it
// missing.
type S3 struct {
	Client *s3.Client
	Bucket string
}

// NewS3 makes the client from the environment, as the AWS SDK does: in the
// worker's pod that is a web identity (its JWT-SVID, kept in a file by
// spiffe-helper) traded for the role's credentials through RGW's STS. The
// region only has to be set for request signing; RGW does not check it.
func NewS3(ctx context.Context, endpoint, bucket string) (*S3, error) {
	cfg, err := config.LoadDefaultConfig(ctx)
	if err != nil {
		return nil, err
	}
	if cfg.Region == "" {
		cfg.Region = "us-east-1"
	}
	client := s3.NewFromConfig(cfg, func(o *s3.Options) {
		o.BaseEndpoint = aws.String(endpoint)
		o.UsePathStyle = true
	})
	return &S3{Client: client, Bucket: bucket}, nil
}

func (a *S3) Put(ctx context.Context, key string, body []byte) error {
	err := a.put(ctx, key, body)
	if errorCode(err) != "NoSuchBucket" {
		return err
	}
	_, err = a.Client.CreateBucket(ctx, &s3.CreateBucketInput{Bucket: aws.String(a.Bucket)})
	if err != nil && errorCode(err) != "BucketAlreadyOwnedByYou" {
		return fmt.Errorf("creating bucket %s: %w", a.Bucket, err)
	}
	return a.put(ctx, key, body)
}

// errorCode is the S3 error code of err, such as NoSuchBucket. PutObject
// does not model NoSuchBucket as a type, so it is matched by code.
func errorCode(err error) string {
	var api smithy.APIError
	if errors.As(err, &api) {
		return api.ErrorCode()
	}
	return ""
}

func (a *S3) put(ctx context.Context, key string, body []byte) error {
	_, err := a.Client.PutObject(ctx, &s3.PutObjectInput{
		Bucket:      aws.String(a.Bucket),
		Key:         aws.String(key),
		Body:        bytes.NewReader(body),
		ContentType: aws.String("application/x-ndjson"),
	})
	return err
}

// Fake is an in-memory Archive.
type Fake struct {
	mu      sync.Mutex
	Objects map[string][]byte
	// Err, if set, is returned by every Put.
	Err error
}

func NewFake() *Fake { return &Fake{Objects: map[string][]byte{}} }

func (f *Fake) Put(_ context.Context, key string, body []byte) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.Err != nil {
		return f.Err
	}
	f.Objects[key] = append([]byte(nil), body...)
	return nil
}
