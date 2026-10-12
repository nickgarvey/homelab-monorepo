package backup

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	v4 "github.com/aws/aws-sdk-go-v2/aws/signer/v4"
)

// RGWAdmin reads RGW's metadata, as `radosgw-admin metadata list/get` does.
type RGWAdmin interface {
	// MetadataSections lists the sections (user, account, roles, bucket, ...).
	MetadataSections(ctx context.Context) ([]string, error)
	// MetadataKeys lists a section's keys.
	MetadataKeys(ctx context.Context, section string) ([]string, error)
	// MetadataGet is one entry, as JSON.
	MetadataGet(ctx context.Context, section, key string) ([]byte, error)
}

// AdminOps is RGWAdmin over RGW's admin-ops HTTPS API (/admin/metadata),
// signed with SigV4 as the admin-ops user, whose access_key and secret_key
// are files in KeyDir. Its caps are read-only.
type AdminOps struct {
	Endpoint string
	KeyDir   string
	HTTP     *http.Client
}

// adminRegion is the region RGW's SigV4 check expects: the zonegroup's.
const adminRegion = "default"

var emptyPayloadHash = func() string { h := sha256.Sum256(nil); return hex.EncodeToString(h[:]) }()

func (c *AdminOps) get(ctx context.Context, path string, query url.Values) ([]byte, error) {
	ak, err := readSecret(c.KeyDir, "access_key")
	if err != nil {
		return nil, err
	}
	sk, err := readSecret(c.KeyDir, "secret_key")
	if err != nil {
		return nil, err
	}
	u := strings.TrimRight(c.Endpoint, "/") + path
	if len(query) > 0 {
		u += "?" + query.Encode()
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("X-Amz-Content-Sha256", emptyPayloadHash)
	creds := aws.Credentials{AccessKeyID: ak, SecretAccessKey: sk}
	if err := v4.NewSigner().SignHTTP(ctx, creds, req, emptyPayloadHash, "s3", adminRegion, time.Now()); err != nil {
		return nil, err
	}
	hc := c.HTTP
	if hc == nil {
		hc = http.DefaultClient
	}
	resp, err := hc.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, err
	}
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("GET %s: %s: %s", req.URL.Path, resp.Status, strings.TrimSpace(string(body)))
	}
	return body, nil
}

func (c *AdminOps) list(ctx context.Context, path string) ([]string, error) {
	body, err := c.get(ctx, path, nil)
	if err != nil {
		return nil, err
	}
	var out []string
	if err := json.Unmarshal(body, &out); err != nil {
		return nil, fmt.Errorf("GET %s: %w", path, err)
	}
	return out, nil
}

func (c *AdminOps) MetadataSections(ctx context.Context) ([]string, error) {
	return c.list(ctx, "/admin/metadata")
}

func (c *AdminOps) MetadataKeys(ctx context.Context, section string) ([]string, error) {
	return c.list(ctx, "/admin/metadata/"+url.PathEscape(section))
}

func (c *AdminOps) MetadataGet(ctx context.Context, section, key string) ([]byte, error) {
	return c.get(ctx, "/admin/metadata/"+url.PathEscape(section), url.Values{"key": {key}})
}
