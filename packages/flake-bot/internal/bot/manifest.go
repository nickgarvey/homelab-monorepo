package bot

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// Manifest is manifest.json in the flake-bot bucket: for each host, the
// newest system built from dev. Hosts read it without credentials
// (pull-updates, ait nix-b77Zn.9) and fetch the path from celler.
type Manifest struct {
	// Commit is the newest dev commit the bot built, whether or not every
	// host built at it.
	Commit  string               `json:"commit"`
	Subject string               `json:"subject"`
	BuiltAt time.Time            `json:"built_at"`
	Hosts   map[string]HostEntry `json:"hosts"`
}

// HostEntry is one host's newest system that built.
type HostEntry struct {
	Path    string    `json:"path"`
	Commit  string    `json:"commit"`
	Subject string    `json:"subject"`
	BuiltAt time.Time `json:"built_at"`
}

// Merge returns m updated with a build of dev at commit: hosts that built
// get the new path; hosts that failed keep their previous entry.
func (m Manifest) Merge(commit, subject string, at time.Time, b BuildResult) Manifest {
	out := Manifest{Commit: commit, Subject: subject, BuiltAt: at, Hosts: map[string]HostEntry{}}
	for h, e := range m.Hosts {
		out.Hosts[h] = e
	}
	for _, t := range b.Targets {
		if t.Kind == KindHost && t.OK() {
			out.Hosts[t.Name] = HostEntry{Path: t.OutPath, Commit: commit, Subject: subject, BuiltAt: at}
		}
	}
	return out
}

// Store reads and writes the manifest.
type Store interface {
	Read(ctx context.Context) (Manifest, error)
	Write(ctx context.Context, m Manifest) error
}

// JWTSource fetches JWT-SVIDs, for RGW's STS.
type JWTSource interface {
	JWT(ctx context.Context, audience string) (string, error)
}

// S3Store keeps the manifest in an RGW bucket with the AWS CLI, which gets
// credentials from RGW's STS for the worker's JWT-SVID. The bucket's policy
// lets anyone read the manifest object, so hosts need no credentials.
type S3Store struct {
	Run      Runner
	AWS      string // path to aws
	Endpoint string // https://rgw.home.garvey.sh
	RoleARN  string
	Bucket   string
	Key      string
	SVID     JWTSource
	// TokenDir holds the JWT file the AWS CLI reads (private to the worker).
	TokenDir string
}

const rgwAudience = "rgw.home.garvey.sh"

func (s *S3Store) env(ctx context.Context) ([]string, error) {
	tok, err := s.SVID.JWT(ctx, rgwAudience)
	if err != nil {
		return nil, fmt.Errorf("JWT-SVID: %w", err)
	}
	f := filepath.Join(s.TokenDir, "rgw-jwt")
	if err := os.WriteFile(f, []byte(tok), 0o600); err != nil {
		return nil, err
	}
	return []string{
		"AWS_WEB_IDENTITY_TOKEN_FILE=" + f,
		"AWS_ROLE_ARN=" + s.RoleARN,
		"AWS_ROLE_SESSION_NAME=flake-bot",
		"AWS_ENDPOINT_URL_STS=" + s.Endpoint,
		"AWS_REGION=default",
	}, nil
}

func (s *S3Store) aws(ctx context.Context, env []string, stdin []byte, args ...string) ([]byte, error) {
	return s.Run.Run(ctx, Cmd{Name: s.AWS, Args: append([]string{"--endpoint-url", s.Endpoint}, args...), Env: env, Stdin: stdin})
}

// Read returns the stored manifest, or an empty one if there is none yet.
func (s *S3Store) Read(ctx context.Context) (Manifest, error) {
	env, err := s.env(ctx)
	if err != nil {
		return Manifest{}, err
	}
	out, err := s.aws(ctx, env, nil, "s3", "cp", "--only-show-errors", fmt.Sprintf("s3://%s/%s", s.Bucket, s.Key), "-")
	if err != nil {
		if missing(err) {
			return Manifest{}, nil
		}
		return Manifest{}, err
	}
	var m Manifest
	if err := json.Unmarshal(out, &m); err != nil {
		return Manifest{}, fmt.Errorf("manifest: %w", err)
	}
	return m, nil
}

// Write creates the bucket if it is missing, sets its policy (anyone may
// read the manifest object, nothing else) and uploads m.
func (s *S3Store) Write(ctx context.Context, m Manifest) error {
	env, err := s.env(ctx)
	if err != nil {
		return err
	}
	if _, err := s.aws(ctx, env, nil, "s3api", "head-bucket", "--bucket", s.Bucket); err != nil {
		if !missing(err) {
			return err
		}
		if _, err := s.aws(ctx, env, nil, "s3api", "create-bucket", "--bucket", s.Bucket); err != nil {
			return err
		}
	}
	if _, err := s.aws(ctx, env, nil, "s3api", "put-bucket-policy", "--bucket", s.Bucket, "--policy", s.policy()); err != nil {
		return err
	}
	body, err := json.MarshalIndent(m, "", "  ")
	if err != nil {
		return err
	}
	_, err = s.aws(ctx, env, body, "s3", "cp", "--only-show-errors", "--content-type", "application/json",
		"-", fmt.Sprintf("s3://%s/%s", s.Bucket, s.Key))
	return err
}

func (s *S3Store) policy() string {
	p, _ := json.Marshal(map[string]any{
		"Version": "2012-10-17",
		"Statement": []map[string]any{{
			"Sid":       "AnyoneReadsTheManifest",
			"Effect":    "Allow",
			"Principal": "*",
			"Action":    []string{"s3:GetObject"},
			"Resource":  []string{fmt.Sprintf("arn:aws:s3:::%s/%s", s.Bucket, s.Key)},
		}},
	})
	return string(p)
}

// missing reports whether an AWS CLI error is a 404 (no such bucket or key).
func missing(err error) bool {
	s := err.Error()
	return strings.Contains(s, "404") || strings.Contains(s, "NoSuchKey") ||
		strings.Contains(s, "NoSuchBucket") || strings.Contains(s, "Not Found")
}
