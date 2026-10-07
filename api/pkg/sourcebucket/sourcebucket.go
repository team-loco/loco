package sourcebucket

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	awshttp "github.com/aws/aws-sdk-go-v2/aws/transport/http"
	"github.com/aws/aws-sdk-go-v2/credentials"
	"github.com/aws/aws-sdk-go-v2/service/s3"
	"github.com/aws/aws-sdk-go-v2/service/s3/types"
)

var ErrObjectNotFound = errors.New("object not found")

type Object struct {
	Key          string
	LastModified time.Time
}

type Config struct {
	Endpoint        string
	Bucket          string
	Region          string
	AccessKeyID     string
	SecretAccessKey string
}

func (c Config) Validate() error {
	var missing []string
	if c.Bucket == "" {
		missing = append(missing, "bucket")
	}
	if c.Region == "" {
		missing = append(missing, "region")
	}
	if c.AccessKeyID == "" {
		missing = append(missing, "access key id")
	}
	if c.SecretAccessKey == "" {
		missing = append(missing, "secret access key")
	}
	if len(missing) > 0 {
		return fmt.Errorf("source bucket config is missing %v", missing)
	}
	return nil
}

type Bucket struct {
	client    *s3.Client
	presigner *s3.PresignClient
	bucket    string
}

func New(cfg Config) (*Bucket, error) {
	if err := cfg.Validate(); err != nil {
		return nil, err
	}

	creds := credentials.NewStaticCredentialsProvider(cfg.AccessKeyID, cfg.SecretAccessKey, "")
	endpoint := cfg.Endpoint
	client := s3.New(s3.Options{
		Region:                     cfg.Region,
		Credentials:                creds,
		UsePathStyle:               false,
		RequestChecksumCalculation: aws.RequestChecksumCalculationWhenRequired,
		ResponseChecksumValidation: aws.ResponseChecksumValidationWhenRequired,
	}, func(o *s3.Options) {
		if endpoint != "" {
			o.BaseEndpoint = &endpoint
		}
	})
	presigner := s3.NewPresignClient(client)

	return &Bucket{
		client:    client,
		presigner: presigner,
		bucket:    cfg.Bucket,
	}, nil
}

func (b *Bucket) PresignPut(ctx context.Context, key string, size int64, ttl time.Duration) (string, error) {
	bucket := b.bucket
	input := &s3.PutObjectInput{
		Bucket:        &bucket,
		Key:           &key,
		ContentLength: &size,
	}
	expires := s3.WithPresignExpires(ttl)
	req, err := b.presigner.PresignPutObject(ctx, input, expires)
	if err != nil {
		return "", fmt.Errorf("presign put %s: %w", key, err)
	}
	return req.URL, nil
}

func (b *Bucket) Delete(ctx context.Context, key string) error {
	bucket := b.bucket
	_, err := b.client.DeleteObject(ctx, &s3.DeleteObjectInput{
		Bucket: &bucket,
		Key:    &key,
	})
	if err != nil && !isNotFound(err) {
		return fmt.Errorf("delete %s: %w", key, err)
	}
	return nil
}

func (b *Bucket) List(ctx context.Context, prefix, startAfter string, limit int32) ([]Object, bool, error) {
	bucket := b.bucket
	input := &s3.ListObjectsV2Input{
		Bucket:  &bucket,
		Prefix:  &prefix,
		MaxKeys: &limit,
	}
	if startAfter != "" {
		input.StartAfter = &startAfter
	}
	out, err := b.client.ListObjectsV2(ctx, input)
	if err != nil {
		return nil, false, fmt.Errorf("list %s: %w", prefix, err)
	}
	objects := make([]Object, 0, len(out.Contents))
	for _, o := range out.Contents {
		key := aws.ToString(o.Key)
		modified := aws.ToTime(o.LastModified)
		objects = append(objects, Object{Key: key, LastModified: modified})
	}
	truncated := aws.ToBool(out.IsTruncated)
	return objects, truncated, nil
}

func (b *Bucket) ObjectSize(ctx context.Context, key string) (int64, error) {
	bucket := b.bucket
	out, err := b.client.HeadObject(ctx, &s3.HeadObjectInput{
		Bucket: &bucket,
		Key:    &key,
	})
	if isNotFound(err) {
		return 0, ErrObjectNotFound
	}
	if err != nil {
		return 0, fmt.Errorf("head %s: %w", key, err)
	}
	return aws.ToInt64(out.ContentLength), nil
}

func isNotFound(err error) bool {
	if err == nil {
		return false
	}
	if notFound, ok := errors.AsType[*types.NotFound](err); ok && notFound != nil {
		return true
	}
	if noSuchKey, ok := errors.AsType[*types.NoSuchKey](err); ok && noSuchKey != nil {
		return true
	}
	var respErr *awshttp.ResponseError
	return errors.As(err, &respErr) && respErr.HTTPStatusCode() == http.StatusNotFound
}
